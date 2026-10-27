// KAOS (kaos-lake, PRD novelcore/kaos#1217 F-06): new file, Apache-2.0.

package declared_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/treeverse/lakefs/contrib/auth/acl"
	"github.com/treeverse/lakefs/pkg/auth"
	"github.com/treeverse/lakefs/pkg/auth/crypt"
	"github.com/treeverse/lakefs/pkg/auth/declared"
	"github.com/treeverse/lakefs/pkg/auth/model"
	"github.com/treeverse/lakefs/pkg/auth/multiuser"
	authparams "github.com/treeverse/lakefs/pkg/auth/params"
	"github.com/treeverse/lakefs/pkg/auth/setup"
	"github.com/treeverse/lakefs/pkg/kv/kvtest"
	"github.com/treeverse/lakefs/pkg/permissions"
)

var (
	yolo  = declared.App{User: "kaos-app-yolo", AccessKeyID: "AKIAJYOLO000000001Q", SecretAccessKey: "yolo-secret"}
	mlci  = declared.App{User: "kaos-app-mlci", AccessKeyID: "AKIAJMLCI000000001Q", SecretAccessKey: "mlci-secret"}
	repo  = permissions.RepoArn("data")
	objct = permissions.ObjectArn("data", "main/x")
)

// newStore returns a multi-user store set up like a fresh stack: base groups and an admin.
func newStore(t *testing.T) auth.Service {
	t.Helper()
	ctx := t.Context()
	// The service lakefs run uses in multi-user mode (it sets up the RBAC base groups).
	svc := &multiuser.Service{AuthService: acl.NewAuthService(kvtest.GetStore(ctx, t), crypt.NewSecretStore([]byte("k")), authparams.ServiceCache{})}
	_, err := setup.CreateAdminUser(ctx, svc, &model.SuperuserConfiguration{
		User: model.User{Username: "admin", CreatedAt: time.Now()}, AccessKeyID: "AKIAJADMIN00000001Q", SecretAccessKey: "admin-secret",
	})
	require.NoError(t, err)
	return svc
}

func allowed(t *testing.T, svc auth.Service, user, action, resource string) bool {
	t.Helper()
	resp, err := svc.Authorize(t.Context(), &auth.AuthorizationRequest{
		Username:            user,
		RequiredPermissions: permissions.Node{Permission: permissions.Permission{Action: action, Resource: resource}},
	})
	require.NoError(t, err)
	return resp.Allowed
}

func requireKey(t *testing.T, svc auth.Service, a declared.App) {
	t.Helper()
	c, err := svc.GetCredentials(t.Context(), a.AccessKeyID)
	require.NoError(t, err)
	require.Equal(t, a.User, c.Username)
	require.Equal(t, a.SecretAccessKey, c.SecretAccessKey)
}

func requireNoKey(t *testing.T, svc auth.Service, accessKeyID string) {
	t.Helper()
	_, err := svc.GetCredentials(t.Context(), accessKeyID)
	require.ErrorIs(t, err, auth.ErrNotFound)
}

func writeApp(t *testing.T, dir, name, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600))
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	writeApp(t, dir, "kubecore-acme-vision-ml-lakefs-app-yolo", `{"user":"kaos-app-yolo","access_key_id":"AKIAJYOLO000000001Q","secret_access_key":"yolo-secret"}`)
	// A mounted Secret's own entries are skipped.
	require.NoError(t, os.Mkdir(filepath.Join(dir, "..data"), 0o700))
	apps, err := declared.Load(dir)
	require.NoError(t, err)
	require.Equal(t, []declared.App{yolo}, apps)

	empty, err := declared.Load(t.TempDir())
	require.NoError(t, err)
	require.Empty(t, empty)

	for name, content := range map[string]string{
		"not json":       `{`,
		"foreign user":   `{"user":"alice","access_key_id":"AKIAJALICE0000001Q","secret_access_key":"s"}`,
		"bare prefix":    `{"user":"kaos-app-","access_key_id":"AKIAJBARE00000001Q","secret_access_key":"s"}`,
		"no secret":      `{"user":"kaos-app-x","access_key_id":"AKIAJX0000000001Q"}`,
		"long key id":    `{"user":"kaos-app-x","access_key_id":"AKIAJX00000000000000001Q","secret_access_key":"s"}`,
		"duplicate user": `{"user":"kaos-app-yolo","access_key_id":"AKIAJOTHER000001Q","secret_access_key":"s"}`,
		"duplicate key":  `{"user":"kaos-app-other","access_key_id":"AKIAJYOLO000000001Q","secret_access_key":"s"}`,
	} {
		t.Run(name, func(t *testing.T) {
			d := t.TempDir()
			writeApp(t, d, "a-yolo", `{"user":"kaos-app-yolo","access_key_id":"AKIAJYOLO000000001Q","secret_access_key":"yolo-secret"}`)
			writeApp(t, d, "b-bad", content)
			_, err := declared.Load(d)
			require.ErrorIs(t, err, declared.ErrInvalid)
		})
	}
}

