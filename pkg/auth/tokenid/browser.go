// KAOS (kaos-lake, PRD novelcore/kaos#1217 F-04): new file, Apache-2.0.

package tokenid

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/sessions"
	"github.com/treeverse/lakefs/pkg/auth"
	"github.com/treeverse/lakefs/pkg/config"
	"github.com/treeverse/lakefs/pkg/logging"
)

// loginSessionName holds a login in progress: state, PKCE verifier and where to go after.
const loginSessionName = "kaos_oidc_login"

// BrowserLogin signs people into the lakeFS UI with the identity provider: authorization code with
// PKCE, then the same lakeFS session /auth/login creates. The ID token it receives goes through
// the same verification and group mapping as a bearer token, so the browser user gets exactly the
// groups their project role maps to.
type BrowserLogin struct {
	id            *Identity
	cfg           config.KAOSBrowserLogin
	sessions      sessions.Store
	secret        []byte
	loginDuration time.Duration
	logger        logging.Logger
}

// NewBrowserLogin returns the browser login, or nil when it is not configured or token identity
// is off.
func NewBrowserLogin(cfg config.KAOSBrowserLogin, store sessions.Store, secret []byte, loginDuration time.Duration, logger logging.Logger) *BrowserLogin {
	id := Active()
	if id == nil || cfg.ClientID == "" {
		return nil
	}
	if len(cfg.Scopes) == 0 {
		cfg.Scopes = []string{"openid", "profile", "email"}
	}
	return &BrowserLogin{id: id, cfg: cfg, sessions: store, secret: secret, loginDuration: loginDuration, logger: logger}
}

// Login starts the authorization code flow.
func (b *BrowserLogin) Login(w http.ResponseWriter, r *http.Request) {
	authorizationEndpoint, _, err := b.id.keys.endpoints(r.Context())
	if err != nil || authorizationEndpoint == "" {
		b.fail(w, r, http.StatusBadGateway, "The sign-in service is not reachable. Try again in a minute.", err)
		return
	}
	state, verifier := random(), random()
	s, _ := b.sessions.Get(r, loginSessionName)
	s.Values["state"], s.Values["verifier"], s.Values["next"] = state, verifier, safeNext(r.URL.Query().Get("next"))
	s.Options = &sessions.Options{Path: "/oidc", MaxAge: 600, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"}
	if err := s.Save(r, w); err != nil {
		b.fail(w, r, http.StatusInternalServerError, "Could not start the sign-in.", err)
		return
	}
	challenge := sha256.Sum256([]byte(verifier))
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {b.cfg.ClientID},
		"redirect_uri":          {b.cfg.RedirectURL},
		"scope":                 {strings.Join(b.cfg.Scopes, " ")},
		"state":                 {state},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(challenge[:])},
		"code_challenge_method": {"S256"},
	}
	http.Redirect(w, r, authorizationEndpoint+"?"+q.Encode(), http.StatusFound)
}

// Callback finishes the flow and starts a lakeFS session for the caller.
func (b *BrowserLogin) Callback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	s, _ := b.sessions.Get(r, loginSessionName)
	state, _ := s.Values["state"].(string)
	verifier, _ := s.Values["verifier"].(string)
	next, _ := s.Values["next"].(string)
	s.Options = &sessions.Options{Path: "/oidc", MaxAge: -1}
	_ = s.Save(r, w) // one use only
	if e := r.URL.Query().Get("error"); e != "" {
		b.fail(w, r, http.StatusUnauthorized, "Sign-in was cancelled or refused.", errors.New(e))
		return
	}
	got := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")
	if state == "" || code == "" || subtle.ConstantTimeCompare([]byte(state), []byte(got)) != 1 {
		b.fail(w, r, http.StatusUnauthorized, "This sign-in link expired or was already used. Start again.", errors.New("state mismatch"))
		return
	}

	idToken, err := b.exchange(r, code, verifier)
	if err != nil {
		b.fail(w, r, http.StatusUnauthorized, "Sign-in failed.", err)
		return
	}
	// The ID token, audienced to this client, carries the email and groups claims (the web app
	// asserts userinfo and roles into it); its access token may be opaque.
	user, err := b.id.resolve(ctx, idToken, b.cfg.ClientID)
	if errors.Is(err, ErrDenied) {
		b.fail(w, r, http.StatusForbidden, "Your account has no role in this project's lakeFS. Ask a project admin to add you to one of its teams.", err)
		return
	}
	if err != nil {
		b.fail(w, r, http.StatusInternalServerError, "Sign-in failed.", err)
		return
	}

	now := time.Now()
	token, err := auth.GenerateJWTLogin(b.secret, user.Username, now, now.Add(b.loginDuration))
	if err != nil {
		b.fail(w, r, http.StatusInternalServerError, "Sign-in failed.", err)
		return
	}
	session, _ := b.sessions.Get(r, auth.InternalAuthSessionName)
	session.Values[auth.TokenSessionKeyName] = token
	if err := b.sessions.Save(r, w, session); err != nil {
		b.fail(w, r, http.StatusInternalServerError, "Sign-in failed.", err)
		return
	}
	http.Redirect(w, r, next, http.StatusFound)
}

func (b *BrowserLogin) exchange(r *http.Request, code, verifier string) (string, error) {
	_, tokenEndpoint, err := b.id.keys.endpoints(r.Context())
	if err != nil {
		return "", err
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {b.cfg.RedirectURL},
		"code_verifier": {verifier},
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(url.QueryEscape(b.cfg.ClientID), url.QueryEscape(string(b.cfg.ClientSecret)))
	resp, err := b.id.keys.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var body struct {
		IDToken string `json:"id_token"`
		Error   string `json:"error"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 1<<20)).Decode(&body); err != nil {
		return "", fmt.Errorf("token response: %w", err)
	}
	if resp.StatusCode != http.StatusOK || body.IDToken == "" {
		return "", fmt.Errorf("token endpoint: %s %s", resp.Status, body.Error)
	}
	return body.IDToken, nil
}

func (b *BrowserLogin) fail(w http.ResponseWriter, r *http.Request, status int, message string, err error) {
	b.logger.WithContext(r.Context()).WithError(err).WithField("status", status).Warn("Browser sign-in failed")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>lakeFS sign-in</title>`+
		`<body style="font-family:system-ui,sans-serif;max-width:34rem;margin:4rem auto;line-height:1.5">`+
		`<h2>Could not sign you in</h2><p>%s</p><p><a href="/oidc/login">Try again</a></p></body>`, htmlEscape(message))
}

// safeNext keeps the post-login redirect on this site.
func safeNext(next string) string {
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") {
		return "/"
	}
	return next
}

func random() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func htmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}
