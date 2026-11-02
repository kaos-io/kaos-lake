// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-12): new file, Apache-2.0.

package sharing_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
	"github.com/treeverse/lakefs/pkg/kaos/sharing"
)

const (
	orgProjectID = "312345678901234567"
	machineUser  = "289000000000000001"
)

// zitadel is a fake Zitadel: the JWT-profile token endpoint, which checks the assertion against
// the machine keys it knows, plus discovery and JWKS of the key it signs access tokens with, so
// pkg/auth/tokenid can verify them.
type zitadel struct {
	*httptest.Server
	t       *testing.T
	signing *rsa.PrivateKey

	mu        sync.Mutex
	machine   map[string]*rsa.PublicKey // keyId → machine key
	email     string
	groups    []string
	expiresIn int
	status    int // non-zero: the token endpoint fails with it
	form      url.Values
	header    map[string]any
	claims    jwt.MapClaims
	mints     atomic.Int32
}

func newZitadel(t *testing.T) *zitadel {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	z := &zitadel{t: t, signing: k, machine: map[string]*rsa.PublicKey{}, email: "vision-ml@acme.zitadel.cloud",
		groups: []string{"kubeproject:vision:admin"}, expiresIn: 3600}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"issuer": z.URL, "jwks_uri": z.URL + "/oauth/v2/keys"})
	})
	mux.HandleFunc("/oauth/v2/keys", func(w http.ResponseWriter, _ *http.Request) {
		pub := z.signing.PublicKey
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "use": "sig", "kid": "zitadel-1", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
		}}})
	})
	mux.HandleFunc("/oauth/v2/token", z.token)
	z.Server = httptest.NewServer(mux)
	t.Cleanup(z.Close)
	return z
}

