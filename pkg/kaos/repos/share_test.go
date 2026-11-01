// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-12): new file, Apache-2.0.

package repos_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/treeverse/lakefs/contrib/auth/acl"
	"github.com/treeverse/lakefs/pkg/auth"
	"github.com/treeverse/lakefs/pkg/auth/crypt"
	"github.com/treeverse/lakefs/pkg/auth/model"
	authparams "github.com/treeverse/lakefs/pkg/auth/params"
	"github.com/treeverse/lakefs/pkg/kaos/repos"
	"github.com/treeverse/lakefs/pkg/kv/kvtest"
	"github.com/treeverse/lakefs/pkg/permissions"
)

func can(t *testing.T, svc auth.Service, user, action, resource string) bool {
	t.Helper()
	resp, err := svc.Authorize(t.Context(), &auth.AuthorizationRequest{
		Username:            user,
		RequiredPermissions: permissions.Node{Permission: permissions.Permission{Action: action, Resource: resource}},
	})
	require.NoError(t, err)
	return resp.Allowed
}

// AC-11: a dataset shared with project orbits is readable, never writable, by orbits' consumer
// group; unsharing removes the grant and the group.
func TestReconcileShares(t *testing.T) {
	ctx := t.Context()
	svc := acl.NewAuthService(kvtest.GetStore(ctx, t), crypt.NewSecretStore([]byte("k")), authparams.ServiceCache{})
	f := newFake()
	f.add("dataset-cars", map[string]string{repos.KeyKind: "dataset", repos.KeySharedWith: "orbits"})
	f.add("dataset-private", map[string]string{repos.KeyKind: "dataset"})

	changes, err := repos.ReconcileShares(ctx, f, svc)
	require.NoError(t, err)
	require.NotEmpty(t, changes)
	_, err = svc.CreateUser(ctx, &model.User{Username: "bob@orbits.example", CreatedAt: time.Now()})
	require.NoError(t, err)
	require.NoError(t, svc.AddUserToGroup(ctx, "bob@orbits.example", repos.ConsumerGroupPrefix+"orbits"))

	bob := "bob@orbits.example"
	require.True(t, can(t, svc, bob, permissions.ReadRepositoryAction, permissions.RepoArn("dataset-cars")))
	require.True(t, can(t, svc, bob, permissions.ReadObjectAction, permissions.ObjectArn("dataset-cars", "data.yaml")))
	require.True(t, can(t, svc, bob, permissions.ListObjectsAction, permissions.RepoArn("dataset-cars")))
	require.False(t, can(t, svc, bob, permissions.WriteObjectAction, permissions.ObjectArn("dataset-cars", "x")))
	require.False(t, can(t, svc, bob, permissions.CreateCommitAction, permissions.BranchArn("dataset-cars", "main")))
	require.False(t, can(t, svc, bob, permissions.ReadObjectAction, permissions.ObjectArn("dataset-private", "x")))

	again, err := repos.ReconcileShares(ctx, f, svc)
	require.NoError(t, err)
	require.Empty(t, again, "a second pass changes nothing")

	f.md["dataset-cars"][repos.KeySharedWith] = ""
	_, err = repos.ReconcileShares(ctx, f, svc)
	require.NoError(t, err)
	require.False(t, can(t, svc, bob, permissions.ReadObjectAction, permissions.ObjectArn("dataset-cars", "data.yaml")))
	_, err = svc.GetGroup(ctx, repos.ConsumerGroupPrefix+"orbits")
	require.ErrorIs(t, err, auth.ErrNotFound)
}
