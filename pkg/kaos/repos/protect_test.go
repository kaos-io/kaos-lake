// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-03/F-05): new file, Apache-2.0.

package repos_test

import (
	"context"
	"errors"
	"path"
	"testing"
	"time"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"github.com/treeverse/lakefs/pkg/block"
	"github.com/treeverse/lakefs/pkg/cache"
	"github.com/treeverse/lakefs/pkg/catalog"
	"github.com/treeverse/lakefs/pkg/config"
	"github.com/treeverse/lakefs/pkg/graveler"
	"github.com/treeverse/lakefs/pkg/graveler/settings"
	"github.com/treeverse/lakefs/pkg/kaos/repos"
	"github.com/treeverse/lakefs/pkg/kv/kvtest"
	"github.com/treeverse/lakefs/pkg/kv/mem"
	"github.com/treeverse/lakefs/pkg/upload"
)

// realCatalog is a lakeFS catalog over in-memory storage: branch protection is enforced by the
// real core, not a fake.
func realCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	ctx := context.WithoutCancel(t.Context())
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set(config.BlockstoreTypeKey, block.BlockstoreTypeMem)
	viper.Set("database.type", mem.DriverName)
	viper.Set("committed.local_cache.size_bytes", 24*1024*1024)
	viper.Set("committed.sstable.memory.cache_size_bytes", 2*1024*1024)
	cfg := &config.ConfigImpl{}
	_, err := config.NewConfig("", cfg)
	require.NoError(t, err)
	cfg.Committed.LocalCache.Dir = path.Join(t.TempDir(), "cache")
	c, err := catalog.New(ctx, catalog.Config{
		Config:                cfg,
		KVStore:               kvtest.GetStore(ctx, t),
		SettingsManagerOption: settings.WithCache(cache.NoCache),
		PathProvider:          upload.DefaultPathProvider,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func addFile(t *testing.T, c *catalog.Catalog, repo, branch, name string) error {
	t.Helper()
	return c.CreateEntry(t.Context(), repo, branch, catalog.DBEntry{
		Path: name, PhysicalAddress: "mem://x/" + name, CreationDate: time.Now(), Size: 1, Checksum: name,
	})
}

func newRepo(t *testing.T, c *catalog.Catalog, name string, labels map[string]string) {
	t.Helper()
	_, err := c.CreateRepository(t.Context(), name, "", "mem://"+name, "main", false)
	require.NoError(t, err)
	require.NoError(t, addFile(t, c, name, "main", "start.txt"))
	_, err = c.Commit(t.Context(), name, "main", "start", "tester", nil, nil, nil, false)
	require.NoError(t, err)
	if labels != nil {
		require.NoError(t, c.UpdateRepositoryMetadata(t.Context(), name, labels))
	}
}

// Datasets (by label) are protected: no direct write or commit to main, but a merge lands. Outputs
// repositories and unlabelled ones are not.
func TestReconcileProtection(t *testing.T) {
	ctx := t.Context()
	c := realCatalog(t)
	newRepo(t, c, "dataset-cars", map[string]string{repos.KeyKind: repos.KindDataset})
	newRepo(t, c, "dataset-new", nil) // created, label not set yet: the New dataset flow's first commit
	newRepo(t, c, "outputs-yolo", map[string]string{repos.KeyKind: repos.KindOutputs})

	// Before the label, main takes the first commit; with it, nothing protects yet until reconcile.
	require.NoError(t, addFile(t, c, "dataset-cars", "main", "before.txt"))
	_, err := c.Commit(ctx, "dataset-cars", "main", "before", "tester", nil, nil, nil, false)
	require.NoError(t, err)

	changes, err := repos.ReconcileProtection(ctx, c, c)
	require.NoError(t, err)
	require.Equal(t, []string{"protected main of dataset-cars: only a merge may change it"}, changes)

	// Direct writes and commits to main are refused.
	require.ErrorIs(t, addFile(t, c, "dataset-cars", "main", "direct.txt"), graveler.ErrWriteToProtectedBranch)
	_, err = c.Commit(ctx, "dataset-cars", "main", "direct", "tester", nil, nil, nil, true)
	require.ErrorIs(t, err, graveler.ErrCommitToProtectedBranch)

	// A draft branch, committed and merged, still lands on main.
	_, err = c.CreateBranch(ctx, "dataset-cars", "edit-1", "main")
	require.NoError(t, err)
	require.NoError(t, addFile(t, c, "dataset-cars", "edit-1", "images.txt"))
	_, err = c.Commit(ctx, "dataset-cars", "edit-1", "edit", "tester", nil, nil, nil, false)
	require.NoError(t, err)
	_, err = c.Merge(ctx, "dataset-cars", "main", "edit-1", "tester", "merge edit-1", nil, "")
	require.NoError(t, err, "a merge into a protected main must work")
	_, err = c.GetEntry(ctx, "dataset-cars", "main", "images.txt", catalog.GetEntryParams{})
	require.NoError(t, err)

	// Not labelled yet, or outputs: open.
	require.NoError(t, addFile(t, c, "dataset-new", "main", "more.txt"))
	require.NoError(t, addFile(t, c, "outputs-yolo", "main", "run1/model.pt"))
	_, err = c.Commit(ctx, "outputs-yolo", "main", "run", "tester", nil, nil, nil, false)
	require.NoError(t, err)

	// Idempotent; a rule deleted by hand comes back; removing the label does not remove it.
	changes, err = repos.ReconcileProtection(ctx, c, c)
	require.NoError(t, err)
	require.Empty(t, changes)
	require.NoError(t, c.SetBranchProtectionRules(ctx, "dataset-cars", &graveler.BranchProtectionRules{}, nil))
	require.NoError(t, addFile(t, c, "dataset-cars", "main", "sneaky.txt"))
	changes, err = repos.ReconcileProtection(ctx, c, c)
	require.NoError(t, err)
	require.Len(t, changes, 1)
	require.ErrorIs(t, addFile(t, c, "dataset-cars", "main", "again.txt"), graveler.ErrWriteToProtectedBranch)

	require.NoError(t, c.DeleteRepositoryMetadata(ctx, "dataset-cars", []string{repos.KeyKind}))
	_, err = repos.ReconcileProtection(ctx, c, c)
	require.NoError(t, err)
	require.ErrorIs(t, addFile(t, c, "dataset-cars", "main", "nolabel.txt"), graveler.ErrWriteToProtectedBranch)
}

// Rules for other branches are kept, and an existing main rule is completed, not replaced.
func TestReconcileProtection_KeepsOtherRules(t *testing.T) {
	ctx := t.Context()
	c := realCatalog(t)
	newRepo(t, c, "dataset-cars", map[string]string{repos.KeyKind: repos.KindDataset})
	require.NoError(t, c.SetBranchProtectionRules(ctx, "dataset-cars", &graveler.BranchProtectionRules{
		BranchPatternToBlockedActions: map[string]*graveler.BranchProtectionBlockedActions{
			"release-*": {Value: []graveler.BranchProtectionBlockedAction{graveler.BranchProtectionBlockedAction_COMMIT}},
			"main":      {Value: []graveler.BranchProtectionBlockedAction{graveler.BranchProtectionBlockedAction_COMMIT}},
		},
	}, nil))
	_, err := repos.ReconcileProtection(ctx, c, c)
	require.NoError(t, err)
	rules, _, err := c.GetBranchProtectionRules(ctx, "dataset-cars")
	require.NoError(t, err)
	require.Len(t, rules.BranchPatternToBlockedActions, 2)
	require.ElementsMatch(t, []graveler.BranchProtectionBlockedAction{
		graveler.BranchProtectionBlockedAction_COMMIT, graveler.BranchProtectionBlockedAction_STAGING_WRITE,
	}, rules.BranchPatternToBlockedActions["main"].Value)
}

// PRD novelcore/kaos#1911 D-04, AC-07: a dataset an app produces is protected from the pass that
// creates it. Its producer may commit on main by policy (that is the merge), but the core refuses a
// direct write or commit there: main moves only when a run-* branch is merged.
func TestProducedDataset_MainMovesOnlyByMerge(t *testing.T) {
	ctx := t.Context()
	c := realCatalog(t)
	want := repos.Desired("", nil, map[string]string{"dataset-clean": "yolo"})
	_, err := repos.Reconcile(ctx, c, "", "mem://lake", "", want)
	require.NoError(t, err)
	_, err = repos.ReconcileProtection(ctx, c, c)
	require.NoError(t, err)
	md, err := c.GetRepositoryMetadata(ctx, "dataset-clean")
	require.NoError(t, err)
	require.Equal(t, "yolo", md[repos.KeyProducer])

	require.ErrorIs(t, addFile(t, c, "dataset-clean", "main", "direct.txt"), graveler.ErrWriteToProtectedBranch)
	_, err = c.Commit(ctx, "dataset-clean", "main", "direct", "kaos-app-yolo", nil, nil, nil, true)
	require.ErrorIs(t, err, graveler.ErrCommitToProtectedBranch)

	_, err = c.CreateBranch(ctx, "dataset-clean", "run-7f3a", "main")
	require.NoError(t, err)
	require.NoError(t, addFile(t, c, "dataset-clean", "run-7f3a", "images/1.jpg"))
	_, err = c.Commit(ctx, "dataset-clean", "run-7f3a", "run 7f3a", "kaos-app-yolo", nil, nil, nil, false)
	require.NoError(t, err)
	_, err = c.Merge(ctx, "dataset-clean", "main", "run-7f3a", "kaos-app-yolo", "promote run 7f3a", nil, "")
	require.NoError(t, err)
	_, err = c.GetEntry(ctx, "dataset-clean", "main", "images/1.jpg", catalog.GetEntryParams{})
	require.NoError(t, err)
}

// PRD novelcore/kaos#1911 CON-02, CON-03: a produced dataset is ready for its app only once it
// exists, names that app as producer and has main protected.
func TestProducedBy(t *testing.T) {
	ctx := t.Context()
	c := realCatalog(t)
	ready := func(dataset, app string) bool {
		ok, err := repos.ProducedBy(ctx, c, dataset, app)
		require.NoError(t, err)
		return ok
	}
	require.False(t, ready("dataset-clean", "yolo"), "no repository yet")

	_, err := repos.Reconcile(ctx, c, "", "mem://lake", "", repos.Desired("", nil, map[string]string{"dataset-clean": "yolo"}))
	require.NoError(t, err)
	require.False(t, ready("dataset-clean", "yolo"), "labelled, main not protected yet")

	_, err = repos.ReconcileProtection(ctx, c, c)
	require.NoError(t, err)
	require.True(t, ready("dataset-clean", "yolo"))
	require.False(t, ready("dataset-clean", "hera"), "another app")
	require.False(t, ready("dataset-clean", ""))

	// Half a rule is not protection; a dataset people upload is nobody's to produce.
	require.NoError(t, c.SetBranchProtectionRules(ctx, "dataset-clean", &graveler.BranchProtectionRules{
		BranchPatternToBlockedActions: map[string]*graveler.BranchProtectionBlockedActions{
			"main": {Value: []graveler.BranchProtectionBlockedAction{graveler.BranchProtectionBlockedAction_STAGING_WRITE}},
		},
	}, nil))
	require.False(t, ready("dataset-clean", "yolo"), "commits to main not blocked")
	newRepo(t, c, "dataset-cars", map[string]string{repos.KeyKind: repos.KindDataset})
	_, err = repos.ReconcileProtection(ctx, c, c)
	require.NoError(t, err)
	require.True(t, ready("dataset-clean", "yolo"), "the rule is completed again")
	require.False(t, ready("dataset-cars", "yolo"))
}

// brokenRules fails to read the protection rules of one repository.
type brokenRules struct {
	repos.ProtectionCatalog
	broken string
}

func (b brokenRules) GetBranchProtectionRules(ctx context.Context, repo string) (*graveler.BranchProtectionRules, *string, error) {
	if repo == b.broken {
		return nil, nil, errors.New("rules unreadable")
	}
	return b.ProtectionCatalog.GetBranchProtectionRules(ctx, repo)
}

// One dataset whose rules cannot be read or written does not leave the datasets after it open:
// every other one is protected, and every failure is reported.
func TestReconcileProtection_GoesOnAfterAFailure(t *testing.T) {
	ctx := t.Context()
	c := realCatalog(t)
	for _, name := range []string{"dataset-aaa", "dataset-bbb", "dataset-ccc"} {
		newRepo(t, c, name, map[string]string{repos.KeyKind: repos.KindDataset})
	}
	changes, err := repos.ReconcileProtection(ctx, c, brokenRules{ProtectionCatalog: c, broken: "dataset-aaa"})
	require.ErrorContains(t, err, "repository dataset-aaa: rules unreadable")
	require.Equal(t, []string{
		"protected main of dataset-bbb: only a merge may change it",
		"protected main of dataset-ccc: only a merge may change it",
	}, changes)
	require.ErrorIs(t, addFile(t, c, "dataset-ccc", "main", "direct.txt"), graveler.ErrWriteToProtectedBranch)
}

// PRD novelcore/kaos#1911 CON-02: a repository someone made under a name an app later declares is
// never the app's, whatever shape it has. Each of these looked untouched to a check of the default
// branch's head alone: data on main beside another default branch, an uncommitted upload on main,
// data on a side branch. And one nobody touched at all: still not made by the platform.
func TestReconcile_NeverAdoptsWhatSomeoneMade(t *testing.T) {
	ctx := t.Context()
	c := realCatalog(t)
	create := func(name, defaultBranch string) {
		_, err := c.CreateRepository(ctx, name, "", "mem://"+name, defaultBranch, false)
		require.NoError(t, err)
	}
	commit := func(repo, branch, file string) {
		require.NoError(t, addFile(t, c, repo, branch, file))
		_, err := c.Commit(ctx, repo, branch, "by a person", "alice", nil, nil, nil, false)
		require.NoError(t, err)
	}
	// (a) the default branch is not main; people's data is committed on main.
	create("dataset-trunk", "trunk")
	_, err := c.CreateBranch(ctx, "dataset-trunk", "main", "trunk")
	require.NoError(t, err)
	commit("dataset-trunk", "main", "people.jpg")
	// (b) an upload on main that nobody committed.
	create("dataset-staged", "main")
	require.NoError(t, addFile(t, c, "dataset-staged", "main", "uncommitted.jpg"))
	// (c) data on a side branch.
	create("dataset-side", "main")
	_, err = c.CreateBranch(ctx, "dataset-side", "theirs", "main")
	require.NoError(t, err)
	commit("dataset-side", "theirs", "people.jpg")
	// (d) nothing at all.
	create("dataset-empty", "main")

	shapes := []string{"dataset-trunk", "dataset-staged", "dataset-side", "dataset-empty"}
	producers := map[string]string{"dataset-fresh": "yolo"}
	for _, name := range shapes {
		producers[name] = "yolo"
	}
	_, err = repos.Reconcile(ctx, c, "", "mem://lake", "", repos.Desired("", nil, producers))
	require.ErrorIs(t, err, repos.ErrNotAdopted)
	_, perr := repos.ReconcileProtection(ctx, c, c)
	require.NoError(t, perr)
	for _, name := range shapes {
		require.ErrorContains(t, err, "repository "+name+": dataset not adopted: app yolo declares it, but it exists and the platform did not create it for yolo")
		md, merr := c.GetRepositoryMetadata(ctx, name)
		require.NoError(t, merr)
		require.Empty(t, md, "%s keeps its labels: none", name)
		ready, rerr := repos.ProducedBy(ctx, c, name, "yolo")
		require.NoError(t, rerr)
		require.False(t, ready, "%s is never ready for the app", name)
	}
	// What the person made is still theirs to change.
	require.NoError(t, addFile(t, c, "dataset-staged", "main", "more.jpg"))
	require.NoError(t, c.DeleteBranch(ctx, "dataset-side", "theirs"))

	// The one the reconcile made carries its labels from the moment it exists.
	md, err := c.GetRepositoryMetadata(ctx, "dataset-fresh")
	require.NoError(t, err)
	require.Equal(t, graveler.RepositoryMetadata{repos.KeyKind: repos.KindDataset, repos.KeyProducer: "yolo"}, md)
}

// A repository created with metadata is never seen without it: the labels are in place by the
// time the repository can be read at all.
func TestCreateRepositoryWithMetadata(t *testing.T) {
	ctx := t.Context()
	c := realCatalog(t)
	_, err := c.GetRepositoryMetadata(ctx, "dataset-clean")
	require.ErrorIs(t, err, graveler.ErrNotFound)
	labels := map[string]string{repos.KeyKind: repos.KindDataset, repos.KeyProducer: "yolo"}
	r, err := c.CreateRepositoryWithMetadata(ctx, "dataset-clean", "", "mem://dataset-clean", "main", false, labels)
	require.NoError(t, err)
	require.Equal(t, "main", r.DefaultBranch)
	md, err := c.GetRepositoryMetadata(ctx, "dataset-clean")
	require.NoError(t, err)
	require.Equal(t, graveler.RepositoryMetadata(labels), md)
	// It is an ordinary repository otherwise: labels update, a second creation is refused.
	require.NoError(t, c.UpdateRepositoryMetadata(ctx, "dataset-clean", graveler.RepositoryMetadata{repos.KeyLocation: "here"}))
	md, err = c.GetRepositoryMetadata(ctx, "dataset-clean")
	require.NoError(t, err)
	require.Equal(t, "yolo", md[repos.KeyProducer])
	require.Equal(t, "here", md[repos.KeyLocation])
	_, err = c.CreateRepositoryWithMetadata(ctx, "dataset-clean", "", "mem://dataset-clean-2", "main", false, labels)
	require.ErrorIs(t, err, graveler.ErrNotUnique)
	_, err = c.GetBranchReference(ctx, "dataset-clean", "main")
	require.NoError(t, err)
}

// cachedCatalog is realCatalog with the settings cache the server runs with, so the rules the
// write paths enforce lag the stored ones.
func cachedCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	ctx := context.WithoutCancel(t.Context())
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set(config.BlockstoreTypeKey, block.BlockstoreTypeMem)
	viper.Set("database.type", mem.DriverName)
	viper.Set("committed.local_cache.size_bytes", 24*1024*1024)
	viper.Set("committed.sstable.memory.cache_size_bytes", 2*1024*1024)
	cfg := &config.ConfigImpl{}
	_, err := config.NewConfig("", cfg)
	require.NoError(t, err)
	cfg.Committed.LocalCache.Dir = path.Join(t.TempDir(), "cache")
	c, err := catalog.New(ctx, catalog.Config{Config: cfg, KVStore: kvtest.GetStore(ctx, t), PathProvider: upload.DefaultPathProvider})
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// PRD novelcore/kaos#1911 CON-03: the stored rules say protected a few seconds before the write
// paths enforce it (they read the rules through a cache). A dataset is ready only once a direct
// write to main would really be refused.
func TestProducedBy_WaitsForEnforcement(t *testing.T) {
	ctx := t.Context()
	c := cachedCatalog(t)
	_, err := repos.Reconcile(ctx, c, "", "mem://lake", "", repos.Desired("", nil, map[string]string{"dataset-clean": "yolo"}))
	require.NoError(t, err)
	// A write before protection: the write paths now hold "no rule" in their cache.
	require.NoError(t, addFile(t, c, "dataset-clean", "main", "early.txt"))
	require.NoError(t, c.ResetBranch(ctx, "dataset-clean", "main"))

	_, err = repos.ReconcileProtection(ctx, c, c)
	require.NoError(t, err)
	rules, _, err := c.GetBranchProtectionRules(ctx, "dataset-clean")
	require.NoError(t, err)
	require.NotEmpty(t, rules.BranchPatternToBlockedActions["main"].GetValue(), "the stored rules already protect main")

	ready, err := repos.ProducedBy(ctx, c, "dataset-clean", "yolo")
	require.NoError(t, err)
	if !ready {
		// Not ready exactly while the core would still take a direct write.
		require.NoError(t, addFile(t, c, "dataset-clean", "main", "window.txt"), "enforcement has not caught up: not ready is right")
		require.NoError(t, c.ResetBranch(ctx, "dataset-clean", "main"))
	}
	require.False(t, ready, "the stored rules alone do not make it ready")
	require.Eventually(t, func() bool {
		ok, err := repos.ProducedBy(ctx, c, "dataset-clean", "yolo")
		require.NoError(t, err)
		return ok
	}, 30*time.Second, 100*time.Millisecond)
	require.ErrorIs(t, addFile(t, c, "dataset-clean", "main", "late.txt"), graveler.ErrWriteToProtectedBranch, "ready means enforced")
}
