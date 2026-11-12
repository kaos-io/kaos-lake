// KAOS (kaos-lake, PRD novelcore/kaos#1911): new file, Apache-2.0.

package repos_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/treeverse/lakefs/pkg/block"
	"github.com/treeverse/lakefs/pkg/catalog"
	"github.com/treeverse/lakefs/pkg/graveler"
	"github.com/treeverse/lakefs/pkg/kaos/repos"
)

// The sample is proven against the real catalog core over in-memory storage, not a fake: the
// merge, the branch protection and the expected head are the real ones.

const def = "dataset-default"

var sampleFiles = []string{"README.txt", "sample/measurements.csv", "sample/notes-1.txt", "sample/notes-2.txt"}

func reconcileDefault(t *testing.T, c repos.Catalog) error {
	t.Helper()
	_, err := repos.Reconcile(t.Context(), c, "", "mem://lake", "", repos.Desired(def, nil, nil))
	return err
}

// declareEmpty creates the default dataset as Reconcile does, without seeding it.
func declareEmpty(t *testing.T, c *catalog.Catalog, name string, labels map[string]string) {
	t.Helper()
	_, err := c.CreateRepositoryWithMetadata(t.Context(), name, "", "mem://lake/"+name, "main", false, labels)
	require.NoError(t, err)
}

var defaultLabels = map[string]string{repos.KeyKind: repos.KindDataset, repos.KeyDefault: "true"}

func mainPaths(t *testing.T, c *catalog.Catalog, name string) []string {
	t.Helper()
	entries, _, err := c.ListEntries(t.Context(), name, "main", "", "", "", 100)
	require.NoError(t, err)
	var paths []string
	for _, e := range entries {
		paths = append(paths, e.Path)
	}
	return paths
}

func branchNames(t *testing.T, c *catalog.Catalog, name string) []string {
	t.Helper()
	bs, _, err := c.ListBranches(t.Context(), name, "", 100, "")
	require.NoError(t, err)
	var names []string
	for _, b := range bs {
		names = append(names, b.Name)
	}
	return names
}

// seedCommits counts the sample commits reachable from main.
func seedCommits(t *testing.T, c *catalog.Catalog, name string) int {
	t.Helper()
	log, more, err := c.ListCommits(t.Context(), name, "main", catalog.LogParams{Amount: 100, Limit: true})
	require.NoError(t, err)
	require.False(t, more)
	n := 0
	for _, cm := range log {
		if cm.Metadata[repos.KeySeed] == repos.SeedSample {
			n++
		}
	}
	return n
}

func read(t *testing.T, c *catalog.Catalog, name, path string) string {
	t.Helper()
	e, err := c.GetEntry(t.Context(), name, "main", path, catalog.GetEntryParams{})
	require.NoError(t, err)
	r, err := c.BlockAdapter.Get(t.Context(), block.ObjectPointer{
		StorageNamespace: "mem://lake/" + name, IdentifierType: e.AddressType.ToIdentifierType(), Identifier: e.PhysicalAddress})
	require.NoError(t, err)
	defer r.Close()
	b, err := io.ReadAll(r)
	require.NoError(t, err)
	return string(b)
}

func requireSeeded(t *testing.T, c *catalog.Catalog, name string) {
	t.Helper()
	require.ElementsMatch(t, sampleFiles, mainPaths(t, c, name))
	require.Equal(t, []string{"main"}, branchNames(t, c, name), "the temporary branch is gone")
	require.Equal(t, 1, seedCommits(t, c, name))
}

func requireUntouched(t *testing.T, c *catalog.Catalog, name string, paths []string) {
	t.Helper()
	require.ElementsMatch(t, paths, mainPaths(t, c, name))
	require.Zero(t, seedCommits(t, c, name))
	require.NotContains(t, branchNames(t, c, name), "kaos-seed-sample")
}

