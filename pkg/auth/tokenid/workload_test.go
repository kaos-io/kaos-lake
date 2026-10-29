// KAOS (kaos-lake, PRD novelcore/kaos#1217 F-05): new file, Apache-2.0.

package tokenid

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
	"github.com/treeverse/lakefs/contrib/auth/acl"
	"github.com/treeverse/lakefs/pkg/auth"
	"github.com/treeverse/lakefs/pkg/auth/crypt"
	"github.com/treeverse/lakefs/pkg/auth/declared"
	"github.com/treeverse/lakefs/pkg/auth/model"
	authparams "github.com/treeverse/lakefs/pkg/auth/params"
	"github.com/treeverse/lakefs/pkg/config"
	"github.com/treeverse/lakefs/pkg/kv/kvtest"
)

const workloadAudience = "kaos-lake"

// podToken is a projected token of service account sa in namespace ns, as the kubelet mints it.
func podToken(t *testing.T, is *issuer, ns, sa string, edit func(jwt.MapClaims)) string {
	t.Helper()
	return is.token(t, func(c jwt.MapClaims) {
		for _, k := range []string{"email", "groups"} {
			delete(c, k)
		}
		c["aud"] = []string{workloadAudience}
		c["sub"] = "system:serviceaccount:" + ns + ":" + sa
		if edit != nil {
			edit(c)
		}
	})
}

// newWorkload declares yolo in vision-ml, mlci in its own namespace, and old (from before
// namespaces were declared) in none; all three users exist.
func newWorkload(t *testing.T, is *issuer) (*Workload, auth.Service) {
	t.Helper()
	ctx := t.Context()
	svc := acl.NewAuthService(kvtest.GetStore(ctx, t), crypt.NewSecretStore([]byte("k")), authparams.ServiceCache{})
	apps := &declared.Loaded{}
	apps.Set([]declared.App{
		{User: "kaos-app-yolo", Namespace: "vision-ml"},
		{User: "kaos-app-mlci", Namespace: "vision-mlci--train"},
		{User: "kaos-app-old"},
	})
	for _, u := range []string{"kaos-app-yolo", "kaos-app-mlci", "kaos-app-old"} {
		_, err := svc.CreateUser(ctx, &model.User{Username: u, CreatedAt: time.Now()})
		require.NoError(t, err)
	}
	w, err := NewWorkload(config.KAOSWorkloadIdentity{Audience: workloadAudience, Issuer: is.URL, KeyTTL: time.Hour}, svc, apps)
	require.NoError(t, err)
	w.keys.minRefresh = 0
	return w, svc
}

func TestWorkload_Resolve(t *testing.T) {
	is := newIssuer(t)
	w, _ := newWorkload(t, is)

	tok := podToken(t, is, "vision-ml", "yolo-workflow", nil)
	require.True(t, w.Owns(tok))
	u, err := w.Resolve(t.Context(), tok)
	require.NoError(t, err)
	require.Equal(t, "kaos-app-yolo", u.Username)
	u, err = w.Resolve(t.Context(), podToken(t, is, "vision-mlci--train", "mlci-workflow", nil))
	require.NoError(t, err)
	require.Equal(t, "kaos-app-mlci", u.Username)

	for name, tok := range map[string]string{
		"other namespace":       podToken(t, is, "other-ml", "yolo-workflow", nil),
		"other app's namespace": podToken(t, is, "vision-mlci--train", "yolo-workflow", nil),
		"no declared namespace": podToken(t, is, "vision-ml", "old-workflow", nil),
		"empty namespace":       podToken(t, is, "", "old-workflow", nil),
		"not a service account subject": podToken(t, is, "vision-ml", "yolo-workflow", func(c jwt.MapClaims) {
			c["sub"] = "system:node:vision-ml:yolo-workflow"
		}),
		"not a workflow":        podToken(t, is, "vision-ml", "yolo", nil),
		"no app":                podToken(t, is, "vision-ml", "-workflow", nil),
		"app not declared":      podToken(t, is, "vision-ml", "ghost-workflow", nil),
		"other audience":        podToken(t, is, "vision-ml", "yolo-workflow", func(c jwt.MapClaims) { c["aud"] = []string{"https://kubernetes.default.svc"} }),
		"expired":               podToken(t, is, "vision-ml", "yolo-workflow", func(c jwt.MapClaims) { c["exp"] = time.Now().Add(-time.Minute).Unix() }),
		"no expiry":             podToken(t, is, "vision-ml", "yolo-workflow", func(c jwt.MapClaims) { delete(c, "exp") }),
		"not a service account": podToken(t, is, "vision-ml", "yolo-workflow", func(c jwt.MapClaims) { c["sub"] = "kaos-app-yolo" }),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := w.Resolve(t.Context(), tok)
			require.ErrorIs(t, err, ErrDenied)
			require.ErrorIs(t, err, auth.ErrAuthenticatingRequest)
		})
	}

	t.Run("other signer", func(t *testing.T) {
		// Same issuer name and key id, signed by a key the issuer never published.
		forged := podToken(t, newIssuer(t), "vision-ml", "yolo-workflow", func(c jwt.MapClaims) { c["iss"] = is.URL })
		_, err := w.Resolve(t.Context(), forged)
		require.ErrorIs(t, err, ErrDenied)
	})
}

