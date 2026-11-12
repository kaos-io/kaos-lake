// KAOS (kaos-lake, PRD novelcore/kaos#1911 CON-03): new file, Apache-2.0.

package repos_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/treeverse/lakefs/pkg/catalog"
	"github.com/treeverse/lakefs/pkg/graveler"
	"github.com/treeverse/lakefs/pkg/kaos/repos"
)

const ttl = 24 * time.Hour

var produced = map[string]string{repos.KeyKind: repos.KindDataset, repos.KeyProducer: "yolo"}

// ago is the commit date of a time d before now.
func ago(d time.Duration) *int64 { t := time.Now().Add(-d).Unix(); return &t }

// producedRepo is a produced dataset whose main holds one commit of its own, dated mainAge ago.
func producedRepo(t *testing.T, c *catalog.Catalog, name string, labels map[string]string, mainAge time.Duration) {
	t.Helper()
	declareEmpty(t, c, name, labels)
	require.NoError(t, addFile(t, c, name, "main", "base.txt"))
	_, err := c.Commit(t.Context(), name, "main", "base", "someone", nil, ago(mainAge), nil, false)
	require.NoError(t, err)
}

// runBranch creates branch from main with one file staged, dated fileAge ago, and, when commitAge
// is not nil, committed with that date.
func runBranch(t *testing.T, c *catalog.Catalog, repo, branch string, fileAge time.Duration, commitAge *time.Duration) {
	t.Helper()
	_, err := c.CreateBranch(t.Context(), repo, branch, "main")
	require.NoError(t, err)
	require.NoError(t, c.CreateEntry(t.Context(), repo, branch, catalog.DBEntry{
		Path: branch + ".txt", PhysicalAddress: "mem://x/" + branch, CreationDate: time.Now().Add(-fileAge), Size: 1, Checksum: branch}))
	if commitAge != nil {
		_, err = c.Commit(t.Context(), repo, branch, "run", "yolo", nil, ago(*commitAge), nil, false)
		require.NoError(t, err)
	}
}

func dur(d time.Duration) *time.Duration { return &d }

func TestRunBranches_DeletesOnlyOldRunBranchesOfProducedDatasets(t *testing.T) {
	c := realCatalog(t)
	producedRepo(t, c, "dataset-clean", produced, 72*time.Hour)
	runBranch(t, c, "dataset-clean", "run-old-committed", 48*time.Hour, dur(48*time.Hour))
	runBranch(t, c, "dataset-clean", "run-old-staged", 48*time.Hour, nil)
	runBranch(t, c, "dataset-clean", "run-fresh-committed", 48*time.Hour, dur(time.Hour)) // committed an hour ago
	runBranch(t, c, "dataset-clean", "run-fresh-staged", time.Hour, nil)
	runBranch(t, c, "dataset-clean", "draft-old", 48*time.Hour, dur(48*time.Hour)) // not run-*
	// New, from an old main, nothing written: no age, kept.
	_, err := c.CreateBranch(t.Context(), "dataset-clean", "run-empty", "main")
	require.NoError(t, err)

	// Not produced datasets and outputs repositories are not touched, whatever they hold.
	producedRepo(t, c, "dataset-hand", map[string]string{repos.KeyKind: repos.KindDataset}, 72*time.Hour)
	runBranch(t, c, "dataset-hand", "run-old", 48*time.Hour, dur(48*time.Hour))

	changes, err := repos.ReconcileRunBranches(t.Context(), c, c, ttl, time.Now())
	require.NoError(t, err)
	require.Len(t, changes, 2)
	require.ElementsMatch(t, []string{"main", "run-fresh-committed", "run-fresh-staged", "draft-old", "run-empty"}, branchNames(t, c, "dataset-clean"))
	require.ElementsMatch(t, []string{"main", "run-old"}, branchNames(t, c, "dataset-hand"))
}

