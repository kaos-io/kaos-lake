// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-12): new file, Apache-2.0.

package sharing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	// JWTBearerGrant is the OAuth grant of a signed assertion (RFC 7523), Zitadel's JWT profile.
	JWTBearerGrant = "urn:ietf:params:oauth:grant-type:jwt-bearer"
	// assertionTTL is how long a minted assertion is valid; it is used at once.
	assertionTTL = 5 * time.Minute
)

// MachineKey is a Zitadel machine user's key file.
type MachineKey struct {
	Type   string `json:"type"`
	KeyID  string `json:"keyId"`
	Key    string `json:"key"` // PEM, PKCS#1 or PKCS#8
	UserID string `json:"userId"`
}

// Minter mints this project's machine user's access token with its Zitadel machine key: an
// RS256 assertion (iss = sub = the key's userId, aud = the issuer, header kid = keyId) traded at
// {Issuer}/oauth/v2/token. The token is reused until half its lifetime has passed. The key file
// is read again for every mint, so a rotated key is picked up.
type Minter struct {
	KeyPath string
	// Issuer is the identity provider (kaos.token_identity.issuer).
	Issuer string
	// ProjectID is the organisation's Zitadel project, the audience peers accept
	// (kaos.token_identity.audiences[0]).
	ProjectID string
	Client    *http.Client
	Now       func() time.Time

	mu    sync.Mutex
	token string
	until time.Time
}

// Scope is what the token asks for: the caller's project roles, audienced to the org's project.
func (m *Minter) Scope() string {
	return "openid email profile urn:zitadel:iam:org:projects:roles urn:zitadel:iam:org:project:id:" + m.ProjectID + ":aud"
}

func (m *Minter) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

// Token returns an access token of the machine user, minting one when the last is past half its
// lifetime.
func (m *Minter) Token(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.token != "" && m.now().Before(m.until) {
		return m.token, nil
	}
	token, lifetime, err := m.mint(ctx)
	if err != nil {
		return "", err
	}
	m.token, m.until = token, m.now().Add(lifetime/2)
	return token, nil
}

// ReadMachineKey reads and checks a machine key file.
func ReadMachineKey(path string) (*MachineKey, error) {
	if path == "" {
		return nil, errors.New("kaos.sharing.machine_key_path is not set: no machine key to call peers with")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("machine key %s cannot be read (is the project's machine key Secret mounted?): %w", path, err)
	}
	var k MachineKey
	if err := json.Unmarshal(raw, &k); err != nil {
		return nil, fmt.Errorf("machine key %s is not a Zitadel key file: %w", path, err)
	}
	if k.KeyID == "" || k.UserID == "" || k.Key == "" {
		return nil, fmt.Errorf("machine key %s lacks keyId, userId or key", path)
	}
	return &k, nil
}

// Assertion signs the JWT-profile assertion of key for issuer at now.
func Assertion(key *MachineKey, issuer string, now time.Time) (string, error) {
	private, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(key.Key))
	if err != nil {
		return "", fmt.Errorf("machine key %s: %w", key.KeyID, err)
	}
	t := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": key.UserID,
		"sub": key.UserID,
		"aud": issuer,
		"iat": now.Unix(),
		"exp": now.Add(assertionTTL).Unix(),
	})
	t.Header["kid"] = key.KeyID
	return t.SignedString(private)
}

func (m *Minter) mint(ctx context.Context) (string, time.Duration, error) {
	key, err := ReadMachineKey(m.KeyPath)
	if err != nil {
		return "", 0, err
	}
	issuer := strings.TrimSuffix(m.Issuer, "/")
	assertion, err := Assertion(key, issuer, m.now())
	if err != nil {
		return "", 0, err
	}
	form := url.Values{"grant_type": {JWTBearerGrant}, "scope": {m.Scope()}, "assertion": {assertion}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, issuer+"/oauth/v2/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	client := m.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("mint the machine token: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", 0, fmt.Errorf("mint the machine token: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("mint the machine token: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tok); err != nil || tok.AccessToken == "" {
		return "", 0, fmt.Errorf("mint the machine token: no access_token in the answer (%v)", err)
	}
	return tok.AccessToken, time.Duration(tok.ExpiresIn) * time.Second, nil
}
