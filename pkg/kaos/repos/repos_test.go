// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-01/F-02): new file, Apache-2.0.

package repos_test

import (
	"context"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/treeverse/lakefs/pkg/catalog"
	"github.com/treeverse/lakefs/pkg/graveler"
	"github.com/treeverse/lakefs/pkg/kaos/datasetevents"
	"github.com/treeverse/lakefs/pkg/kaos/repos"
)

type fakeCatalog struct {
	mu      sync.Mutex // Run (kick_test.go) reads it from its own goroutine
	repos   map[string]*catalog.Repository
	md      map[string]graveler.RepositoryMetadata
	created []string
	empty   map[string]bool // repositories whose default branch has only the initial commit
}

func newFake() *fakeCatalog {
	return &fakeCatalog{repos: map[string]*catalog.Repository{}, md: map[string]graveler.RepositoryMetadata{}, empty: map[string]bool{}}
}

func (f *fakeCatalog) add(name string, md map[string]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.repos[name] = &catalog.Repository{Name: name}
	f.md[name] = maps.Clone(md)
}

func (f *fakeCatalog) GetRepository(_ context.Context, name string) (*catalog.Repository, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r, ok := f.repos[name]; ok {
		return r, nil
	}
	return nil, graveler.ErrRepositoryNotFound
}

func (f *fakeCatalog) CreateRepository(_ context.Context, name, storageID, ns, branch string, _ bool) (*catalog.Repository, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := &catalog.Repository{Name: name, StorageID: storageID, StorageNamespace: ns, DefaultBranch: branch}
	f.repos[name], f.created = r, append(f.created, name)
	return r, nil
}

func (f *fakeCatalog) GetCommit(_ context.Context, name, _ string) (*catalog.CommitLog, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.repos[name]; !ok {
		return nil, graveler.ErrRepositoryNotFound
	}
	if f.empty[name] {
		return &catalog.CommitLog{}, nil
	}
	return &catalog.CommitLog{Parents: []string{"initial"}}, nil
}

func (f *fakeCatalog) GetRepositoryMetadata(_ context.Context, name string) (graveler.RepositoryMetadata, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return maps.Clone(f.md[name]), nil
}

func (f *fakeCatalog) UpdateRepositoryMetadata(_ context.Context, name string, md graveler.RepositoryMetadata) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.md[name] == nil {
		f.md[name] = graveler.RepositoryMetadata{}
	}
	maps.Copy(f.md[name], md)
	return nil
}

func (f *fakeCatalog) DeleteRepositoryMetadata(_ context.Context, name string, keys []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, k := range keys {
		delete(f.md[name], k)
	}
	return nil
}

func (f *fakeCatalog) ListRepositories(_ context.Context, limit int, prefix, _, after string, _ ...catalog.ListRepositoriesOptionsFunc) ([]*catalog.Repository, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	names := slices.Sorted(maps.Keys(f.repos))
	var page []*catalog.Repository
	for _, n := range names {
		if strings.HasPrefix(n, prefix) && n > after {
			page = append(page, f.repos[n])
		}
	}
	if len(page) > limit {
		return page[:limit], true, nil
	}
	return page, false, nil
}

const prefix = "gs://kaos-e2e-lakefs/"

var want = repos.Desired("dataset-default", map[string]string{"yolo": "outputs-yolo", "hera": "outputs-hera"}, nil)

// AC-01: a new project gets its default dataset and one outputs repository per app, labelled.
func TestReconcile_CreatesAndLabelsTheDeclaredRepositories(t *testing.T) {
	f := newFake()
	changes, err := repos.Reconcile(t.Context(), f, "", prefix, "", want)
	require.NoError(t, err)
	require.Equal(t, []string{"dataset-default", "outputs-hera", "outputs-yolo"}, f.created)
	require.Equal(t, "gs://kaos-e2e-lakefs/outputs-yolo", f.repos["outputs-yolo"].StorageNamespace)
	require.Equal(t, "main", f.repos["outputs-yolo"].DefaultBranch)
	require.Equal(t, graveler.RepositoryMetadata{repos.KeyKind: "dataset", repos.KeyDefault: "true"}, f.md["dataset-default"])
	require.Equal(t, graveler.RepositoryMetadata{repos.KeyKind: "outputs", repos.KeyApp: "yolo"}, f.md["outputs-yolo"])
	require.NotEmpty(t, changes)

	again, err := repos.Reconcile(t.Context(), f, "", prefix, "", want)
	require.NoError(t, err)
	require.Empty(t, again, "a second pass changes nothing")
}

// AC-01: a repository someone deleted comes back; labels someone removed are put back; a
// person's own labels stay.
func TestReconcile_Repairs(t *testing.T) {
	f := newFake()
	f.add("dataset-default", map[string]string{repos.KeyDescription: "our data"})
	_, err := repos.Reconcile(t.Context(), f, "", prefix, "", want)
	require.NoError(t, err)
	require.NotContains(t, f.created, "dataset-default", "an existing repository is never recreated")
	require.Equal(t, "our data", f.md["dataset-default"][repos.KeyDescription])
	require.Equal(t, "true", f.md["dataset-default"][repos.KeyDefault])
}

