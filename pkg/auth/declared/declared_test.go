// KAOS (kaos-lake, PRD novelcore/kaos#1217 F-06): new file, Apache-2.0.

package declared_test

import (
	"os"
	"path/filepath"
	"slices"
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

	changes, err := declared.Reconcile(ctx, svc, []declared.App{yolo, mlci}, 0)
	require.NoError(t, err)
	require.NotEmpty(t, changes)
	requireKey(t, svc, yolo)
	requireKey(t, svc, mlci)
	// Apps are in no group; their rights are their own policy (PRD #1283 D-04).
	groups, _, err := svc.ListUserGroups(ctx, yolo.User, &model.PaginationParams{Amount: -1})
	require.NoError(t, err)
	require.Empty(t, groups)
	require.False(t, allowed(t, svc, yolo.User, permissions.WriteObjectAction, objct), "no legacy repository configured")
	require.False(t, allowed(t, svc, yolo.User, permissions.DeleteRepositoryAction, repo))
	require.False(t, allowed(t, svc, yolo.User, permissions.CreateUserAction, permissions.UserArn("x")))

	changes, err = declared.Reconcile(ctx, svc, []declared.App{yolo, mlci}, 0)
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

	_, err = declared.Reconcile(ctx, svc, nil, 0)
	require.NoError(t, err)
	require.True(t, allowed(t, svc, "alice", permissions.CreateRepositoryAction, repo))
	require.True(t, allowed(t, svc, "alice", permissions.AttachStorageNamespaceAction, permissions.StorageNamespace("gs://bucket/data")))
	require.False(t, allowed(t, svc, "alice", permissions.DeleteRepositoryAction, repo))
	// Editors cannot delete the branch protection that keeps a dataset's main to pull requests.
	require.False(t, allowed(t, svc, "alice", permissions.SetBranchProtectionRulesAction, repo))
}

func TestReconcile_RotateAndRemove(t *testing.T) {
	ctx := t.Context()
	svc := newStore(t)
	_, err := declared.Reconcile(ctx, svc, []declared.App{yolo, mlci}, 0)
	require.NoError(t, err)

	rotated := yolo
	rotated.AccessKeyID, rotated.SecretAccessKey = "AKIAJYOLO000000002Q", "yolo-secret-2"
	_, err = declared.Reconcile(ctx, svc, []declared.App{rotated}, 0)
	require.NoError(t, err)
	requireKey(t, svc, rotated)
	requireNoKey(t, svc, yolo.AccessKeyID)
	requireNoKey(t, svc, mlci.AccessKeyID)
	_, err = svc.GetUser(ctx, mlci.User)
	require.ErrorIs(t, err, auth.ErrNotFound, "an app no longer declared is deleted")

	// Same key id, new secret: the stored secret is replaced.
	resecret := rotated
	resecret.SecretAccessKey = "yolo-secret-3"
	_, err = declared.Reconcile(ctx, svc, []declared.App{resecret}, 0)
	require.NoError(t, err)
	requireKey(t, svc, resecret)
}

func TestReconcile_UndoesDrift(t *testing.T) {
	ctx := t.Context()
	svc := newStore(t)
	_, err := declared.Reconcile(ctx, svc, []declared.App{yolo}, 0)
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

	_, err = declared.Reconcile(ctx, svc, []declared.App{yolo}, 0)
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
	_, err := declared.Reconcile(ctx, svc, []declared.App{yolo}, 0)
	require.NoError(t, err)
	// Deleting the user removes its stored key too; the acl store's DeleteUser alone would not.
	changes, err := declared.Reconcile(ctx, svc, nil, 0)
	require.NoError(t, err)
	require.Equal(t, []string{"deleted access key " + yolo.AccessKeyID + " of " + yolo.User, "deleted user " + yolo.User, "deleted policy KAOSApp-yolo"}, changes)

	again := yolo
	again.AccessKeyID, again.SecretAccessKey = "AKIAJYOLO000000003Q", "yolo-secret-new"
	_, err = declared.Reconcile(ctx, svc, []declared.App{again}, 0)
	require.NoError(t, err)
	requireKey(t, svc, again)
	requireNoKey(t, svc, yolo.AccessKeyID)
}

