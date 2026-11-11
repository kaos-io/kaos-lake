// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-03/F-05): new file, Apache-2.0.

package repos_test

import (
	"context"
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