// T-03: an outputs repository no app declares is labelled orphaned, never deleted; declared again,
// the label goes.
func TestReconcile_KeepsAndLabelsOrphanedOutputs(t *testing.T) {
	f := newFake()
	f.add("outputs-old", map[string]string{repos.KeyKind: "outputs", repos.KeyApp: "old"})
	f.add("outputs-handmade", nil) // not the platform's: left alone
	_, err := repos.Reconcile(t.Context(), f, "", prefix, "", want)
	require.NoError(t, err)
	require.Contains(t, f.repos, "outputs-old")
	require.Equal(t, "true", f.md["outputs-old"][repos.KeyOrphaned])
	require.Empty(t, f.md["outputs-handmade"])

	back := repos.Desired("dataset-default", map[string]string{"old": "outputs-old"}, nil)
	_, err = repos.Reconcile(t.Context(), f, "", prefix, "", back)
	require.NoError(t, err)
	require.NotContains(t, f.md["outputs-old"], repos.KeyOrphaned)
}

// CON-02: a person labels only a dataset-* repository a dataset; the platform's keys and the
// outputs kind are refused, saying why.
func TestValidateMetadata(t *testing.T) {
	for _, c := range []struct {
		name string
		md   map[string]string
		ok   bool
	}{
		{"dataset-cars", map[string]string{repos.KeyKind: "dataset", repos.KeyDescription: "cars"}, true},
		{"anything", map[string]string{repos.KeyDescription: "free text"}, true},
		{"cars", map[string]string{repos.KeyKind: "dataset"}, false},
		{"dataset-ab", map[string]string{repos.KeyKind: "dataset"}, false},
		{"dataset-Cars", map[string]string{repos.KeyKind: "dataset"}, false},
		{"outputs-yolo", map[string]string{repos.KeyKind: "outputs"}, false},
		{"dataset-cars", map[string]string{repos.KeyDefault: "true"}, false},
		{"dataset-cars", map[string]string{repos.KeyApp: "yolo"}, false},
		// PRD novelcore/kaos#1911 CON-02: who produces a dataset is the platform's to say.
		{"dataset-cars", map[string]string{repos.KeyProducer: "yolo"}, false},
		{"dataset-cars", map[string]string{repos.KeyProducer: ""}, false},
		{"dataset-cars", map[string]string{repos.KeyProducer + ".filtered": "yolo"}, false},
		{"dataset-cars", map[string]string{"kaos.producers": "free text"}, true},
		{"dataset-cars", map[string]string{repos.KeyKind: "table"}, false},
	} {
		err := repos.ValidateMetadata(c.name, c.md)
		if c.ok {
			require.NoError(t, err, "%s %v", c.name, c.md)
		} else {
			require.ErrorIs(t, err, repos.ErrInvalid, "%s %v", c.name, c.md)
		}
	}
	require.ErrorIs(t, repos.ValidateDelete([]string{repos.KeyKind}), repos.ErrInvalid)
	require.ErrorIs(t, repos.ValidateDelete([]string{repos.KeyOrphaned}), repos.ErrInvalid)
	require.ErrorIs(t, repos.ValidateDelete([]string{repos.KeyProducer}), repos.ErrInvalid)
	require.ErrorIs(t, repos.ValidateDelete([]string{repos.KeyProducer + ".filtered"}), repos.ErrInvalid)
	require.NoError(t, repos.ValidateDelete([]string{repos.KeyDescription}))
}

// F-13 / AC-12: every repository whose storage is under the prefix carries the location; others
// (another bucket) are left alone; people cannot set it.
func TestReconcile_LabelsLocation(t *testing.T) {
	f := newFake()
	f.add("dataset-cars", nil)
	f.repos["dataset-cars"].StorageNamespace = prefix + "dataset-cars"
	f.add("elsewhere", nil)
	f.repos["elsewhere"].StorageNamespace = "gs://other-bucket/elsewhere"
	const where = "europe-west1 · Belgium"
	_, err := repos.Reconcile(t.Context(), f, "", prefix, where, want)
	require.NoError(t, err)
	for _, name := range []string{"dataset-cars", "dataset-default", "outputs-yolo", "outputs-hera"} {
		require.Equal(t, where, f.md[name][repos.KeyLocation], name)
	}
	require.Empty(t, f.md["elsewhere"][repos.KeyLocation])
	require.ErrorIs(t, repos.ValidateMetadata("dataset-cars", map[string]string{repos.KeyLocation: "Mars"}), repos.ErrInvalid)
}

