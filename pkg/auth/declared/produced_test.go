// KAOS (kaos-lake, PRD novelcore/kaos#1911 CON-02, CON-03): new file, Apache-2.0.

package declared_test

import (
	"context"
	"path"
	"testing"
	"time"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"github.com/treeverse/lakefs/pkg/auth/declared"
	"github.com/treeverse/lakefs/pkg/block"
	"github.com/treeverse/lakefs/pkg/cache"
	"github.com/treeverse/lakefs/pkg/catalog"
	"github.com/treeverse/lakefs/pkg/config"
	"github.com/treeverse/lakefs/pkg/graveler"
	"github.com/treeverse/lakefs/pkg/graveler/settings"
	"github.com/treeverse/lakefs/pkg/kaos/repos"
	"github.com/treeverse/lakefs/pkg/kv/kvtest"
	"github.com/treeverse/lakefs/pkg/kv/mem"
	"github.com/treeverse/lakefs/pkg/permissions"
	"github.com/treeverse/lakefs/pkg/upload"
)

// realCatalog is a lakeFS catalog over in-memory storage, as pkg/kaos/repos tests use.
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

// The app-users reconcile and the repositories reconcile are two loops with their own intervals.
// Whatever order they run in, an app never holds the write rights on a declared dataset (above
// all, the commit right on its main) before the repository exists as the app's own with main
// protected; and it never gets them on a dataset that is not its own.
func TestReconcile_WriteRightsWaitForTheDataset(t *testing.T) {
	ctx := t.Context()
	svc := newStore(t)
	c := realCatalog(t)
	app := yolo
	app.Datasets = []string{"dataset-clean", "dataset-cars"}
	produced := repos.Desired("", nil, map[string]string{"dataset-clean": "yolo", "dataset-cars": "yolo"})
	// dataset-cars is a dataset people uploaded: labelled, protected, with data.
	_, err := c.CreateRepository(ctx, "dataset-cars", "", "mem://dataset-cars", "main", false)
	require.NoError(t, err)
	require.NoError(t, c.CreateEntry(ctx, "dataset-cars", "main", catalog.DBEntry{
		Path: "a.jpg", PhysicalAddress: "mem://x/a.jpg", CreationDate: time.Now(), Size: 1, Checksum: "a",
	}))
	_, err = c.Commit(ctx, "dataset-cars", "main", "upload", "alice", nil, nil, nil, false)
	require.NoError(t, err)
	require.NoError(t, c.UpdateRepositoryMetadata(ctx, "dataset-cars", graveler.RepositoryMetadata{repos.KeyKind: repos.KindDataset}))

	ready := declared.WithProducedDatasets(func(ctx context.Context, dataset, app string) bool {
		ok, err := repos.ProducedBy(ctx, c, dataset, app)
		require.NoError(t, err)
		return ok
	})
	policyPass := func() {
		t.Helper()
		_, err := declared.Reconcile(ctx, svc, []declared.App{app}, 0, ready)
		require.NoError(t, err)
	}
	writes := func(dataset string) bool {
		t.Helper()
		rights := []bool{
			allowed(t, svc, app.User, permissions.WriteObjectAction, permissions.ObjectArn(dataset, "a")),
			allowed(t, svc, app.User, permissions.CreateBranchAction, permissions.BranchArn(dataset, "run-1")),
			allowed(t, svc, app.User, permissions.CreateCommitAction, permissions.BranchArn(dataset, "run-1")),
			allowed(t, svc, app.User, permissions.CreateCommitAction, permissions.BranchArn(dataset, "main")),
			allowed(t, svc, app.User, permissions.CreateTagAction, permissions.TagArn(dataset, "k-1")),
		}
		for _, r := range rights[1:] {
			require.Equal(t, rights[0], r, "the write rights on %s come and go together", dataset)
		}
		return rights[0]
	}
	reads := func(dataset string) bool {
		return allowed(t, svc, app.User, permissions.ReadObjectAction, permissions.ObjectArn(dataset, "a"))
	}

	// The bad order: the policy loop first, the repository not there yet.
	policyPass()
	require.False(t, writes("dataset-clean"), "no repository yet")
	require.True(t, reads("dataset-clean"))

	// The repositories loop creates and labels it, and is cut short before protecting main.
	_, err = repos.Reconcile(ctx, c, "", "mem://lake", "", produced)
	require.ErrorIs(t, err, repos.ErrNotAdopted, "dataset-cars is people's")
	policyPass()
	require.False(t, writes("dataset-clean"), "main is not protected yet")

	// A direct commit by the app here would have reached an unprotected main; it has no right to.
	require.NoError(t, c.CreateEntry(ctx, "dataset-clean", "main", catalog.DBEntry{
		Path: "probe", PhysicalAddress: "mem://x/probe", CreationDate: time.Now(), Size: 1, Checksum: "p",
	}), "the core would still take a write at this point")
	require.NoError(t, c.ResetBranch(ctx, "dataset-clean", "main"))

	// Protected, but the policy loop has not run since: still closed.
	_, err = repos.ReconcileProtection(ctx, c, c)
	require.NoError(t, err)
	require.False(t, writes("dataset-clean"), "the policy loop has not seen it yet")

	policyPass()
	require.True(t, writes("dataset-clean"), "exists, the app's own, main protected")

	// The dataset people uploaded is never the app's, declared or not, protected or not.
	require.False(t, writes("dataset-cars"))
	require.True(t, reads("dataset-cars"))
	md, err := c.GetRepositoryMetadata(ctx, "dataset-cars")
	require.NoError(t, err)
	require.NotContains(t, md, repos.KeyProducer)

	// Protection taken off main: the next policy pass takes the rights back.
	require.NoError(t, c.SetBranchProtectionRules(ctx, "dataset-clean", &graveler.BranchProtectionRules{}, nil))
	policyPass()
	require.False(t, writes("dataset-clean"))
}