// The cluster's issuer is unreachable (AC-07): a workload token is denied, as a bearer and at
// the exchange, and no key is created.
func TestWorkload_IssuerDown(t *testing.T) {
	is := newIssuer(t)
	tok := podToken(t, is, "vision-ml", "yolo-workflow", nil)
	w, svc := newWorkload(t, is)
	is.Close()

	_, err := w.Resolve(t.Context(), tok)
	require.ErrorIs(t, err, ErrDenied)
	require.ErrorIs(t, err, auth.ErrAuthenticatingRequest)
	r := httptest.NewRequest(http.MethodPost, "/kaos/v1/credentials", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	w.Exchange(rec, r)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	creds, _, err := svc.ListUserCredentials(t.Context(), "kaos-app-yolo", &model.PaginationParams{Amount: 10})
	require.NoError(t, err)
	require.Empty(t, creds)
}

func TestWorkload_Exchange(t *testing.T) {
	is := newIssuer(t)
	w, svc := newWorkload(t, is)
	exchange := func(header string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/kaos/v1/credentials", nil)
		if header != "" {
			r.Header.Set("Authorization", header)
		}
		rec := httptest.NewRecorder()
		w.Exchange(rec, r)
		return rec
	}

	rec := exchange("Bearer " + podToken(t, is, "vision-ml", "yolo-workflow", nil))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var got ExchangeResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.True(t, strings.HasPrefix(got.AccessKeyID, declared.ExchangeKeyPrefix))
	require.Len(t, got.AccessKeyID, 20)
	require.Len(t, got.SecretAccessKey, 40)
	require.WithinDuration(t, time.Now().Add(time.Hour), got.Expiration, time.Minute)

	c, err := declared.ExpiringKeys(svc, time.Hour).GetCredentials(t.Context(), got.AccessKeyID)
	require.NoError(t, err)
	require.Equal(t, "kaos-app-yolo", c.Username)
	require.Equal(t, got.SecretAccessKey, c.SecretAccessKey)

	for name, header := range map[string]string{
		"no token":     "",
		"basic":        "Basic eDp5",
		"denied token": "Bearer " + podToken(t, is, "other-ml", "yolo-workflow", nil),
		"other issuer": "Bearer " + newIssuer(t).token(t, nil),
	} {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, http.StatusUnauthorized, exchange(header).Code)
		})
	}
}

func TestNewWorkload(t *testing.T) {
	svc := acl.NewAuthService(kvtest.GetStore(t.Context(), t), crypt.NewSecretStore([]byte("k")), authparams.ServiceCache{})
	w, err := NewWorkload(config.KAOSWorkloadIdentity{}, svc, nil)
	require.NoError(t, err)
	require.Nil(t, w, "off without an audience")

	// Without an issuer, the cluster's: the issuer of the server's own service-account token.
	is := newIssuer(t)
	path := filepath.Join(t.TempDir(), "token")
	require.NoError(t, os.WriteFile(path, []byte(podToken(t, is, "vision-ml", "vision-lakefs", nil)+"\n"), 0o600))
	defer func(old string) { serviceAccountTokenPath = old }(serviceAccountTokenPath)
	serviceAccountTokenPath = path
	w, err = NewWorkload(config.KAOSWorkloadIdentity{Audience: workloadAudience}, svc, nil)
	require.NoError(t, err)
	require.Equal(t, is.URL, w.cfg.Issuer)

	serviceAccountTokenPath = filepath.Join(t.TempDir(), "missing")
	_, err = NewWorkload(config.KAOSWorkloadIdentity{Audience: workloadAudience}, svc, nil)
	require.Error(t, err)
}