// D-09: kaos.shared_with lists project names, on datasets only.
func TestValidateMetadata_SharedWith(t *testing.T) {
	require.NoError(t, repos.ValidateMetadata("dataset-cars", map[string]string{repos.KeySharedWith: "orbits, vision-2 ,"}))
	require.NoError(t, repos.ValidateMetadata("dataset-cars", map[string]string{repos.KeySharedWith: ""}))
	require.ErrorIs(t, repos.ValidateMetadata("outputs-yolo", map[string]string{repos.KeySharedWith: "orbits"}), repos.ErrInvalid)
	require.ErrorIs(t, repos.ValidateMetadata("dataset-cars", map[string]string{repos.KeySharedWith: "Orbits"}), repos.ErrInvalid)
	require.ErrorIs(t, repos.ValidateMetadata("dataset-cars", map[string]string{repos.KeySharedWith: "a/b"}), repos.ErrInvalid)
	require.Equal(t, []string{"orbits", "vision-2"}, repos.SharedWith(" orbits,vision-2,,orbits "))
	require.NoError(t, repos.ValidateDelete([]string{repos.KeySharedWith}))
	require.ErrorIs(t, repos.ValidateDelete([]string{repos.KeyLocation}), repos.ErrInvalid)
}

// PRD novelcore/kaos#1283 (owner 2026-11-01): templates are optional. A dataset-* repository made
// by hand gets kaos.kind=dataset; its own labels stay; a name that is not a valid dataset name, a
// repository already labelled and any other repository are left alone.
func TestReconcile_LabelsHandmadeDatasets(t *testing.T) {
	f := newFake()
	f.add("dataset-cars", nil)
	f.add("dataset-trucks", map[string]string{repos.KeyDescription: "ours"})
	f.add("dataset-ab", nil)
	f.add("dataset-made", map[string]string{repos.KeyKind: repos.KindDataset, repos.KeyTemplate: "yolo"})
	f.add("scratch", nil)
	f.add("dataset-fresh", nil)
	f.empty["dataset-fresh"] = true
	changes, err := repos.Reconcile(t.Context(), f, "", prefix, "", want)
	require.NoError(t, err)
	require.Equal(t, repos.KindDataset, f.md["dataset-cars"][repos.KeyKind])
	require.Equal(t, graveler.RepositoryMetadata{repos.KeyKind: repos.KindDataset, repos.KeyDescription: "ours"}, f.md["dataset-trucks"])
	require.Empty(t, f.md["dataset-ab"])
	require.Equal(t, "yolo", f.md["dataset-made"][repos.KeyTemplate])
	require.Empty(t, f.md["scratch"])
	require.Empty(t, f.md["dataset-fresh"], "an empty repository is labelled only after its first commit")
	require.Contains(t, changes, "labelled dataset-cars a dataset: made without a template")

	again, err := repos.Reconcile(t.Context(), f, "", prefix, "", want)
	require.NoError(t, err)
	require.Empty(t, again, "a second pass changes nothing")

	f.empty["dataset-fresh"] = false // someone uploaded and committed
	_, err = repos.Reconcile(t.Context(), f, "", prefix, "", want)
	require.NoError(t, err)
	require.Equal(t, repos.KindDataset, f.md["dataset-fresh"][repos.KeyKind])
}

// PRD novelcore/kaos#1911 F-10: a dataset an app declares is created, labelled a dataset of that
// producer, and announced at once, so pipelines can pick it before its first run.
func TestReconcile_CreatesProducedDatasets(t *testing.T) {
	c := &count{}
	n := datasetevents.NewNotifier(c, nil).WithTiming(20*time.Millisecond, time.Millisecond)
	datasetevents.Activate(n)
	t.Cleanup(func() { datasetevents.Activate(nil) })

	f := newFake()
	want := repos.Desired("dataset-default", map[string]string{"yolo": "outputs-yolo"},
		map[string]string{"dataset-clean": "yolo", "dataset-embeddings": "hera"})
	changes, err := repos.Reconcile(t.Context(), f, "", prefix, "", want)
	require.NoError(t, err)
	require.Equal(t, []string{"dataset-default", "outputs-yolo", "dataset-clean", "dataset-embeddings"}, f.created)
	require.Equal(t, "gs://kaos-e2e-lakefs/dataset-clean", f.repos["dataset-clean"].StorageNamespace)
	require.Equal(t, graveler.RepositoryMetadata{repos.KeyKind: "dataset", repos.KeyProducer: "yolo"}, f.md["dataset-clean"])
	require.Equal(t, graveler.RepositoryMetadata{repos.KeyKind: "dataset", repos.KeyProducer: "hera"}, f.md["dataset-embeddings"])
	require.NotContains(t, f.md["dataset-default"], repos.KeyProducer)
	require.NotEmpty(t, changes)
	n.Wait()
	require.EqualValues(t, 1, c.n.Load(), "the new datasets are announced, in one event")

	again, err := repos.Reconcile(t.Context(), f, "", prefix, "", want)
	require.NoError(t, err)
	require.Empty(t, again, "a second pass changes nothing")
	n.Wait()
	require.EqualValues(t, 1, c.n.Load(), "and announces nothing")

	// A producer label someone removed or changed in the store is put back.
	f.md["dataset-clean"][repos.KeyProducer] = "other"
	_, err = repos.Reconcile(t.Context(), f, "", prefix, "", want)
	require.NoError(t, err)
	require.Equal(t, "yolo", f.md["dataset-clean"][repos.KeyProducer])
}
