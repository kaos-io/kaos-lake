// KAOS (kaos-lake, PRD novelcore/kaos#1217 F-03): new file, Apache-2.0.

// Package tokenid lets people and jobs call lakeFS with their own identity-provider token (a
// Zitadel access token) instead of a shared key. A token from the configured issuer is verified
// against the issuer's published keys; the caller becomes a lakeFS user named by the token's
// email, and its groups are set from the token's group claim on every call through a fixed
// mapping, so lakeFS access follows the platform's project roles (PRD D-04). Anything that
// cannot be verified or maps to no group is denied (CON-03).
package tokenid

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/treeverse/lakefs/pkg/auth"
	"github.com/treeverse/lakefs/pkg/auth/model"
	"github.com/treeverse/lakefs/pkg/config"
)

// cacheTTL bounds how long a verified token's user and groups are reused before the store is
// checked again; a token is never reused past its own expiry.
const cacheTTL = time.Minute

var ErrDenied = errors.New("token not accepted")

// Identity verifies tokens of one issuer and keeps their users' groups.
type Identity struct {
	cfg  config.KAOSTokenIdentity
	keys *keySet
	svc  auth.Service
	now  func() time.Time

	mu    sync.Mutex
	cache map[string]cached
}

type cached struct {
	user    *model.User
	expires time.Time
}

// New returns an Identity for cfg, or nil when cfg names no issuer.
func New(cfg config.KAOSTokenIdentity, svc auth.Service) (*Identity, error) {
	if cfg.Issuer == "" {
		return nil, nil
	}
	if len(cfg.Audiences) == 0 || len(cfg.Groups) == 0 {
		return nil, errors.New("kaos.token_identity needs audiences and groups")
	}
	for _, g := range cfg.Groups {
		if g.Claim == "" || g.Group == "" {
			return nil, fmt.Errorf("kaos.token_identity.groups: claim and group are required (%+v)", g)
		}
	}
	if cfg.GroupsClaim == "" {
		cfg.GroupsClaim = "groups"
	}
	return &Identity{
		cfg:   cfg,
		keys:  newKeySet(cfg.Issuer, &http.Client{Timeout: 10 * time.Second}),
		svc:   svc,
		now:   time.Now,
		cache: map[string]cached{},
	}, nil
}

// Owns reports whether token claims to come from this issuer. It does not verify anything.
func (id *Identity) Owns(token string) bool {
	claims := jwt.MapClaims{}
	if _, _, err := jwt.NewParser().ParseUnverified(token, claims); err != nil {
		return false
	}
	iss, _ := claims.GetIssuer()
	return strings.TrimSuffix(iss, "/") == strings.TrimSuffix(id.cfg.Issuer, "/")
}

// Resolve verifies token and returns its user, created on first use, with its groups set from
// the token. Every error denies the request.
func (id *Identity) Resolve(ctx context.Context, token string) (*model.User, error) {
	return id.resolve(ctx, token, "")
}

// resolve is Resolve, also accepting audience extraAudience: the browser login's own client,
// whose ID token it presents.
func (id *Identity) resolve(ctx context.Context, token, extraAudience string) (*model.User, error) {
	now := id.now()
	id.mu.Lock()
	c, ok := id.cache[token]
	id.mu.Unlock()
	if ok && now.Before(c.expires) {
		return c.user, nil
	}

	claims, err := id.verify(ctx, token, extraAudience)
	if err != nil {
		return nil, fmt.Errorf("%w: %w: %w", auth.ErrAuthenticatingRequest, ErrDenied, err)
	}
	username, _ := claims["email"].(string)
	if !strings.Contains(username, "@") {
		// Platform users and app users have no "@", so a token can never act as one of them.
		return nil, fmt.Errorf("%w: %w: email claim %q", auth.ErrAuthenticatingRequest, ErrDenied, username)
	}
	groups := id.groups(claims)
	if len(groups) == 0 {
		return nil, fmt.Errorf("%w: %w: %s has no project role here", auth.ErrAuthenticatingRequest, ErrDenied, username)
	}
	user, err := id.sync(ctx, username, groups)
	if err != nil {
		return nil, err
	}

	expires := now.Add(cacheTTL)
	if exp, err := claims.GetExpirationTime(); err == nil && exp != nil && exp.Before(expires) {
		expires = exp.Time
	}
	id.mu.Lock()
	for k, v := range id.cache { // drop expired entries as we go; the map stays small
		if !now.Before(v.expires) {
			delete(id.cache, k)
		}
	}
	id.cache[token] = cached{user: user, expires: expires}
	id.mu.Unlock()
	return user, nil
}