func TestReconcile(t *testing.T) {
	ctx := t.Context()
	svc := newStore(t)

	changes, err := declared.Reconcile(ctx, svc, []declared.App{yolo, mlci})
	require.NoError(t, err)
	require.NotEmpty(t, changes)
	requireKey(t, svc, yolo)
	requireKey(t, svc, mlci)
	// Apps are project editors: read and write data, no deleting repositories, no auth admin.
	require.True(t, allowed(t, svc, yolo.User, permissions.WriteObjectAction, objct))
	require.True(t, allowed(t, svc, yolo.User, permissions.CreateCommitAction, permissions.BranchArn("data", "outputs")))
	require.False(t, allowed(t, svc, yolo.User, permissions.DeleteRepositoryAction, repo))
	require.False(t, allowed(t, svc, yolo.User, permissions.CreateUserAction, permissions.UserArn("x")))

	changes, err = declared.Reconcile(ctx, svc, []declared.App{yolo, mlci})
	require.NoError(t, err)
	require.Empty(t, changes, "a second run changes nothing")

	// The admin and people are never touched.
	requireKey(t, svc, declared.App{User: "admin", AccessKeyID: "AKIAJADMIN00000001Q", SecretAccessKey: "admin-secret"})
}

// People who are editors through their project role may create repositories (PRD #1147).
func TestReconcile_EditorsCreateRepositories(t *testing.T) {
	ctx := t.Context()
	svc := newStore(t)
	_, err := svc.CreateUser(ctx, &model.User{Username: "alice", CreatedAt: time.Now()})
	require.NoError(t, err)
	require.NoError(t, svc.AddUserToGroup(ctx, "alice", setup.DevelopersGroup))
	require.False(t, allowed(t, svc, "alice", permissions.CreateRepositoryAction, repo), "upstream Developers cannot")

	_, err = declared.Reconcile(ctx, svc, nil)
	require.NoError(t, err)
	require.True(t, allowed(t, svc, "alice", permissions.CreateRepositoryAction, repo))
	require.True(t, allowed(t, svc, "alice", permissions.AttachStorageNamespaceAction, permissions.StorageNamespace("gs://bucket/data")))
	require.False(t, allowed(t, svc, "alice", permissions.DeleteRepositoryAction, repo))
}

func TestReconcile_RotateAndRemove(t *testing.T) {
	ctx := t.Context()
	svc := newStore(t)
	_, err := declared.Reconcile(ctx, svc, []declared.App{yolo, mlci})
	require.NoError(t, err)

	rotated := yolo
	rotated.AccessKeyID, rotated.SecretAccessKey = "AKIAJYOLO000000002Q", "yolo-secret-2"
	_, err = declared.Reconcile(ctx, svc, []declared.App{rotated})
	require.NoError(t, err)
	requireKey(t, svc, rotated)
	requireNoKey(t, svc, yolo.AccessKeyID)
	requireNoKey(t, svc, mlci.AccessKeyID)
	_, err = svc.GetUser(ctx, mlci.User)
	require.ErrorIs(t, err, auth.ErrNotFound, "an app no longer declared is deleted")

	// Same key id, new secret: the stored secret is replaced.
	resecret := rotated
	resecret.SecretAccessKey = "yolo-secret-3"
	_, err = declared.Reconcile(ctx, svc, []declared.App{resecret})
	require.NoError(t, err)
	requireKey(t, svc, resecret)
}

func TestReconcile_UndoesDrift(t *testing.T) {
	ctx := t.Context()
	svc := newStore(t)
	_, err := declared.Reconcile(ctx, svc, []declared.App{yolo})
	require.NoError(t, err)

	// By hand: the app joins Admins, gets a second key, and the policy is widened.
	require.NoError(t, svc.AddUserToGroup(ctx, yolo.User, setup.AdminsGroup))
	_, err = svc.AddCredentials(ctx, yolo.User, "AKIAJEXTRA00000001Q", "extra")
	require.NoError(t, err)
	require.NoError(t, svc.WritePolicy(ctx, &model.Policy{
		CreatedAt: time.Now(), DisplayName: declared.RepositoryCreatePolicy,
		Statement: model.Statements{{Effect: model.StatementEffectAllow, Action: []string{"fs:*"}, Resource: permissions.All}},
	}, true))
	require.True(t, allowed(t, svc, yolo.User, permissions.DeleteRepositoryAction, repo))

	_, err = declared.Reconcile(ctx, svc, []declared.App{yolo})
	require.NoError(t, err)
	require.False(t, allowed(t, svc, yolo.User, permissions.DeleteRepositoryAction, repo))
	requireNoKey(t, svc, "AKIAJEXTRA00000001Q")
	requireKey(t, svc, yolo)
}

// An app deleted and declared again under the same name gets only its new key: the old one does
// not come back with the user.
func TestReconcile_RecreatedAppLosesOldKey(t *testing.T) {
	ctx := t.Context()
	svc := newStore(t)
	_, err := declared.Reconcile(ctx, svc, []declared.App{yolo})
	require.NoError(t, err)
	// Deleting the user removes its stored key too; the acl store's DeleteUser alone would not.
	changes, err := declared.Reconcile(ctx, svc, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"deleted access key " + yolo.AccessKeyID + " of " + yolo.User, "deleted user " + yolo.User}, changes)

	again := yolo
	again.AccessKeyID, again.SecretAccessKey = "AKIAJYOLO000000003Q", "yolo-secret-new"
	_, err = declared.Reconcile(ctx, svc, []declared.App{again})
	require.NoError(t, err)
	requireKey(t, svc, again)
	requireNoKey(t, svc, yolo.AccessKeyID)
}
