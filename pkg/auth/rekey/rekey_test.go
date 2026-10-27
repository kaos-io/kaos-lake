// KAOS (kaos-lake, PRD novelcore/kaos#1217 T-07): new file, Apache-2.0.

package rekey_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/treeverse/lakefs/contrib/auth/acl"
	"github.com/treeverse/lakefs/pkg/auth"
	"github.com/treeverse/lakefs/pkg/auth/crypt"
	"github.com/treeverse/lakefs/pkg/auth/model"
	authparams "github.com/treeverse/lakefs/pkg/auth/params"
	"github.com/treeverse/lakefs/pkg/auth/rekey"
	"github.com/treeverse/lakefs/pkg/config"
	"github.com/treeverse/lakefs/pkg/kv"
	"github.com/treeverse/lakefs/pkg/kv/kvtest"
	"github.com/treeverse/lakefs/pkg/logging"
)

const (
	chartDefaultKey = "asdjfhjaskdhuioaweyuiorasdsjbaskcbkj"
	stackKey        = "per-stack-random-key"
)

var noCache = authparams.ServiceCache{Enabled: false}

func basicService(store kv.Store, key string) *auth.BasicAuthService {
	return auth.NewBasicAuthService(store, crypt.NewSecretStore([]byte(key)), noCache, logging.ContextUnavailable())
}

func aclService(store kv.Store, key string) *acl.AuthService {
	return acl.NewAuthService(store, crypt.NewSecretStore([]byte(key)), noCache)
}

// seed stores the basic-mode admin and a multi-user app key, both under key.
func seed(t *testing.T, store kv.Store, key string) {
	t.Helper()
	ctx := t.Context()
	basic := basicService(store, key)
	_, err := basic.CreateUser(ctx, &model.User{Username: "admin", CreatedAt: time.Now()})
	require.NoError(t, err)
	_, err = basic.AddCredentials(ctx, "admin", "AKIAJADMIN0000000001", "admin-secret")
	require.NoError(t, err)

	svc := aclService(store, key)
	_, err = svc.CreateUser(ctx, &model.User{Username: "app", CreatedAt: time.Now()})
	require.NoError(t, err)
	_, err = svc.AddCredentials(ctx, "app", "AKIAJAPP000000000001", "app-secret")
	require.NoError(t, err)
}

// requireSecrets checks both stored keys decrypt under key to their original secrets.
func requireSecrets(t *testing.T, store kv.Store, key string) {
	t.Helper()
	ctx := t.Context()
	c, err := basicService(store, key).GetCredentials(ctx, "AKIAJADMIN0000000001")
	require.NoError(t, err)
	require.Equal(t, "admin-secret", c.SecretAccessKey)
	c, err = aclService(store, key).GetCredentials(ctx, "AKIAJAPP000000000001")
	require.NoError(t, err)
	require.Equal(t, "app-secret", c.SecretAccessKey)
}

func TestReencrypt(t *testing.T) {
	ctx := t.Context()
	store := kvtest.GetStore(ctx, t)
	seed(t, store, chartDefaultKey)
	current, previous := crypt.NewSecretStore([]byte(stackKey)), crypt.NewSecretStore([]byte(chartDefaultKey))

	n, err := rekey.Reencrypt(ctx, store, current, previous)
	require.NoError(t, err)
	require.Equal(t, 2, n)
	requireSecrets(t, store, stackKey)
	_, err = basicService(store, chartDefaultKey).GetCredentials(ctx, "AKIAJADMIN0000000001")
	require.Error(t, err, "the old key no longer opens the admin secret")

	// Later starts find nothing to do.
	n, err = rekey.Reencrypt(ctx, store, current, previous)
	require.NoError(t, err)
	require.Zero(t, n)
	requireSecrets(t, store, stackKey)
}

// A stack created with its own key from the start is left alone.
func TestReencrypt_AlreadyCurrent(t *testing.T) {
	ctx := t.Context()
	store := kvtest.GetStore(ctx, t)
	seed(t, store, stackKey)

	n, err := rekey.Reencrypt(ctx, store, crypt.NewSecretStore([]byte(stackKey)), crypt.NewSecretStore([]byte(chartDefaultKey)))
	require.NoError(t, err)
	require.Zero(t, n)
	requireSecrets(t, store, stackKey)
}

// A secret neither key opens stops the start instead of leaving that access key unusable.
func TestReencrypt_Undecryptable(t *testing.T) {
	ctx := t.Context()
	store := kvtest.GetStore(ctx, t)
	seed(t, store, "some-other-key")

	_, err := rekey.Reencrypt(ctx, store, crypt.NewSecretStore([]byte(stackKey)), crypt.NewSecretStore([]byte(chartDefaultKey)))
	require.ErrorIs(t, err, rekey.ErrUndecryptable)
}

func TestFromConfig(t *testing.T) {
	ctx := t.Context()
	store := kvtest.GetStore(ctx, t)
	seed(t, store, chartDefaultKey)
	cfg := &config.ConfigImpl{}
	enc := &cfg.Auth.GetBaseAuthConfig().Encrypt

	enc.SecretKey = stackKey
	n, err := rekey.FromConfig(ctx, cfg, store)
	require.NoError(t, err)
	require.Zero(t, n, "no previous key: nothing to do")

	enc.PreviousSecretKey = stackKey
	n, err = rekey.FromConfig(ctx, cfg, store)
	require.NoError(t, err)
	require.Zero(t, n, "previous key equal to the current one: nothing to do")

	enc.PreviousSecretKey = chartDefaultKey
	n, err = rekey.FromConfig(ctx, cfg, store)
	require.NoError(t, err)
	require.Equal(t, 2, n)
	requireSecrets(t, store, stackKey)
}