// PRD novelcore/kaos#1283 F-01: an app may declare its outputs repository; a name without the
// outputs- prefix fails the whole load.
func TestLoad_OutputsRepository(t *testing.T) {
	dir := t.TempDir()
	write := func(name, outputs string) {
		data := `{"user":"kaos-app-yolo","access_key_id":"AKIAYOLO","secret_access_key":"s","outputs_repository":"` + outputs + `"}`
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600))
	}
	write("kaos-app-yolo", "outputs-yolo")
	apps, err := declared.Load(dir)
	require.NoError(t, err)
	var loaded declared.Loaded
	loaded.Set(apps)
	require.Equal(t, map[string]string{"yolo": "outputs-yolo"}, loaded.Outputs())

	write("kaos-app-yolo", "yolo-outputs")
	_, err = declared.Load(dir)
	require.ErrorIs(t, err, declared.ErrInvalid)
}

// PRD novelcore/kaos#1283: editors may label the datasets they create (dataset-*), and nothing else.
func TestReconcile_EditorsLabelDatasets(t *testing.T) {
	ctx := t.Context()
	svc := newStore(t)
	_, err := declared.Reconcile(ctx, svc, nil, time.Hour)
	require.NoError(t, err)
	p, err := svc.GetPolicy(ctx, declared.RepositoryCreatePolicy)
	require.NoError(t, err)
	var update []string
	for _, st := range p.Statement {
		if slices.Contains(st.Action, "fs:UpdateRepository") {
			update = append(update, st.Resource)
		}
	}
	require.Equal(t, []string{"arn:lakefs:fs:::repository/dataset-*"}, update)
}

// PRD novelcore/kaos#1283 F-08 / D-04 / T-01: every app reads datasets, writes only its own
// outputs repository, and the legacy repository only while one is configured.
func TestReconcile_PerAppRights(t *testing.T) {
	ctx := t.Context()
	svc := newStore(t)
	a := yolo
	a.OutputsRepository = "outputs-yolo"
	b := mlci
	b.OutputsRepository = "outputs-mlci"

	_, err := declared.Reconcile(ctx, svc, []declared.App{a, b}, 0)
	require.NoError(t, err)

	obj := func(repo string) string { return permissions.ObjectArn(repo, "main/x") }
	branch := func(repo string) string { return permissions.BranchArn(repo, "main") }
	// A dataset: read and list, never write, commit, branch or delete.
	require.True(t, allowed(t, svc, a.User, permissions.ReadObjectAction, obj("dataset-cars")))
	require.True(t, allowed(t, svc, a.User, permissions.ListObjectsAction, permissions.RepoArn("dataset-cars")))
	require.True(t, allowed(t, svc, a.User, permissions.ReadRepositoryAction, permissions.RepoArn("dataset-cars")))
	require.False(t, allowed(t, svc, a.User, permissions.WriteObjectAction, obj("dataset-cars")))
	require.False(t, allowed(t, svc, a.User, permissions.CreateCommitAction, branch("dataset-cars")))
	require.False(t, allowed(t, svc, a.User, permissions.CreateTagAction, permissions.TagArn("dataset-cars", "v1")))
	require.False(t, allowed(t, svc, a.User, permissions.DeleteObjectAction, obj("dataset-cars")))
	// Its own outputs: read, write, commit, branch, tag.
	for _, action := range []string{permissions.ReadObjectAction, permissions.WriteObjectAction, permissions.DeleteObjectAction} {
		require.True(t, allowed(t, svc, a.User, action, obj("outputs-yolo")), action)
	}
	require.True(t, allowed(t, svc, a.User, permissions.CreateCommitAction, branch("outputs-yolo")))
	require.True(t, allowed(t, svc, a.User, permissions.CreateBranchAction, permissions.BranchArn("outputs-yolo", "run-1")))
	require.True(t, allowed(t, svc, a.User, permissions.CreateTagAction, permissions.TagArn("outputs-yolo", "t")))
	require.False(t, allowed(t, svc, a.User, permissions.DeleteRepositoryAction, permissions.RepoArn("outputs-yolo")))
	// Another app's outputs, and other repositories: nothing.
	require.True(t, allowed(t, svc, a.User, permissions.ReadObjectAction, obj("outputs-yolo")))
	require.False(t, allowed(t, svc, a.User, permissions.ReadObjectAction, obj("outputs-mlci")))
	require.False(t, allowed(t, svc, a.User, permissions.WriteObjectAction, obj("outputs-mlci")))
	require.False(t, allowed(t, svc, a.User, permissions.WriteObjectAction, obj("outputs-yolo2")), "no prefix match")
	require.False(t, allowed(t, svc, a.User, permissions.WriteObjectAction, obj("data")))
	require.False(t, allowed(t, svc, a.User, permissions.CreateRepositoryAction, permissions.RepoArn("outputs-new")))
	require.False(t, allowed(t, svc, a.User, permissions.CreateUserAction, permissions.UserArn("x")))
	require.True(t, allowed(t, svc, b.User, permissions.WriteObjectAction, obj("outputs-mlci")))
	require.False(t, allowed(t, svc, b.User, permissions.WriteObjectAction, obj("outputs-yolo")))

	// The legacy project repository is writable only once configured, and again closed when not.
	_, err = declared.Reconcile(ctx, svc, []declared.App{a, b}, 0, declared.WithLegacyRepository("data"))
	require.NoError(t, err)
	require.True(t, allowed(t, svc, a.User, permissions.WriteObjectAction, obj("data")))
	require.True(t, allowed(t, svc, b.User, permissions.CreateCommitAction, branch("data")))
	require.False(t, allowed(t, svc, a.User, permissions.DeleteRepositoryAction, permissions.RepoArn("data")))
	require.False(t, allowed(t, svc, a.User, permissions.WriteObjectAction, obj("data2")))
	_, err = declared.Reconcile(ctx, svc, []declared.App{a, b}, 0)
	require.NoError(t, err)
	require.False(t, allowed(t, svc, a.User, permissions.WriteObjectAction, obj("data")))
}

