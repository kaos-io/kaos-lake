// KAOS (kaos-lake, PRD novelcore/kaos#1217 F-02): new file, Apache-2.0.

package multiuser_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/treeverse/lakefs/contrib/auth/acl"
	"github.com/treeverse/lakefs/pkg/auth"
	"github.com/treeverse/lakefs/pkg/auth/crypt"
	"github.com/treeverse/lakefs/pkg/auth/model"
	"github.com/treeverse/lakefs/pkg/auth/multiuser"
	authparams "github.com/treeverse/lakefs/pkg/auth/params"
	"github.com/treeverse/lakefs/pkg/auth/setup"
	"github.com/treeverse/lakefs/pkg/config"
	"github.com/treeverse/lakefs/pkg/kv"
	"github.com/treeverse/lakefs/pkg/kv/kvtest"
	"github.com/treeverse/lakefs/pkg/logging"
	"github.com/treeverse/lakefs/pkg/permissions"
)

const (
	adminName   = "admin"
	adminKeyID  = "AKIAJEXAMPLEADMIN01Q"
	adminSecret = "admin-secret-access-key"
)

func newServices(t *testing.T) (kv.Store, crypt.SecretStore, *auth.BasicAuthService, *multiuser.Service) {
	t.Helper()
	store := kvtest.GetStore(t.Context(), t)
	secretStore := crypt.NewSecretStore([]byte("encrypt-secret"))
	noCache := authparams.ServiceCache{Enabled: false}
	basic := auth.NewBasicAuthService(store, secretStore, noCache, logging.ContextUnavailable())
	svc := &multiuser.Service{AuthService: acl.NewAuthService(store, secretStore, noCache)}
	return store, secretStore, basic, svc
}

// basicAdmin sets up the admin the way a basic-mode lakeFS does on POST /setup_lakefs.
func basicAdmin(t *testing.T, basic *auth.BasicAuthService) {
	t.Helper()
	_, err := basic.CreateUser(t.Context(), &model.User{Username: adminName, CreatedAt: time.Now()})
	require.NoError(t, err)
	_, err = basic.AddCredentials(t.Context(), adminName, adminKeyID, adminSecret)
	require.NoError(t, err)
}

func allowed(t *testing.T, svc auth.Service, username, action, resource string) bool {
	t.Helper()
	resp, err := svc.Authorize(t.Context(), &auth.AuthorizationRequest{
		Username:            username,
		RequiredPermissions: permissions.Node{Permission: permissions.Permission{Action: action, Resource: resource}},
	})
	require.NoError(t, err)
	return resp.Allowed
}

func requireAdmin(t *testing.T, svc auth.Service) {
	t.Helper()
	ctx := t.Context()
	cred, err := svc.GetCredentials(ctx, adminKeyID)
	require.NoError(t, err)
	require.Equal(t, adminName, cred.Username)
	require.Equal(t, adminSecret, cred.SecretAccessKey, "the admin keeps its key pair")
	groups, _, err := svc.ListUserGroups(ctx, adminName, &model.PaginationParams{Amount: -1})
	require.NoError(t, err)
	require.Len(t, groups, 1)
	require.Equal(t, setup.AdminsGroup, groups[0].DisplayName)
	require.True(t, allowed(t, svc, adminName, permissions.CreateRepositoryAction, permissions.RepoArn("data")))
}

func TestEnabled(t *testing.T) {
	for _, tc := range []struct {
		rbac, endpoint string
		want           bool
	}{
		{rbac: config.AuthRBACNone},
		{rbac: config.AuthRBACSimplified},
		{rbac: config.AuthRBACExternal, endpoint: "http://auth"},
		{rbac: config.AuthRBACInternal, endpoint: "http://auth"},
		{rbac: config.AuthRBACInternal, want: true},
	} {
		cfg := &config.ConfigImpl{}
		cfg.Auth.GetAuthUIConfig().RBAC = tc.rbac
		cfg.Auth.GetBaseAuthConfig().API.Endpoint = tc.endpoint
		require.Equal(t, tc.want, multiuser.Enabled(cfg), "rbac=%q endpoint=%q", tc.rbac, tc.endpoint)
	}
}

