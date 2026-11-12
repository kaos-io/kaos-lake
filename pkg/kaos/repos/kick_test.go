// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-12): new file, Apache-2.0.

package repos_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/treeverse/lakefs/contrib/auth/acl"
	"github.com/treeverse/lakefs/pkg/auth"
	"github.com/treeverse/lakefs/pkg/auth/crypt"
	authparams "github.com/treeverse/lakefs/pkg/auth/params"
	"github.com/treeverse/lakefs/pkg/graveler"
	"github.com/treeverse/lakefs/pkg/kaos/repos"
	"github.com/treeverse/lakefs/pkg/kv/kvtest"
	"github.com/treeverse/lakefs/pkg/logging"
)

// Kick applies a share or an unshare at once, not at the next interval (an hour here).
func TestRun_KickAppliesSharesNow(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	svc := acl.NewAuthService(kvtest.GetStore(ctx, t), crypt.NewSecretStore([]byte("k")), authparams.ServiceCache{})
	f := newFake()
	f.add("dataset-cars", map[string]string{repos.KeyKind: "dataset", repos.KeySharedWith: "orbits"})
	for len(repos.Kicks()) > 0 { // a kick left by another test
		<-repos.Kicks()
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		repos.Run(ctx, f, svc, "", "gs://kaos-e2e-lakefs/", "", "", func() map[string]string { return nil },
			func() map[string]string { return nil }, time.Hour, 0, repos.Retention{}, logging.ContextUnavailable())
	}()
	hasGroup := func(project string) bool {
		_, err := svc.GetGroup(ctx, repos.ConsumerGroupPrefix+project)
		if err != nil && !errors.Is(err, auth.ErrNotFound) {
			t.Fatal(err)
		}
		return err == nil
	}
	require.Eventually(t, func() bool { return hasGroup("orbits") }, 5*time.Second, 10*time.Millisecond, "the first pass shares")

	setShared := func(v string) {
		require.NoError(t, f.UpdateRepositoryMetadata(ctx, "dataset-cars", graveler.RepositoryMetadata{repos.KeySharedWith: v}))
	}
	setShared("orbits,vision")
	time.Sleep(100 * time.Millisecond)
	require.False(t, hasGroup("vision"), "without a kick nothing happens before the interval")
	repos.Kick()
	require.Eventually(t, func() bool { return hasGroup("vision") }, 5*time.Second, 10*time.Millisecond, "a kick shares now")

	require.NoError(t, f.DeleteRepositoryMetadata(ctx, "dataset-cars", []string{repos.KeySharedWith}))
	repos.Kick()
	repos.Kick() // never blocks, even with one pending
	require.Eventually(t, func() bool { return !hasGroup("orbits") && !hasGroup("vision") }, 5*time.Second, 10*time.Millisecond,
		"a kick unshares now")

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop with its context")
	}
}

// Without a pending Run, Kick still never blocks.
func TestKick_NeverBlocks(t *testing.T) {
	for range 3 {
		repos.Kick()
	}
	require.Len(t, repos.Kicks(), 1)
	<-repos.Kicks()
}

func TestValidProject(t *testing.T) {
	for _, ok := range []string{"a", "vision", "e2e-mls-1a2b", "a1"} {
		require.True(t, repos.ValidProject(ok), ok)
	}
	for _, bad := range []string{"", "-a", "a-", "A", "a_b", "a/b", "a.b", string(make([]byte, 64))} {
		require.False(t, repos.ValidProject(bad), bad)
	}
}