func (z *zitadel) token(w http.ResponseWriter, r *http.Request) {
	z.mints.Add(1)
	z.mu.Lock()
	defer z.mu.Unlock()
	if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
		http.Error(w, "want a form POST", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	z.form = r.PostForm
	if z.status != 0 {
		w.WriteHeader(z.status)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"key expired"}`))
		return
	}
	claims := jwt.MapClaims{}
	parsed, err := jwt.ParseWithClaims(r.PostForm.Get("assertion"), claims, func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		if k, ok := z.machine[kid]; ok {
			return k, nil
		}
		return nil, jwt.ErrTokenUnverifiable
	}, jwt.WithValidMethods([]string{"RS256"}), jwt.WithAudience(z.URL), jwt.WithExpirationRequired())
	if err != nil || r.PostForm.Get("grant_type") != sharing.JWTBearerGrant {
		http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
		return
	}
	z.header, z.claims = parsed.Header, claims
	sub, _ := claims.GetSubject()
	access := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": z.URL, "aud": []string{orgProjectID, "client-1"}, "sub": sub, "email": z.email, "groups": z.groups,
		"exp": time.Now().Add(time.Duration(z.expiresIn) * time.Second).Unix(), "iat": time.Now().Unix(),
	})
	access.Header["kid"] = "zitadel-1"
	signed, err := access.SignedString(z.signing)
	require.NoError(z.t, err)
	_ = json.NewEncoder(w).Encode(map[string]any{"access_token": signed, "token_type": "Bearer", "expires_in": z.expiresIn})
}

// machineKey writes a Zitadel machine key file for keyID, known to z, and returns its path.
func (z *zitadel) machineKey(t *testing.T, dir, keyID string) string {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	z.mu.Lock()
	z.machine[keyID] = &k.PublicKey
	z.mu.Unlock()
	pemKey := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)})
	raw, err := json.Marshal(sharing.MachineKey{Type: "serviceaccount", KeyID: keyID, Key: string(pemKey), UserID: machineUser})
	require.NoError(t, err)
	path := filepath.Join(dir, "ZITADEL_MACHINE_KEY")
	require.NoError(t, os.WriteFile(path, raw, 0o600))
	return path
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *clock { return &clock{t: time.Now()} }

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }

func (c *clock) Add(d time.Duration) { c.mu.Lock(); defer c.mu.Unlock(); c.t = c.t.Add(d) }

// The assertion is Zitadel's JWT profile: RS256, kid = keyId, iss = sub = userId, aud = issuer;
// the token asks for the caller's project roles audienced to the organisation's project.
func TestMinter_Assertion(t *testing.T) {
	z := newZitadel(t)
	m := &sharing.Minter{KeyPath: z.machineKey(t, t.TempDir(), "key-1"), Issuer: z.URL + "/", ProjectID: orgProjectID}
	token, err := m.Token(t.Context())
	require.NoError(t, err)
	require.NotEmpty(t, token)

	require.Equal(t, sharing.JWTBearerGrant, z.form.Get("grant_type"))
	require.Equal(t, "openid email profile urn:zitadel:iam:org:projects:roles urn:zitadel:iam:org:project:id:"+orgProjectID+":aud",
		z.form.Get("scope"))
	require.Equal(t, "RS256", z.header["alg"])
	require.Equal(t, "key-1", z.header["kid"])
	require.Equal(t, machineUser, z.claims["iss"])
	require.Equal(t, machineUser, z.claims["sub"])
	require.Equal(t, z.URL, z.claims["aud"], "the issuer, without the trailing slash")
	iat, _ := z.claims.GetIssuedAt()
	exp, _ := z.claims.GetExpirationTime()
	require.Equal(t, 5*time.Minute, exp.Sub(iat.Time))
}

// A token is reused until half its lifetime has passed.
func TestMinter_CachesUntilHalfLifetime(t *testing.T) {
	z := newZitadel(t)
	c := newClock()
	m := &sharing.Minter{KeyPath: z.machineKey(t, t.TempDir(), "key-1"), Issuer: z.URL, ProjectID: orgProjectID, Now: c.Now}
	first, err := m.Token(t.Context())
	require.NoError(t, err)
	c.Add(30*time.Minute - time.Second)
	again, err := m.Token(t.Context())
	require.NoError(t, err)
	require.Equal(t, first, again)
	require.EqualValues(t, 1, z.mints.Load())
	c.Add(2 * time.Second)
	_, err = m.Token(t.Context())
	require.NoError(t, err)
	require.EqualValues(t, 2, z.mints.Load(), "minted again past half of the 1h lifetime")
}

// The key file is read again for every mint: a rotated key is used at the next one.
func TestMinter_RereadsKeyFile(t *testing.T) {
	z := newZitadel(t)
	c := newClock()
	dir := t.TempDir()
	m := &sharing.Minter{KeyPath: z.machineKey(t, dir, "key-1"), Issuer: z.URL, ProjectID: orgProjectID, Now: c.Now}
	_, err := m.Token(t.Context())
	require.NoError(t, err)
	require.Equal(t, "key-1", z.header["kid"])

	z.machineKey(t, dir, "key-2") // rotated in place, as a Secret update does
	c.Add(31 * time.Minute)
	_, err = m.Token(t.Context())
	require.NoError(t, err)
	require.Equal(t, "key-2", z.header["kid"])

	// While a token is valid the file is not needed; once it must mint, a missing file is an error.
	require.NoError(t, os.Remove(m.KeyPath))
	_, err = m.Token(t.Context())
	require.NoError(t, err)
	c.Add(31 * time.Minute)
	_, err = m.Token(t.Context())
	require.ErrorContains(t, err, "cannot be read")
}

func TestMinter_KeyFileErrors(t *testing.T) {
	z := newZitadel(t)
	dir := t.TempDir()
	missing := filepath.Join(dir, "absent")
	_, err := (&sharing.Minter{KeyPath: missing, Issuer: z.URL, ProjectID: orgProjectID}).Token(t.Context())
	require.ErrorContains(t, err, missing)
	require.ErrorContains(t, err, "cannot be read (is the project's machine key Secret mounted?)")

	_, err = (&sharing.Minter{Issuer: z.URL, ProjectID: orgProjectID}).Token(t.Context())
	require.ErrorContains(t, err, "kaos.sharing.machine_key_path is not set")

	bad := filepath.Join(dir, "bad")
	write(t, bad, "not json")
	_, err = (&sharing.Minter{KeyPath: bad, Issuer: z.URL}).Token(t.Context())
	require.ErrorContains(t, err, "not a Zitadel key file")
	write(t, bad, `{"type":"serviceaccount","keyId":"k"}`)
	_, err = (&sharing.Minter{KeyPath: bad, Issuer: z.URL}).Token(t.Context())
	require.ErrorContains(t, err, "lacks keyId, userId or key")
	write(t, bad, `{"type":"serviceaccount","keyId":"k","userId":"u","key":"not pem"}`)
	_, err = (&sharing.Minter{KeyPath: bad, Issuer: z.URL}).Token(t.Context())
	require.Error(t, err)
	require.Zero(t, z.mints.Load(), "nothing is sent without a usable key")
}

// A refused mint is an error naming Zitadel's answer, and nothing is cached.
func TestMinter_TokenEndpointError(t *testing.T) {
	z := newZitadel(t)
	m := &sharing.Minter{KeyPath: z.machineKey(t, t.TempDir(), "key-1"), Issuer: z.URL, ProjectID: orgProjectID}
	z.status = http.StatusBadRequest
	_, err := m.Token(t.Context())
	require.ErrorContains(t, err, "400")
	require.ErrorContains(t, err, "invalid_grant")
	z.mu.Lock()
	z.status = 0
	z.mu.Unlock()
	_, err = m.Token(t.Context())
	require.NoError(t, err)
	require.EqualValues(t, 2, z.mints.Load())
}

// A PKCS#8 key works as well as Zitadel's PKCS#1.
func TestAssertion_PKCS8(t *testing.T) {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(k)
	require.NoError(t, err)
	key := &sharing.MachineKey{KeyID: "k8", UserID: "u", Key: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))}
	signed, err := sharing.Assertion(key, "https://zitadel.example", time.Now())
	require.NoError(t, err)
	_, err = jwt.Parse(signed, func(*jwt.Token) (any, error) { return &k.PublicKey, nil }, jwt.WithAudience("https://zitadel.example"))
	require.NoError(t, err)
}