func (id *Identity) verify(ctx context.Context, token, extraAudience string) (jwt.MapClaims, error) {
	claims := jwt.MapClaims{}
	keyFn := func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		return id.keys.key(ctx, kid)
	}
	_, err := jwt.ParseWithClaims(token, claims, keyFn,
		jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Name}),
		jwt.WithIssuer(id.cfg.Issuer),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(id.now),
	)
	if err != nil {
		return nil, err
	}
	aud, err := claims.GetAudience()
	if err != nil || !slices.ContainsFunc(aud, func(a string) bool {
		return slices.Contains(id.cfg.Audiences, a) || (extraAudience != "" && a == extraAudience)
	}) {
		return nil, fmt.Errorf("audience %v not accepted", aud)
	}
	return claims, nil
}

// groups maps the token's group claim to lakeFS groups, without duplicates.
func (id *Identity) groups(claims jwt.MapClaims) []string {
	values, _ := claims[id.cfg.GroupsClaim].([]any)
	var groups []string
	for _, m := range id.cfg.Groups {
		if slices.Contains(values, any(m.Claim)) && !slices.Contains(groups, m.Group) {
			groups = append(groups, m.Group)
		}
	}
	return groups
}

// sync creates the user if needed and makes its groups exactly groups.
func (id *Identity) sync(ctx context.Context, username string, groups []string) (*model.User, error) {
	user, err := id.svc.GetUser(ctx, username)
	if errors.Is(err, auth.ErrNotFound) {
		if _, err := id.svc.CreateUser(ctx, &model.User{Username: username, CreatedAt: id.now(), Email: &username, Source: "zitadel"}); err != nil && !errors.Is(err, auth.ErrAlreadyExists) {
			return nil, fmt.Errorf("create user %s: %w", username, err)
		}
		user, err = id.svc.GetUser(ctx, username)
	}
	if err != nil {
		return nil, fmt.Errorf("get user %s: %w", username, err)
	}
	current, _, err := id.svc.ListUserGroups(ctx, username, &model.PaginationParams{Amount: -1})
	if err != nil {
		return nil, fmt.Errorf("list groups of %s: %w", username, err)
	}
	for _, g := range current {
		if !slices.Contains(groups, g.DisplayName) {
			if err := id.svc.RemoveUserFromGroup(ctx, username, g.DisplayName); err != nil {
				return nil, fmt.Errorf("remove %s from %s: %w", username, g.DisplayName, err)
			}
		}
	}
	for _, g := range groups {
		if !slices.ContainsFunc(current, func(c *model.Group) bool { return c.DisplayName == g }) {
			if err := id.svc.AddUserToGroup(ctx, username, g); err != nil {
				return nil, fmt.Errorf("add %s to %s: %w", username, g, err)
			}
		}
	}
	return user, nil
}

// active is the server's Identity, set once at start by Configure; nil when off.
var active *Identity

// Configure sets the server's Identity from cfg. Call before serving.
func Configure(cfg config.KAOSTokenIdentity, svc auth.Service) error {
	id, err := New(cfg, svc)
	if err != nil {
		return err
	}
	active = id
	return nil
}

// Active returns the server's Identity, or nil when token identity is off.
func Active() *Identity {
	return active
}
