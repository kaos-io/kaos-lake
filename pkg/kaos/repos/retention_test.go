// KAOS (kaos-lake, PRD novelcore/kaos#1911 CON-03): new file, Apache-2.0.

package repos_test

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
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

func TestRunBranches_RefusesAnAgeUnderADay(t *testing.T) {
	c := realCatalog(t)
	producedRepo(t, c, "dataset-clean", produced, 72*time.Hour)
	runBranch(t, c, "dataset-clean", "run-old", 48*time.Hour, dur(48*time.Hour))
	_, err := repos.ReconcileRunBranches(t.Context(), c, c, 23*time.Hour, time.Now())
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

// A run branch is alive while its upload goes on: an object staged at hour 0 and another at
// TTL-1h keep it, however old the first is; only a branch nothing was staged on for TTL goes.
func TestRunBranches_ALiveRunKeepsItsBranch(t *testing.T) {
	c := realCatalog(t)
	producedRepo(t, c, "dataset-clean", produced, 72*time.Hour)
	base := time.Now().Add(-10 * 24 * time.Hour)
	_, err := c.CreateBranch(t.Context(), "dataset-clean", "run-live", "main")
	require.NoError(t, err)
	stage := func(name string, at time.Time) {
		require.NoError(t, c.CreateEntry(t.Context(), "dataset-clean", "run-live", catalog.DBEntry{
			Path: name, PhysicalAddress: "mem://x/" + name, CreationDate: at, Size: 1, Checksum: name}))
	}
	stage("first", base)                     // hour 0
	stage("second", base.Add(ttl-time.Hour)) // TTL-1h

	// The first object is older than the TTL; the newest is not: kept.
	changes, err := repos.ReconcileRunBranches(t.Context(), c, c, ttl, base.Add(ttl+30*time.Minute))
	require.NoError(t, err)
	require.Empty(t, changes)
	require.Contains(t, branchNames(t, c, "dataset-clean"), "run-live")

	// Nothing staged for longer than the TTL: deleted, and the log names the commit it was at.
	changes, err = repos.ReconcileRunBranches(t.Context(), c, c, ttl, base.Add(2*ttl))
	require.NoError(t, err)
	require.Len(t, changes, 1)
	head, err := c.GetCommit(t.Context(), "dataset-clean", "main")
	require.NoError(t, err)
	require.Contains(t, changes[0], "run-live")
	require.Contains(t, changes[0], "dataset-clean")
	require.Contains(t, changes[0], head.Reference)
	require.Contains(t, changes[0], "run_branch_ttl")
	require.NotContains(t, branchNames(t, c, "dataset-clean"), "run-live")
}

// A client that writes or commits on a branch the platform just deleted gets the branch's absence.
func TestRunBranches_AWriteOnADeletedBranchIsRefusedAsNotFound(t *testing.T) {
	c := realCatalog(t)
	producedRepo(t, c, "dataset-clean", produced, 72*time.Hour)
	runBranch(t, c, "dataset-clean", "run-old", 48*time.Hour, nil)
	_, err := repos.ReconcileRunBranches(t.Context(), c, c, ttl, time.Now())
	require.NoError(t, err)
	err = addFile(t, c, "dataset-clean", "run-old", "late.txt")
	require.ErrorIs(t, err, graveler.ErrNotFound)
	_, err = c.Commit(t.Context(), "dataset-clean", "run-old", "late", "yolo", nil, nil, nil, true)
	require.ErrorIs(t, err, graveler.ErrNotFound)
}

func TestRunBranches_AtMostABoundPerPass(t *testing.T) {
	c := realCatalog(t)
	producedRepo(t, c, "dataset-clean", produced, 72*time.Hour)
	for i := range 230 {
		runBranch(t, c, "dataset-clean", fmt.Sprintf("run-%03d", i), 48*time.Hour, nil)
	}
	changes, err := repos.ReconcileRunBranches(t.Context(), c, c, ttl, time.Now())
	require.NoError(t, err)
	require.Len(t, changes, 200)
	changes, err = repos.ReconcileRunBranches(t.Context(), c, c, ttl, time.Now())
	require.NoError(t, err)
	require.Len(t, changes, 30)
}

func stamp(t time.Time) string { return t.UTC().Format("20060102T150405Z") }

// tagAt tags a new commit of repo, dated commitAge ago, name; returns the commit.
func tagAt(t *testing.T, c *catalog.Catalog, repo, name string, commitAge time.Duration) string {
	t.Helper()
	return keptTag(t, c, repo, name, commitAge, false, false)
}

func TestKeptVersions_AgeIsTheTimeInTheName(t *testing.T) {
	c := realCatalog(t)
	producedRepo(t, c, "dataset-clean", produced, 90*24*time.Hour)
	old, fresh := 40*24*time.Hour, 24*time.Hour
	now := time.Now()
	tagAt(t, c, "dataset-clean", "k-"+stamp(now.Add(-time.Hour))+"-r1-train-aaaaaaaaaaaa", old) // fresh name, old commit: kept
	tagAt(t, c, "dataset-clean", "k-"+stamp(now.Add(-old))+"-r2-train-bbbbbbbbbbbb", fresh)     // old name, fresh commit: expires
	tagAt(t, c, "dataset-clean", "k-r3-train-cccccccccccc", old)                                // old form, old commit: expires
	tagAt(t, c, "dataset-clean", "k-r4-train-dddddddddddd", fresh)                              // old form, fresh commit: kept
	tagAt(t, c, "dataset-clean", "k-20269999T999999Z-r5-train-eeeeeeeeeeee", old)               // malformed time, old commit: expires
	tagAt(t, c, "dataset-clean", "k-20269999T999999Z-r6-train-ffffffffffff", fresh)             // malformed time, fresh commit: kept

	changes, err := repos.ReconcileKeptVersions(t.Context(), c, c, 30, now)
	require.NoError(t, err)
	require.Len(t, changes, 3)
	require.ElementsMatch(t, []string{
		"k-" + stamp(now.Add(-time.Hour)) + "-r1-train-aaaaaaaaaaaa",
		"k-r4-train-dddddddddddd",
		"k-20269999T999999Z-r6-train-ffffffffffff",
	}, tagNames(t, c, "dataset-clean"))
	for _, ch := range changes { // the log names what was deleted, the commit and the rule
		require.Contains(t, ch, "dataset-clean")
		require.Contains(t, ch, "keep_days 30")
		require.Regexp(t, `commit [0-9a-f]{64}`, ch)
	}
}

// countingLog counts the commit reads.
type countingLog struct {
	*catalog.Catalog
	calls atomic.Int32
}

func (f *countingLog) ListCommits(ctx context.Context, repo, ref string, p catalog.LogParams) ([]*catalog.CommitLog, bool, error) {
	f.calls.Add(1)
	return f.Catalog.ListCommits(ctx, repo, ref, p)
}

func TestKeptVersions_NoCommitIsReadForATagThatIsNotOld(t *testing.T) {
	f := &countingLog{Catalog: realCatalog(t)}
	producedRepo(t, f.Catalog, "dataset-clean", produced, 90*24*time.Hour)
	now := time.Now()
	for i := range 20 {
		tagAt(t, f.Catalog, "dataset-clean", fmt.Sprintf("k-%s-r%d-train-aaaaaaaaaaaa", stamp(now.Add(-time.Hour)), i), 40*24*time.Hour)
	}
	f.calls.Store(0)
	changes, err := repos.ReconcileKeptVersions(t.Context(), f, f, 30, now)
	require.NoError(t, err)
	require.Empty(t, changes)
	require.Zero(t, f.calls.Load())
}

// Promotion: the client merges the sealed commit into main without squash (ancestry), a squash
// holds the run's content (content), and a squash into a main that moved holds the union of two
// runs, equal to neither: only the promotion's kaos.promoted metadata, the sealed commit's full
// id, names it.
func TestKeptVersions_Promoted(t *testing.T) {
	for _, tc := range []struct {
		name     string
		named    bool // the merges carry kaos.promoted
		wantKept bool
	}{
		{"main moved, no metadata: the second run is expired", false, false},
		{"main moved, kaos.promoted names the sealed commit", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := realCatalog(t)
			producedRepo(t, c, "dataset-clean", produced, 90*24*time.Hour)
			old := 40 * 24 * time.Hour
			// Two runs branch from the same main; both are squash-promoted, one after the other.
			sealed := map[string]string{}
			for _, n := range []string{"k-r1-train-aaaaaaaaaaaa", "k-r2-train-bbbbbbbbbbbb"} {
				_, err := c.CreateBranch(t.Context(), "dataset-clean", "run-"+n, "main")
				require.NoError(t, err)
				require.NoError(t, addFile(t, c, "dataset-clean", "run-"+n, n+".txt"))
				cm, err := c.Commit(t.Context(), "dataset-clean", "run-"+n, "run "+n, "yolo", nil, ago(old), nil, false)
				require.NoError(t, err)
				_, err = c.CreateTag(t.Context(), "dataset-clean", n, cm.Reference)
				require.NoError(t, err)
				sealed[n] = cm.Reference
			}
			for _, n := range []string{"k-r1-train-aaaaaaaaaaaa", "k-r2-train-bbbbbbbbbbbb"} {
				var md catalog.Metadata
				if tc.named {
					md = catalog.Metadata{repos.KeyPromoted: sealed[n]}
				}
				_, err := c.Merge(t.Context(), "dataset-clean", "main", sealed[n], "yolo", "promote", md, "", graveler.WithSquashMerge(true))
				require.NoError(t, err)
			}
			_, err := repos.ReconcileKeptVersions(t.Context(), c, c, 30, time.Now())
			require.NoError(t, err)
			got := tagNames(t, c, "dataset-clean")
			require.Contains(t, got, "k-r1-train-aaaaaaaaaaaa") // content equals main after the first merge
			if tc.wantKept {
				require.Contains(t, got, "k-r2-train-bbbbbbbbbbbb")
			} else {
				require.NotContains(t, got, "k-r2-train-bbbbbbbbbbbb")
			}
		})
	}
}