// An app without an outputs repository still reads datasets; policies follow the declaration and
// non-app users and policies are never touched.
func TestReconcile_AppPolicyLifecycle(t *testing.T) {
	ctx := t.Context()
	svc := newStore(t)
	_, err := declared.Reconcile(ctx, svc, []declared.App{yolo, mlci}, 0)
	require.NoError(t, err)
	require.True(t, allowed(t, svc, yolo.User, permissions.ReadObjectAction, permissions.ObjectArn("dataset-x", "main/a")))

	// Drift: an extra policy on the app is detached, an edited app policy is restored.
	require.NoError(t, svc.WritePolicy(ctx, &model.Policy{CreatedAt: time.Now(), DisplayName: "Other",
		Statement: model.Statements{{Effect: model.StatementEffectAllow, Action: []string{"fs:*"}, Resource: permissions.All}}}, false))
	require.NoError(t, svc.AttachPolicyToUser(ctx, "Other", yolo.User))
	require.NoError(t, svc.WritePolicy(ctx, &model.Policy{CreatedAt: time.Now(), DisplayName: declared.AppPolicyName(yolo.User),
		Statement: model.Statements{{Effect: model.StatementEffectAllow, Action: []string{"fs:*"}, Resource: permissions.All}}}, true))
	require.True(t, allowed(t, svc, yolo.User, permissions.DeleteRepositoryAction, repo))
	_, err = declared.Reconcile(ctx, svc, []declared.App{yolo, mlci}, 0)
	require.NoError(t, err)
	require.False(t, allowed(t, svc, yolo.User, permissions.DeleteRepositoryAction, repo))

	// An app no longer declared loses its user and policy; "Other" stays.
	_, err = declared.Reconcile(ctx, svc, []declared.App{yolo}, 0)
	require.NoError(t, err)
	_, err = svc.GetPolicy(ctx, declared.AppPolicyName(mlci.User))
	require.ErrorIs(t, err, auth.ErrNotFound)
	_, err = svc.GetPolicy(ctx, declared.AppPolicyName(yolo.User))
	require.NoError(t, err)
	_, err = svc.GetPolicy(ctx, "Other")
	require.NoError(t, err)
}
