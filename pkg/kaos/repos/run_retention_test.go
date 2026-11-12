// KAOS (kaos-lake, PRD novelcore/kaos#1911 CON-03): new file, Apache-2.0.

package repos_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/treeverse/lakefs/pkg/catalog"
	"github.com/treeverse/lakefs/pkg/graveler"
	"github.com/treeverse/lakefs/pkg/kaos/repos"
	"github.com/treeverse/lakefs/pkg/logging"
)

// brokenRetention is a catalog whose retention passes cannot read branches or tags, and counts the
// attempts.
type brokenRetention struct {
	*catalog.Catalog
	branchReads, tagReads atomic.Int32
}

func (b *brokenRetention) ListBranches(ctx context.Context, repo, prefix string, limit int, after string, opts ...graveler.ListOptionsFunc) ([]*catalog.Branch, bool, error) {
	if prefix == repos.RunBranchPrefix {
		b.branchReads.Add(1)
		return nil, false, errors.New("store unavailable")
	}
	return b.Catalog.ListBranches(ctx, repo, prefix, limit, after, opts...)
}

func (b *brokenRetention) ListTags(ctx context.Context, repo, prefix string, limit int, after string) ([]*catalog.Tag, bool, error) {
	b.tagReads.Add(1)
	return nil, false, errors.New("store unavailable")
}

func runUntil(t *testing.T, c repos.Catalog, ret repos.Retention, interval time.Duration, cond func() bool) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.WithoutCancel(t.Context()))
	done := make(chan struct{})
	go func() {
		defer close(done)
		repos.Run(ctx, c, nil, "", "mem://lake", "", def, func() map[string]string { return map[string]string{"yolo": "outputs-yolo"} },
			func() map[string]string { return map[string]string{"dataset-clean": "yolo"} }, interval, 0, ret, logging.ContextUnavailable())
	}()
	defer func() { cancel(); <-done }()
	require.Eventually(t, cond, 20*time.Second, 20*time.Millisecond)
}

// Run applies the retention it is given: an old run branch and an old unpromoted tag of a produced
// dataset go, while the default dataset is created, seeded and protected as ever.
func TestRun_AppliesRetention(t *testing.T) {
	c := realCatalog(t)
	producedRepo(t, c, "dataset-clean", produced, 72*time.Hour)
	runBranch(t, c, "dataset-clean", "run-old", 10*24*time.Hour, nil)
	tagAt(t, c, "dataset-clean", "k-20200101T000000Z-r1-train-aaaaaaaaaaaa", time.Hour)
	runUntil(t, c, repos.Retention{RunBranchTTL: 24 * time.Hour, KeepDays: 30, Interval: 30 * time.Millisecond}, 30*time.Millisecond, func() bool {
		names, _, err := c.ListBranches(t.Context(), "dataset-clean", "", 100, "")
		require.NoError(t, err)
		tags := tagNames(t, c, "dataset-clean")
		_, derr := c.GetRepository(t.Context(), def)
		return len(names) == 1 && len(tags) == 0 && derr == nil && len(mainPaths(t, c, def)) == len(sampleFiles)
	})
	rules, _, err := c.GetBranchProtectionRules(t.Context(), def)
	require.NoError(t, err)
	require.NotNil(t, rules)
}

// A retention pass that fails every time never blocks creation, seeding, protection or the
// discard of uncommitted objects on produced datasets.
func TestRun_AFailingRetentionPassBlocksNothing(t *testing.T) {
	b := &brokenRetention{Catalog: realCatalog(t)}
	producedRepo(t, b.Catalog, "dataset-clean", produced, 72*time.Hour)
	require.NoError(t, addFile(t, b.Catalog, "dataset-clean", "main", "stray.txt")) // uncommitted on a produced main
	runUntil(t, b, repos.Retention{RunBranchTTL: 24 * time.Hour, KeepDays: 30, Interval: 20 * time.Millisecond}, 20*time.Millisecond, func() bool {
		_, o := b.Catalog.GetRepository(t.Context(), "outputs-yolo")
		diff, _, err := b.Catalog.DiffUncommitted(t.Context(), "dataset-clean", "main", "", "", 10, "")
		rules, _, rerr := b.Catalog.GetBranchProtectionRules(t.Context(), def)
		return o == nil && err == nil && len(diff) == 0 && rerr == nil && rules != nil && len(mainPaths(t, b.Catalog, def)) == len(sampleFiles)
	})
	require.NotZero(t, b.branchReads.Load(), "the retention pass ran, and failed")
	require.NotZero(t, b.tagReads.Load())
}

// The retention passes run on their own cadence, not at every reconcile.
func TestRun_RetentionHasItsOwnCadence(t *testing.T) {
	b := &brokenRetention{Catalog: realCatalog(t)}
	producedRepo(t, b.Catalog, "dataset-clean", produced, 72*time.Hour)
	runUntil(t, b, repos.Retention{RunBranchTTL: 24 * time.Hour, Interval: 300 * time.Millisecond}, 10*time.Millisecond, func() bool {
		return b.branchReads.Load() >= 3
	})
	// 3 passes at 300ms took about 600ms; the reconcile ran about every 10ms, 60 times.
	require.Less(t, b.branchReads.Load(), int32(8))

	// Without an interval, or with nothing to apply, the passes do not run.
	b2 := &brokenRetention{Catalog: realCatalog(t)}
	producedRepo(t, b2.Catalog, "dataset-clean", produced, 72*time.Hour)
	runUntil(t, b2, repos.Retention{RunBranchTTL: 24 * time.Hour}, 10*time.Millisecond, func() bool {
		_, err := b2.Catalog.GetRepository(t.Context(), "outputs-yolo")
		return err == nil
	})
	time.Sleep(100 * time.Millisecond)
	require.Zero(t, b2.branchReads.Load())
}
