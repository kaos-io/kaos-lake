// KAOS (kaos-lake, PRD novelcore/kaos#1217 F-04): new file, Apache-2.0.

package tokenid

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/sessions"
	"github.com/stretchr/testify/require"
	"github.com/treeverse/lakefs/pkg/auth"
	"github.com/treeverse/lakefs/pkg/auth/setup"
	"github.com/treeverse/lakefs/pkg/config"
	"github.com/treeverse/lakefs/pkg/logging"
)

const (
	webClient = "lakefs-web-client"
	webSecret = "web-secret"
)

// browserIssuer adds authorize and token endpoints to the fake issuer. The token endpoint checks
// the client secret and the PKCE verifier and returns an ID token for the client with groups.
func browserIssuer(t *testing.T, groups []string) *issuer {
	t.Helper()
	is := newIssuer(t)
	challenges := map[string]string{} // code -> challenge
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"issuer": is.URL, "jwks_uri": is.URL + "/keys",
			"authorization_endpoint": is.URL + "/authorize", "token_endpoint": is.URL + "/token"})
	})
	mux.Handle("/keys", is.Config.Handler)
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("client_id") != webClient || q.Get("code_challenge_method") != "S256" || q.Get("response_type") != "code" {
			http.Error(w, "bad authorize request", http.StatusBadRequest)
			return
		}
		challenges["code-1"] = q.Get("code_challenge")
		http.Redirect(w, r, q.Get("redirect_uri")+"?code=code-1&state="+url.QueryEscape(q.Get("state")), http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		id, secret, _ := r.BasicAuth()
		sum := sha256.Sum256([]byte(r.FormValue("code_verifier")))
		if id != webClient || secret != webSecret || challenges[r.FormValue("code")] != base64.RawURLEncoding.EncodeToString(sum[:]) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		idToken := is.token(t, func(c jwt.MapClaims) { c["aud"] = webClient; c["groups"] = groups })
		_ = json.NewEncoder(w).Encode(map[string]string{"id_token": idToken, "access_token": "opaque", "token_type": "Bearer"})
	})
	is.Config.Handler = mux
	return is
}

func newBrowserLogin(t *testing.T, is *issuer) *BrowserLogin {
	t.Helper()
	id, _ := newIdentity(t, is)
	active = id
	t.Cleanup(func() { active = nil })
	store := sessions.NewCookieStore([]byte("shared-secret"))
	b := NewBrowserLogin(config.KAOSBrowserLogin{ClientID: webClient, ClientSecret: webSecret, RedirectURL: "https://lakefs.example/oidc/callback"},
		store, []byte("shared-secret"), time.Hour, logging.ContextUnavailable())
	require.NotNil(t, b)
	return b
}

// signIn runs /oidc/login -> issuer authorize -> /oidc/callback like a browser and returns the
// callback response.
func signIn(t *testing.T, b *BrowserLogin, next string, tamperState bool) *httptest.ResponseRecorder {
	t.Helper()
	login := httptest.NewRecorder()
	b.Login(login, httptest.NewRequest(http.MethodGet, "/oidc/login?next="+url.QueryEscape(next), nil))
	require.Equal(t, http.StatusFound, login.Code)
	authorize, err := url.Parse(login.Header().Get("Location"))
	require.NoError(t, err)

	resp, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Get(authorize.String())
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusFound, resp.StatusCode)
	back, err := url.Parse(resp.Header.Get("Location"))
	require.NoError(t, err)
	q := back.Query()
	if tamperState {
		q.Set("state", "forged")
	}

	cb := httptest.NewRequest(http.MethodGet, "/oidc/callback?"+q.Encode(), nil)
	for _, c := range login.Result().Cookies() {
		cb.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	b.Callback(rec, cb)
	return rec
}

func TestBrowserLogin(t *testing.T) {
	is := browserIssuer(t, []string{role + "dev"})
	b := newBrowserLogin(t, is)

	rec := signIn(t, b, "/repositories/data", false)
	require.Equal(t, http.StatusFound, rec.Code, rec.Body.String())
	require.Equal(t, "/repositories/data", rec.Header().Get("Location"))
	var session *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.InternalAuthSessionName {
			session = c
		}
	}
	require.NotNil(t, session, "a lakeFS session is started")
	require.Equal(t, []string{setup.DevelopersGroup}, groupsOf(t, b.id.svc, "alice@acme.com"))

	// The session cookie carries a lakeFS login token for alice, like /auth/login's.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(session)
	s, err := b.sessions.Get(req, auth.InternalAuthSessionName)
	require.NoError(t, err)
	claims, err := auth.VerifyToken([]byte("shared-secret"), s.Values[auth.TokenSessionKeyName].(string))
	require.NoError(t, err)
	require.Equal(t, "alice@acme.com", claims.Subject)
}

func TestBrowserLogin_Refused(t *testing.T) {
	t.Run("forged state", func(t *testing.T) {
		rec := signIn(t, newBrowserLogin(t, browserIssuer(t, []string{role + "dev"})), "/", true)
		require.Equal(t, http.StatusUnauthorized, rec.Code)
		for _, c := range rec.Result().Cookies() {
			require.NotEqual(t, auth.InternalAuthSessionName, c.Name, "no session")
			require.Equal(t, loginSessionName, c.Name)
			require.Negative(t, c.MaxAge, "the login state is single use")
		}
	})
	t.Run("no role in this project", func(t *testing.T) {
		rec := signIn(t, newBrowserLogin(t, browserIssuer(t, []string{"kubeorg:acme"})), "/", false)
		require.Equal(t, http.StatusForbidden, rec.Code)
		require.Contains(t, rec.Body.String(), "no role in this project")
		for _, c := range rec.Result().Cookies() {
			require.NotEqual(t, auth.InternalAuthSessionName, c.Name)
		}
	})
}

func TestSafeNext(t *testing.T) {
	for in, want := range map[string]string{
		"/repositories/x": "/repositories/x", "": "/", "//evil.example": "/", "/\\evil.example": "/",
		"https://evil.example": "/", "repositories": "/",
	} {
		require.Equal(t, want, safeNext(in), in)
	}
}

// The browser client's ID token is accepted only by the browser login, never as a bearer token.
func TestBrowserAudienceNotABearer(t *testing.T) {
	is := browserIssuer(t, []string{role + "dev"})
	id, _ := newIdentity(t, is)
	idToken := is.token(t, func(c jwt.MapClaims) { c["aud"] = webClient })
	_, err := id.Resolve(t.Context(), idToken)
	require.ErrorIs(t, err, auth.ErrAuthenticatingRequest)
	_, err = id.resolve(t.Context(), idToken, webClient)
	require.NoError(t, err)
}