func TestRunBranches_BranchOfAnOldMainWithFreshWritesIsKept(t *testing.T) {
	c := realCatalog(t)
	producedRepo(t, c, "dataset-clean", produced, 72*time.Hour)
	runBranch(t, c, "dataset-clean", "run-now", 0, nil) // head is main's old commit, objects are fresh
	_, err := repos.ReconcileRunBranches(t.Context(), c, c, ttl, time.Now())
	require.NoError(t, err)
	require.Contains(t, branchNames(t, c, "dataset-clean"), "run-now")
}

func TestRunBranches_RefusesAnAgeUnderAnHour(t *testing.T) {
	c := realCatalog(t)
	producedRepo(t, c, "dataset-clean", produced, 72*time.Hour)
	runBranch(t, c, "dataset-clean", "run-old", 48*time.Hour, dur(48*time.Hour))
	_, err := repos.ReconcileRunBranches(t.Context(), c, c, 59*time.Minute, time.Now())
	require.Error(t, err)
	require.Contains(t, branchNames(t, c, "dataset-clean"), "run-old")
}

// flaky fails the deletes of the branches and tags named in fail.
type flaky struct {
	*catalog.Catalog
	fail map[string]bool
}

func (f *flaky) DeleteBranch(ctx context.Context, repo, branch string, opts ...graveler.SetOptionsFunc) error {
	if f.fail[repo+"/"+branch] {
		return errors.New("store unavailable")
	}
	return f.Catalog.DeleteBranch(ctx, repo, branch, opts...)
}

func (f *flaky) DeleteTag(ctx context.Context, repo, tag string, opts ...graveler.SetOptionsFunc) error {
	if f.fail[repo+"/"+tag] {
		return errors.New("store unavailable")
	}
	return f.Catalog.DeleteTag(ctx, repo, tag, opts...)
}

func TestRunBranches_AFailedDeleteDoesNotStopThePass(t *testing.T) {
	f := &flaky{Catalog: realCatalog(t), fail: map[string]bool{"dataset-a/run-1": true}}
	for _, r := range []string{"dataset-a", "dataset-b"} {
		producedRepo(t, f.Catalog, r, produced, 72*time.Hour)
		runBranch(t, f.Catalog, r, "run-1", 48*time.Hour, nil)
		runBranch(t, f.Catalog, r, "run-2", 48*time.Hour, nil)
	}
	changes, err := repos.ReconcileRunBranches(t.Context(), f, f, ttl, time.Now())
	require.Error(t, err)
	require.Contains(t, err.Error(), "dataset-a")
	require.Len(t, changes, 3)
	require.ElementsMatch(t, []string{"main", "run-1"}, branchNames(t, f.Catalog, "dataset-a"))
	require.Equal(t, []string{"main"}, branchNames(t, f.Catalog, "dataset-b"))
}

// keptTag commits a file to main of repo through a run branch, dated age ago, tags the commit
// name and returns the commit. promote merges the run into main (squash: by content only).
func keptTag(t *testing.T, c *catalog.Catalog, repo, name string, age time.Duration, promote, squash bool) string {
	t.Helper()
	branch := "run-" + name
	_, err := c.CreateBranch(t.Context(), repo, branch, "main")
	require.NoError(t, err)
	require.NoError(t, addFile(t, c, repo, branch, name+".txt"))
	cm, err := c.Commit(t.Context(), repo, branch, "run "+name, "yolo", nil, ago(age), nil, false)
	require.NoError(t, err)
	_, err = c.CreateTag(t.Context(), repo, name, cm.Reference)
	require.NoError(t, err)
	if promote {
		var opts []graveler.SetOptionsFunc
		if squash {
			opts = append(opts, graveler.WithSquashMerge(true))
		}
		_, err = c.Merge(t.Context(), repo, "main", branch, "yolo", "promote", nil, "", opts...)
		require.NoError(t, err)
	}
	require.NoError(t, c.DeleteBranch(t.Context(), repo, branch))
	return cm.Reference
}

func tagNames(t *testing.T, c *catalog.Catalog, repo string) []string {
	t.Helper()
	tags, _, err := c.ListTags(t.Context(), repo, "", 100, "")
	require.NoError(t, err)
	var names []string
	for _, tg := range tags {
		names = append(names, tg.ID)
	}
	return names
}