// A server switched from basic to multi-user mode starts with its admin migrated.
func TestNewAuthService_MigratesOnStart(t *testing.T) {
	ctx := t.Context()
	store, _, basic, _ := newServices(t)
	basicAdmin(t, basic)
	meta := auth.NewKVMetadataManager("test", "", "", store)
	require.NoError(t, meta.UpdateSetupTimestamp(ctx, time.Now(), config.AuthRBACNone))

	cfg := &config.ConfigImpl{}
	cfg.Auth.GetAuthUIConfig().RBAC = config.AuthRBACInternal
	cfg.Auth.GetBaseAuthConfig().Encrypt.SecretKey = "encrypt-secret"
	svc := multiuser.NewAuthService(ctx, cfg, logging.ContextUnavailable(), store, meta)
	require.True(t, svc.IsAdvancedAuth())
	requireAdmin(t, svc)
}

func TestMigrateFromBasic(t *testing.T) {
	ctx := t.Context()
	store, secretStore, basic, svc := newServices(t)
	basicAdmin(t, basic)

	username, err := multiuser.MigrateFromBasic(ctx, store, secretStore, svc)
	require.NoError(t, err)
	require.Equal(t, adminName, username)
	requireAdmin(t, svc)

	// Basic-mode records are untouched, so switching back to rbac "none" keeps the same key.
	cred, err := basic.GetCredentials(ctx, adminKeyID)
	require.NoError(t, err)
	require.Equal(t, adminSecret, cred.SecretAccessKey)

	// Later starts do nothing.
	username, err = multiuser.MigrateFromBasic(ctx, store, secretStore, svc)
	require.NoError(t, err)
	require.Empty(t, username)
}

func TestMigrateFromBasic_ResumesInterruptedRun(t *testing.T) {
	ctx := t.Context()
	store, secretStore, basic, svc := newServices(t)
	basicAdmin(t, basic)
	// A run that stopped after creating a group and the user.
	_, err := svc.CreateGroup(ctx, &model.Group{DisplayName: setup.AdminsGroup, CreatedAt: time.Now()})
	require.NoError(t, err)
	_, err = svc.CreateUser(ctx, &model.User{Username: adminName, CreatedAt: time.Now()})
	require.NoError(t, err)

	username, err := multiuser.MigrateFromBasic(ctx, store, secretStore, svc)
	require.NoError(t, err)
	require.Equal(t, adminName, username)
	requireAdmin(t, svc)
}

func TestMigrateFromBasic_SetUpInMultiUserMode(t *testing.T) {
	ctx := t.Context()
	store, secretStore, _, svc := newServices(t)

	// Initialized but no users anywhere: refuse rather than run without an admin.
	_, err := multiuser.MigrateFromBasic(ctx, store, secretStore, svc)
	require.ErrorIs(t, err, auth.ErrMigrationNotPossible)

	_, err = setup.CreateAdminUser(ctx, svc, &model.SuperuserConfiguration{
		User: model.User{Username: adminName, CreatedAt: time.Now()}, AccessKeyID: adminKeyID, SecretAccessKey: adminSecret,
	})
	require.NoError(t, err)
	username, err := multiuser.MigrateFromBasic(ctx, store, secretStore, svc)
	require.NoError(t, err)
	require.Empty(t, username)
	requireAdmin(t, svc)
}

// Fresh setup creates the RBAC base groups (IsAdvancedAuth) and access follows group membership.
func TestSetupAndPolicies(t *testing.T) {
	ctx := t.Context()
	_, _, _, svc := newServices(t)
	_, err := setup.CreateAdminUser(ctx, svc, &model.SuperuserConfiguration{
		User: model.User{Username: adminName, CreatedAt: time.Now()}, AccessKeyID: adminKeyID, SecretAccessKey: adminSecret,
	})
	require.NoError(t, err)
	requireAdmin(t, svc)

	_, err = svc.CreateUser(ctx, &model.User{Username: "viewer", CreatedAt: time.Now()})
	require.NoError(t, err)
	object := permissions.ObjectArn("data", "main/images/1.jpg")
	require.False(t, allowed(t, svc, "viewer", permissions.ReadObjectAction, object), "no group, no access")

	require.NoError(t, svc.AddUserToGroup(ctx, "viewer", setup.ViewersGroup))
	require.True(t, allowed(t, svc, "viewer", permissions.ReadObjectAction, object))
	require.False(t, allowed(t, svc, "viewer", permissions.WriteObjectAction, object))
	require.False(t, allowed(t, svc, "viewer", permissions.CreateRepositoryAction, permissions.RepoArn("data")))
}
