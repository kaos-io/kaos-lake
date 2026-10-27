// KAOS (kaos-lake, PRD novelcore/kaos#1217 F-03): new file, Apache-2.0.

package tokenid

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
	"github.com/treeverse/lakefs/contrib/auth/acl"
	"github.com/treeverse/lakefs/pkg/auth"
	"github.com/treeverse/lakefs/pkg/auth/crypt"
	"github.com/treeverse/lakefs/pkg/auth/model"
	authparams "github.com/treeverse/lakefs/pkg/auth/params"
	"github.com/treeverse/lakefs/pkg/auth/setup"
	"github.com/treeverse/lakefs/pkg/config"
	"github.com/treeverse/lakefs/pkg/kv/kvtest"
	"github.com/treeverse/lakefs/pkg/permissions"
)

const (
	audience = "312345678901234567"
	role     = "kubeorg:acme:kubeproject:vision:"
)

// issuer is a fake identity provider: discovery, JWKS, and tokens signed with its current key.
type issuer struct {
	*httptest.Server
	key   *rsa.PrivateKey
	kid   string
	fetch atomic.Int32
}

func newIssuer(t *testing.T) *issuer {
	t.Helper()
	is := &issuer{kid: "k1"}
	is.rotate(t, "k1")
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"issuer": is.URL, "jwks_uri": is.URL + "/keys"})
	})
	mux.HandleFunc("/keys", func(w http.ResponseWriter, _ *http.Request) {
		is.fetch.Add(1)
		pub := is.key.PublicKey
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "use": "sig", "kid": is.kid, "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
		}}})
	})
	is.Server = httptest.NewServer(mux)
	t.Cleanup(is.Close)
	return is
}

func (is *issuer) rotate(t *testing.T, kid string) {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	is.key, is.kid = k, kid
}

// token signs claims with the issuer's key; defaults make a valid editor token for alice.
func (is *issuer) token(t *testing.T, edit func(jwt.MapClaims)) string {
	t.Helper()
	claims := jwt.MapClaims{
		"iss":    is.URL,
		"aud":    []string{audience, "other-client"},
		"sub":    "283749",
		"email":  "alice@acme.com",
		"exp":    time.Now().Add(time.Hour).Unix(),
		"iat":    time.Now().Unix(),
		"groups": []string{"kubeorg:acme", role + "dev", "kubeproject:vision:dev"},
	}
	if edit != nil {
		edit(claims)
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = is.kid
	s, err := tok.SignedString(is.key)
	require.NoError(t, err)
	return s
}

func newIdentity(t *testing.T, is *issuer) (*Identity, auth.Service) {
	t.Helper()
	ctx := t.Context()
	svc := acl.NewAuthService(kvtest.GetStore(ctx, t), crypt.NewSecretStore([]byte("k")), authparams.ServiceCache{})
	require.NoError(t, setup.CreateRBACBaseGroups(ctx, svc, time.Now()))
	cfg := config.KAOSTokenIdentity{Issuer: is.URL, Audiences: []string{audience}}
	for claim, group := range map[string]string{
		role + "business": setup.ViewersGroup, role + "dev": setup.DevelopersGroup,
		role + "ops": setup.DevelopersGroup, role + "admin": setup.AdminsGroup, "kubeorg:acme:admin": setup.AdminsGroup,
	} {
		cfg.Groups = append(cfg.Groups, struct {
			Claim string `mapstructure:"claim"`
			Group string `mapstructure:"group"`
		}{claim, group})
	}
	id, err := New(cfg, svc)
	require.NoError(t, err)
	id.keys.minRefresh = 0
	return id, svc
}

func groupsOf(t *testing.T, svc auth.Service, user string) []string {
	t.Helper()
	gs, _, err := svc.ListUserGroups(t.Context(), user, &model.PaginationParams{Amount: -1})
	require.NoError(t, err)
	var names []string
	for _, g := range gs {
		names = append(names, g.DisplayName)
	}
	return names
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

func TestResolve(t *testing.T) {
	ctx := t.Context()
	is := newIssuer(t)
	id, svc := newIdentity(t, is)

	tok := is.token(t, nil)
	require.True(t, id.Owns(tok))
	u, err := id.Resolve(ctx, tok)
	require.NoError(t, err)
	require.Equal(t, "alice@acme.com", u.Username)
	require.Equal(t, []string{setup.DevelopersGroup}, groupsOf(t, svc, u.Username))
	require.True(t, allowed(t, svc, u.Username, permissions.WriteObjectAction, permissions.ObjectArn("data", "x")))

	// The team's role changes to business: the next token makes alice a viewer only.
	viewer := is.token(t, func(c jwt.MapClaims) { c["groups"] = []string{role + "business"}; c["iat"] = time.Now().Unix() + 1 })
	_, err = id.Resolve(ctx, viewer)
	require.NoError(t, err)
	require.Equal(t, []string{setup.ViewersGroup}, groupsOf(t, svc, "alice@acme.com"))
	require.True(t, allowed(t, svc, "alice@acme.com", permissions.ReadObjectAction, permissions.ObjectArn("data", "x")))
	require.False(t, allowed(t, svc, "alice@acme.com", permissions.WriteObjectAction, permissions.ObjectArn("data", "x")))

	// Org admin and project admin map to the same group once.
	admin := is.token(t, func(c jwt.MapClaims) { c["groups"] = []string{"kubeorg:acme:admin", role + "admin"} })
	_, err = id.Resolve(ctx, admin)
	require.NoError(t, err)
	require.Equal(t, []string{setup.AdminsGroup}, groupsOf(t, svc, "alice@acme.com"))
}

func TestResolve_Denied(t *testing.T) {
	is := newIssuer(t)
	other := newIssuer(t)
	for name, tok := range map[string]func() string{
		"other issuer":         func() string { return other.token(t, func(c jwt.MapClaims) { c["iss"] = is.URL }) },
		"issuer claim differs": func() string { return is.token(t, func(c jwt.MapClaims) { c["iss"] = "https://evil.example" }) },
		"wrong audience":       func() string { return is.token(t, func(c jwt.MapClaims) { c["aud"] = "someone-else" }) },
		"expired": func() string {
			return is.token(t, func(c jwt.MapClaims) { c["exp"] = time.Now().Add(-time.Minute).Unix() })
		},
		"no expiry": func() string { return is.token(t, func(c jwt.MapClaims) { delete(c, "exp") }) },
		"no role here": func() string {
			return is.token(t, func(c jwt.MapClaims) {
				c["groups"] = []string{"kubeorg:acme", "kubeorg:other:kubeproject:vision:admin"}
			})
		},
		"no groups":           func() string { return is.token(t, func(c jwt.MapClaims) { delete(c, "groups") }) },
		"platform user name":  func() string { return is.token(t, func(c jwt.MapClaims) { c["email"] = "admin" }) },
		"app user name":       func() string { return is.token(t, func(c jwt.MapClaims) { c["email"] = "kaos-app-yolo" }) },
		"unknown signing key": func() string { tok := is.token(t, nil); is.rotate(t, "k1"); return tok },
		"tampered": func() string {
			tok := is.token(t, nil)
			return tok[:len(tok)-4] + "AAAA"
		},
		"HS256 with the public key": func() string {
			tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"iss": is.URL, "aud": audience, "email": "alice@acme.com",
				"exp": time.Now().Add(time.Hour).Unix(), "groups": []string{role + "admin"}})
			s, err := tok.SignedString(is.key.PublicKey.N.Bytes())
			require.NoError(t, err)
			return s
		},
	} {
		t.Run(name, func(t *testing.T) {
			id, svc := newIdentity(t, is)
			_, err := id.Resolve(t.Context(), tok())
			require.ErrorIs(t, err, auth.ErrAuthenticatingRequest)
			_, err = svc.GetUser(t.Context(), "alice@acme.com")
			require.ErrorIs(t, err, auth.ErrNotFound, "nothing created for a denied token")
		})
	}
}