func TestSeed_NewDefaultGetsTheSampleOnce(t *testing.T) {
	c := realCatalog(t)
	require.NoError(t, reconcileDefault(t, c))
	requireSeeded(t, c, def)

	head, err := c.GetCommit(t.Context(), def, "main")
	require.NoError(t, err)
	require.Equal(t, "kaos: sample data so a new project's default dataset runs as it is", head.Message)
	require.Equal(t, "kaos-lake", head.Committer)
	require.Equal(t, "sample-v1", head.Metadata["kaos.seed"])

	readme := read(t, c, def, "README.txt")
	require.Contains(t, readme, "sample data from the platform")
	require.Contains(t, readme, "kaosctl dataset push <folder> dataset-default")
	csv := read(t, c, def, "sample/measurements.csv")
	lines := strings.Split(strings.TrimSpace(csv), "\n")
	require.Equal(t, "sample_id,temperature_c,humidity_pct,pressure_hpa,label", lines[0])
	require.Len(t, lines, 51)
	for _, n := range []string{"sample/notes-1.txt", "sample/notes-2.txt"} {
		require.GreaterOrEqual(t, len(read(t, c, def, n)), 200)
	}

	// Every file has content, and the whole sample is small.
	entries, _, err := c.ListEntries(t.Context(), def, "main", "", "", "", 100)
	require.NoError(t, err)
	total := int64(0)
	for _, e := range entries {
		require.Positive(t, e.Size, e.Path)
		total += e.Size
	}
	require.Less(t, total, int64(100*1024))

	// A second pass writes nothing.
	changes, err := repos.Reconcile(t.Context(), c, "", "mem://lake", "", repos.Desired(def, nil, nil))
	require.NoError(t, err)
	require.Empty(t, changes)
	requireSeeded(t, c, def)
}

func TestSeed_EmptyDefaultThatExistsGetsTheSample(t *testing.T) {
	c := realCatalog(t)
	declareEmpty(t, c, def, defaultLabels)
	require.Empty(t, mainPaths(t, c, def))
	require.NoError(t, reconcileDefault(t, c))
	requireSeeded(t, c, def)
}

func TestSeed_ProtectionOnStillSeeds(t *testing.T) {
	c := realCatalog(t)
	declareEmpty(t, c, def, defaultLabels)
	_, err := repos.ReconcileProtection(t.Context(), c, c)
	require.NoError(t, err)
	require.Error(t, addFile(t, c, def, "main", "direct.txt"), "main is protected")
	require.NoError(t, reconcileDefault(t, c))
	requireSeeded(t, c, def)
}

func TestSeed_UntouchedWhenMainIsNotTheInitialCommit(t *testing.T) {
	c := realCatalog(t)
	declareEmpty(t, c, def, defaultLabels)
	require.NoError(t, addFile(t, c, def, "main", "mine.txt"))
	_, err := c.Commit(t.Context(), def, "main", "mine", "someone", nil, nil, nil, false)
	require.NoError(t, err)
	require.NoError(t, reconcileDefault(t, c))
	requireUntouched(t, c, def, []string{"mine.txt"})

	// An empty second commit counts too.
	c2 := realCatalog(t)
	declareEmpty(t, c2, def, defaultLabels)
	_, err = c2.Commit(t.Context(), def, "main", "empty", "someone", nil, nil, nil, true)
	require.NoError(t, err)
	require.NoError(t, reconcileDefault(t, c2))
	requireUntouched(t, c2, def, nil)
}

func TestSeed_UntouchedWhenMainHoldsAnUncommittedObject(t *testing.T) {
	c := realCatalog(t)
	declareEmpty(t, c, def, defaultLabels)
	require.NoError(t, addFile(t, c, def, "main", "staged.txt"))
	require.NoError(t, reconcileDefault(t, c))
	requireUntouched(t, c, def, []string{"staged.txt"})
	head, err := c.GetCommit(t.Context(), def, "main")
	require.NoError(t, err)
	require.Empty(t, head.Parents)
}

func TestSeed_UntouchedWhenAnotherBranchExists(t *testing.T) {
	c := realCatalog(t)
	declareEmpty(t, c, def, defaultLabels)
	_, err := c.CreateBranch(t.Context(), def, "draft", "main")
	require.NoError(t, err)
	require.NoError(t, reconcileDefault(t, c))
	requireUntouched(t, c, def, nil)
	require.ElementsMatch(t, []string{"main", "draft"}, branchNames(t, c, def))
}

func TestSeed_OnlyTheDefaultDataset(t *testing.T) {
	c := realCatalog(t)
	want := repos.Desired(def, map[string]string{"yolo": "outputs-yolo"}, map[string]string{"dataset-clean": "yolo"})
	want = append(want, repos.Declared{Name: "dataset-other", Kind: repos.KindDataset})
	_, err := repos.Reconcile(t.Context(), c, "", "mem://lake", "", want)
	require.NoError(t, err)
	requireSeeded(t, c, def)
	for _, name := range []string{"dataset-other", "dataset-clean", "outputs-yolo"} {
		requireUntouched(t, c, name, nil)
	}
	// A dataset made by hand and labelled a dataset, empty, is not the default either.
	declareEmpty(t, c, "dataset-hand", map[string]string{repos.KeyKind: repos.KindDataset})
	_, err = repos.Reconcile(t.Context(), c, "", "mem://lake", "", want)
	require.NoError(t, err)
	requireUntouched(t, c, "dataset-hand", nil)
}

