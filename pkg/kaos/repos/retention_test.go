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

// flaky fails the deletes of the branches named in fail.
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