func TestKeptVersions(t *testing.T) {
	c := realCatalog(t)
	producedRepo(t, c, "dataset-clean", produced, 90*24*time.Hour)
	old := 40 * 24 * time.Hour
	keptTag(t, c, "dataset-clean", "k-r1-train-aaaaaaaaaaaa", old, false, false)          // old, unpromoted: expires
	keptTag(t, c, "dataset-clean", "k-r2-train-bbbbbbbbbbbb", 24*time.Hour, false, false) // fresh: kept
	keptTag(t, c, "dataset-clean", "k-r3-train-cccccccccccc", old, true, false)           // promoted by a merge: kept
	keptTag(t, c, "dataset-clean", "k-r4-train-dddddddddddd", old, true, true)            // promoted by a squash merge: kept
	named := keptTag(t, c, "dataset-clean", "k-r5-train-eeeeeeeeeeee", old, false, false)
	_, err := c.CreateTag(t.Context(), "dataset-clean", "model-best", named) // another tag: kept
	require.NoError(t, err)
	keptTag(t, c, "dataset-clean", "other-old", old, false, false) // not k-*: never touched
	// Not produced: untouched.
	producedRepo(t, c, "dataset-hand", map[string]string{repos.KeyKind: repos.KindDataset}, 90*24*time.Hour)
	keptTag(t, c, "dataset-hand", "k-r1-train-aaaaaaaaaaaa", old, false, false)

	changes, err := repos.ReconcileKeptVersions(t.Context(), c, c, 30, time.Now())
	require.NoError(t, err)
	require.Len(t, changes, 1)
	require.ElementsMatch(t, []string{"k-r2-train-bbbbbbbbbbbb", "k-r3-train-cccccccccccc", "k-r4-train-dddddddddddd",
		"k-r5-train-eeeeeeeeeeee", "model-best", "other-old"}, tagNames(t, c, "dataset-clean"))
	require.Equal(t, []string{"k-r1-train-aaaaaaaaaaaa"}, tagNames(t, c, "dataset-hand"))

	// A later pass changes nothing; a longer keep keeps what a shorter one would delete.
	changes, err = repos.ReconcileKeptVersions(t.Context(), c, c, 30, time.Now())
	require.NoError(t, err)
	require.Empty(t, changes)
}

func TestKeptVersions_ZeroNeverExpires(t *testing.T) {
	c := realCatalog(t)
	producedRepo(t, c, "dataset-clean", produced, 900*24*time.Hour)
	keptTag(t, c, "dataset-clean", "k-r1-train-aaaaaaaaaaaa", 800*24*time.Hour, false, false)
	changes, err := repos.ReconcileKeptVersions(t.Context(), c, c, 0, time.Now())
	require.NoError(t, err)
	require.Empty(t, changes)
	require.Equal(t, []string{"k-r1-train-aaaaaaaaaaaa"}, tagNames(t, c, "dataset-clean"))
	_, err = repos.ReconcileKeptVersions(t.Context(), c, c, -1, time.Now())
	require.Error(t, err)
}

func TestKeptVersions_AFailureDoesNotStopTheOthers(t *testing.T) {
	f := &flaky{Catalog: realCatalog(t), fail: map[string]bool{"dataset-a/k-r1-train-aaaaaaaaaaaa": true}}
	old := 40 * 24 * time.Hour
	for _, r := range []string{"dataset-a", "dataset-b"} {
		producedRepo(t, f.Catalog, r, produced, 90*24*time.Hour)
		keptTag(t, f.Catalog, r, "k-r1-train-aaaaaaaaaaaa", old, false, false)
		keptTag(t, f.Catalog, r, "k-r2-train-bbbbbbbbbbbb", old, false, false)
	}
	changes, err := repos.ReconcileKeptVersions(t.Context(), f, f, 30, time.Now())
	require.Error(t, err)
	require.Contains(t, err.Error(), "dataset-a")
	require.Len(t, changes, 3)
	require.Equal(t, []string{"k-r1-train-aaaaaaaaaaaa"}, tagNames(t, f.Catalog, "dataset-a"))
	require.Empty(t, tagNames(t, f.Catalog, "dataset-b"))
}