func TestSeed_NeverAProducedDataset(t *testing.T) {
	c := realCatalog(t)
	// Declared as the default and as produced: created with the producer label, so never seeded.
	_, err := repos.Reconcile(t.Context(), c, "", "mem://lake", "",
		[]repos.Declared{{Name: def, Kind: repos.KindDataset, Default: true, Producer: "yolo"}})
	require.NoError(t, err)
	requireUntouched(t, c, def, nil)

	// An existing repository labelled as produced stays as it is, whatever is declared.
	declareEmpty(t, c, "dataset-made", map[string]string{repos.KeyKind: repos.KindDataset, repos.KeyProducer: "hera"})
	_, err = repos.Reconcile(t.Context(), c, "", "mem://lake", "",
		[]repos.Declared{{Name: "dataset-made", Kind: repos.KindDataset, Default: true}})
	require.NoError(t, err)
	requireUntouched(t, c, "dataset-made", nil)
}

func TestSeed_CrashedAttemptIsCleanedAndSeeded(t *testing.T) {
	c := realCatalog(t)
	// What a crashed attempt leaves: some of the sample's own files, staged, then committed.
	good := realCatalog(t)
	require.NoError(t, reconcileDefault(t, good))
	declareEmpty(t, c, def, defaultLabels)
	_, err := c.CreateBranch(t.Context(), def, "kaos-seed-sample", "main")
	require.NoError(t, err)
	for _, p := range []string{"README.txt", "sample/notes-1.txt"} {
		require.NoError(t, c.PutObject(t.Context(), def, "kaos-seed-sample", p, []byte(read(t, good, def, p)), "text/plain"))
	}
	require.NoError(t, reconcileDefault(t, c))
	requireSeeded(t, c, def)

	// Committed once on the branch before the crash: the same.
	c2 := realCatalog(t)
	declareEmpty(t, c2, def, defaultLabels)
	_, err = c2.CreateBranch(t.Context(), def, "kaos-seed-sample", "main")
	require.NoError(t, err)
	require.NoError(t, c2.PutObject(t.Context(), def, "kaos-seed-sample", "README.txt", []byte(read(t, good, def, "README.txt")), "text/plain"))
	_, err = c2.Commit(t.Context(), def, "kaos-seed-sample", "half", "kaos-lake", nil, nil, nil, false)
	require.NoError(t, err)
	require.NoError(t, reconcileDefault(t, c2))
	requireSeeded(t, c2, def)
}

// A person may create a branch named like the seed's. It is removed only when it is provably the
// seed's own; anything else on it, and the default dataset is left alone, with its objects.
// mineCount makes a dataset name of its own for each case, also when the tests run twice in a process.
var mineCount atomic.Int32

func TestSeed_APersonsBranchOfTheSeedsNameIsNeverDeleted(t *testing.T) {
	for name, stage := range map[string]func(t *testing.T, c *catalog.Catalog, mine string){
		"their own file": func(t *testing.T, c *catalog.Catalog, mine string) {
			require.NoError(t, addFile(t, c, mine, "kaos-seed-sample", "theirs.txt"))
		},
		"a sample file with other content": func(t *testing.T, c *catalog.Catalog, mine string) {
			require.NoError(t, c.PutObject(t.Context(), mine, "kaos-seed-sample", "README.txt", []byte("mine, not the sample"), "text/plain"))
		},
		"their file, committed": func(t *testing.T, c *catalog.Catalog, mine string) {
			require.NoError(t, addFile(t, c, mine, "kaos-seed-sample", "theirs.txt"))
			_, err := c.Commit(t.Context(), mine, "kaos-seed-sample", "theirs", "someone", nil, nil, nil, false)
			require.NoError(t, err)
		},
	} {
		mine := fmt.Sprintf("dataset-mine-%d", mineCount.Add(1)) // the refusal is logged once per dataset name per process
		t.Run(name, func(t *testing.T) {
			c := realCatalog(t)
			declareEmpty(t, c, mine, defaultLabels)
			_, err := c.CreateBranch(t.Context(), mine, "kaos-seed-sample", "main")
			require.NoError(t, err)
			stage(t, c, mine)
			want := repos.Desired(mine, nil, nil)
			changes, err := repos.Reconcile(t.Context(), c, "", "mem://lake", "", want)
			require.NoError(t, err)
			require.NotEmpty(t, changes, "the refusal is logged")
			require.Contains(t, changes[len(changes)-1], "kaos-seed-sample")
			require.ElementsMatch(t, []string{"main", "kaos-seed-sample"}, branchNames(t, c, mine))
			require.Empty(t, mainPaths(t, c, mine))
			diff, _, err := c.ListEntries(t.Context(), mine, "kaos-seed-sample", "", "", "", 10)
			require.NoError(t, err)
			require.NotEmpty(t, diff, "their objects are still there")
			require.Zero(t, seedCommits(t, c, mine))
			// Logged once, not at every pass.
			changes, err = repos.Reconcile(t.Context(), c, "", "mem://lake", "", want)
			require.NoError(t, err)
			require.Empty(t, changes)
		})
	}
}