func TestKeptVersions_HistoryBeyondAPageAndTagPaging(t *testing.T) {
	restore := repos.SetHistoryPage(1000)
	defer restore()
	f := &countingLog{Catalog: realCatalog(t)}
	c := f.Catalog
	producedRepo(t, c, "dataset-clean", produced, 90*24*time.Hour)
	var first string
	for i := range 2500 {
		cm, err := c.Commit(t.Context(), "dataset-clean", "main", fmt.Sprintf("c%d", i), "someone", nil, nil, nil, true)
		require.NoError(t, err)
		if i == 0 {
			first = cm.Reference // the oldest commit of main's history, past two pages
		}
	}
	oldName := func(i int) string { return fmt.Sprintf("k-20200101T000000Z-r%04d-train-aaaaaaaaaaaa", i) }
	// One unpromoted commit carries 1099 old tags, over two pages of tags; the oldest main commit one.
	branch, err := c.CreateBranch(t.Context(), "dataset-clean", "run-x", "main")
	require.NoError(t, err)
	require.NoError(t, addFile(t, c, "dataset-clean", "run-x", "x.txt"))
	lone, err := c.Commit(t.Context(), "dataset-clean", "run-x", "x", "yolo", nil, nil, nil, false)
	require.NoError(t, err)
	_ = branch
	for i := 1; i < 1100; i++ {
		_, err := c.CreateTag(t.Context(), "dataset-clean", oldName(i), lone.Reference)
		require.NoError(t, err)
	}
	_, err = c.CreateTag(t.Context(), "dataset-clean", oldName(0), first)
	require.NoError(t, err)

	f.calls.Store(0)
	changes, err := repos.ReconcileKeptVersions(t.Context(), f, f, 30, time.Now())
	require.NoError(t, err)
	require.Len(t, changes, 200, "at most 200 tags go from one repository in one pass")
	// main's 2503 commits: three pages, each started at the last commit read; the content of each
	// unpromoted commit is read once.
	require.LessOrEqual(t, int(f.calls.Load()), 3+1)

	for range 5 {
		_, err = repos.ReconcileKeptVersions(t.Context(), f, f, 30, time.Now())
		require.NoError(t, err)
	}
	require.Equal(t, []string{oldName(0)}, tagNames(t, c, "dataset-clean"), "only the tag on main's oldest commit stays")

	// With the default page main's 2500 commits are one call, however many there are: the history
	// is read once, not once per page with each page skipping the ones before it.
	restore()
	_, err = c.CreateTag(t.Context(), "dataset-clean", oldName(9999), lone.Reference)
	require.NoError(t, err)
	f.calls.Store(0)
	changes, err = repos.ReconcileKeptVersions(t.Context(), f, f, 30, time.Now())
	require.NoError(t, err)
	require.Len(t, changes, 1)
	require.LessOrEqual(t, int(f.calls.Load()), 2, "one call for main's history, one for the candidate's commit")
}
