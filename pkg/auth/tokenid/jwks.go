// KAOS (kaos-lake, PRD novelcore/kaos#1217 F-03): new file, Apache-2.0.

package tokenid

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

// keySet holds the issuer's RSA signing keys, found through OIDC discovery. Keys are fetched on
// first use, again when a token names a key the set lacks (key rotation), and at most once per
// minRefresh, so unknown key IDs cannot be used to flood the issuer.
type keySet struct {
	issuer     string
	client     *http.Client
	minRefresh time.Duration

	mu      sync.Mutex
	keys    map[string]*rsa.PublicKey
	fetched time.Time
}

func newKeySet(issuer string, client *http.Client) *keySet {
	return &keySet{issuer: strings.TrimSuffix(issuer, "/"), client: client, minRefresh: time.Minute}
}

// key returns the key with id kid. An error means the token cannot be verified: callers deny.
func (s *keySet) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if k, ok := s.keys[kid]; ok {
		return k, nil
	}
	if !s.fetched.IsZero() && time.Since(s.fetched) < s.minRefresh {
		return nil, fmt.Errorf("unknown signing key %q", kid)
	}
	keys, err := s.fetch(ctx)
	s.fetched = time.Now()
	if err != nil {
		return nil, fmt.Errorf("fetch signing keys of %s: %w", s.issuer, err)
	}
	s.keys = keys
	if k, ok := keys[kid]; ok {
		return k, nil
	}
	return nil, fmt.Errorf("unknown signing key %q", kid)
}

func (s *keySet) fetch(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	var discovery struct {
		Issuer  string `json:"issuer"`
		JWKSURI string `json:"jwks_uri"`
	}
	if err := s.getJSON(ctx, s.issuer+"/.well-known/openid-configuration", &discovery); err != nil {
		return nil, err
	}
	if strings.TrimSuffix(discovery.Issuer, "/") != s.issuer || discovery.JWKSURI == "" {
		return nil, fmt.Errorf("discovery document names issuer %q, jwks_uri %q", discovery.Issuer, discovery.JWKSURI)
	}
	var jwks struct {
		Keys []struct {
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			Use string `json:"use"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := s.getJSON(ctx, discovery.JWKSURI, &jwks); err != nil {
		return nil, err
	}
	keys := map[string]*rsa.PublicKey{}
	for _, k := range jwks.Keys {
		if k.Kty != "RSA" || (k.Use != "" && k.Use != "sig") || k.Kid == "" {
			continue
		}
		n, errN := base64.RawURLEncoding.DecodeString(k.N)
		e, errE := base64.RawURLEncoding.DecodeString(k.E)
		if errN != nil || errE != nil || len(e) == 0 || len(e) > 4 {
			continue
		}
		keys[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
	}
	if len(keys) == 0 {
		return nil, errors.New("no RSA signing keys")
	}
	return keys, nil
}

func (s *keySet) getJSON(ctx context.Context, url string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 1<<20)).Decode(v)
}