// The issuer is unreachable: tokens are denied, never let through.
func TestResolve_IssuerDown(t *testing.T) {
	is := newIssuer(t)
	tok := is.token(t, nil)
	id, _ := newIdentity(t, is)
	is.Close()
	_, err := id.Resolve(t.Context(), tok)
	require.ErrorIs(t, err, auth.ErrAuthenticatingRequest)
}

// A new signing key is picked up when a token names it.
func TestResolve_KeyRotation(t *testing.T) {
	is := newIssuer(t)
	id, _ := newIdentity(t, is)
	_, err := id.Resolve(t.Context(), is.token(t, nil))
	require.NoError(t, err)
	is.rotate(t, "k2")
	_, err = id.Resolve(t.Context(), is.token(t, func(c jwt.MapClaims) { c["iat"] = time.Now().Unix() + 2 }))
	require.NoError(t, err)
	require.Equal(t, int32(2), is.fetch.Load())
}

// A verified token is reused until the cache entry or the token expires, without asking the issuer.
func TestResolve_Cache(t *testing.T) {
	is := newIssuer(t)
	id, _ := newIdentity(t, is)
	tok := is.token(t, func(c jwt.MapClaims) { c["exp"] = time.Now().Add(30 * time.Second).Unix() })
	for range 3 {
		_, err := id.Resolve(t.Context(), tok)
		require.NoError(t, err)
	}
	require.Equal(t, int32(1), is.fetch.Load())
	id.now = func() time.Time { return time.Now().Add(31 * time.Second) }
	_, err := id.Resolve(t.Context(), tok)
	require.ErrorIs(t, err, auth.ErrAuthenticatingRequest, "expired token is not served from the cache")
}

func TestNew(t *testing.T) {
	id, err := New(config.KAOSTokenIdentity{}, nil)
	require.NoError(t, err)
	require.Nil(t, id, "no issuer: off")
	_, err = New(config.KAOSTokenIdentity{Issuer: "https://z"}, nil)
	require.Error(t, err, "issuer without audiences or groups")
}

func TestOwns(t *testing.T) {
	is := newIssuer(t)
	id, _ := newIdentity(t, is)
	require.False(t, id.Owns("not-a-jwt"))
	lakeFSToken, err := auth.GenerateJWTLogin([]byte("secret"), "admin", time.Now(), time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.False(t, id.Owns(lakeFSToken), "lakeFS's own login tokens keep the upstream path")
}