// A default dataset whose branch is not main is not the seed's to fill.
func TestSeed_NoMainNoSample(t *testing.T) {
	c := realCatalog(t)
	_, err := c.CreateRepositoryWithMetadata(t.Context(), def, "", "mem://lake/"+def, "trunk", false, defaultLabels)
	require.NoError(t, err)
	require.NoError(t, reconcileDefault(t, c))
	require.Equal(t, []string{"trunk"}, branchNames(t, c, def))
	entries, _, err := c.ListEntries(t.Context(), def, "trunk", "", "", "", 10)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestSeed_ContentTypesAreSet(t *testing.T) {
	c := realCatalog(t)
	require.NoError(t, reconcileDefault(t, c))
	for p, want := range map[string]string{
		"README.txt": "text/plain; charset=utf-8", "sample/notes-1.txt": "text/plain; charset=utf-8", "sample/measurements.csv": "text/csv"} {
		e, err := c.GetEntry(t.Context(), def, "main", p, catalog.GetEntryParams{})
		require.NoError(t, err)
		require.Equal(t, want, e.ContentType, p)
	}
}

// failing makes the catalog fail one way, for as long as on is set.
type failing struct {
	*catalog.Catalog
	on        atomic.Bool
	putAfter  int // files written before PutObject fails
	puts      atomic.Int32
	failMerge bool
}

func (f *failing) PutObject(ctx context.Context, repo, branch, path string, content []byte, ct string) error {
	if f.on.Load() && int(f.puts.Add(1)) > f.putAfter && !f.failMerge {
		return errors.New("storage unavailable")
	}
	return f.Catalog.PutObject(ctx, repo, branch, path, content, ct)
}

func (f *failing) Merge(ctx context.Context, repo, dst, src, committer, msg string, md catalog.Metadata, strategy string, opts ...graveler.SetOptionsFunc) (string, error) {
	if f.on.Load() && f.failMerge {
		return "", errors.New("merge refused")
	}
	return f.Catalog.Merge(ctx, repo, dst, src, committer, msg, md, strategy, opts...)
}

func TestSeed_FailureIsReportedLeavesNothingAndIsRetried(t *testing.T) {
	for name, f := range map[string]*failing{
		"after two files written": {putAfter: 2},
		"merge refused":           {failMerge: true},
	} {
		t.Run(name, func(t *testing.T) {
			f.Catalog = realCatalog(t)
			f.on.Store(true)
			want := repos.Desired(def, map[string]string{"yolo": "outputs-yolo", "hera": "outputs-hera"}, nil)
			_, err := repos.Reconcile(t.Context(), f, "", "mem://lake", "", want)
			require.Error(t, err)
			require.Contains(t, err.Error(), "repository "+def)

			// The other declared repositories were reconciled.
			for _, n := range []string{"outputs-yolo", "outputs-hera"} {
				_, err := f.Catalog.GetRepository(t.Context(), n)
				require.NoError(t, err)
			}
			// Nothing is on main and the temporary branch is gone.
			requireUntouched(t, f.Catalog, def, nil)
			require.Equal(t, []string{"main"}, branchNames(t, f.Catalog, def))

			// The next pass retries and seeds.
			f.on.Store(false)
			_, err = repos.Reconcile(t.Context(), f, "", "mem://lake", "", want)
			require.NoError(t, err)
			requireSeeded(t, f.Catalog, def)
		})
	}
}

func TestSeed_PassesRacingLeaveOneSampleCommit(t *testing.T) {
	for range 5 {
		c := realCatalog(t)
		declareEmpty(t, c, def, defaultLabels)
		var wg sync.WaitGroup
		for range 4 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				// A pass that loses the race may report it; it must not change main.
				_ = reconcileDefault(t, c)
			}()
		}
		wg.Wait()
		// Whatever the losers did, a later pass converges and main holds one sample commit.
		require.NoError(t, reconcileDefault(t, c))
		require.ElementsMatch(t, sampleFiles, mainPaths(t, c, def))
		require.Equal(t, 1, seedCommits(t, c, def))
		require.Equal(t, []string{"main"}, branchNames(t, c, def))
	}
}

func TestSeed_FilesAreTheBriefsFour(t *testing.T) {
	c := realCatalog(t)
	require.NoError(t, reconcileDefault(t, c))
	require.True(t, slices.Equal(slices.Sorted(slices.Values(mainPaths(t, c, def))), slices.Sorted(slices.Values(sampleFiles))))
}
