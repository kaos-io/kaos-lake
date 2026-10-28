// KAOS (kaos-lake, PRD novelcore/kaos#1217 F-05): new file, Apache-2.0.

package tokenid

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/treeverse/lakefs/pkg/auth"
	"github.com/treeverse/lakefs/pkg/auth/declared"
	"github.com/treeverse/lakefs/pkg/auth/keys"
	"github.com/treeverse/lakefs/pkg/auth/model"
	"github.com/treeverse/lakefs/pkg/config"
	"github.com/treeverse/lakefs/pkg/logging"
)

// WorkflowSuffix ends the service account an ML app's workflows run as: {app}-workflow.
const WorkflowSuffix = "-workflow"

// serviceAccountTokenPath is this server's own token, read once for the cluster's issuer.
var serviceAccountTokenPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"

// Workload accepts the Kubernetes service-account tokens of ML workflow pods. A token of service
// account {app}-workflow acts as the declared app user kaos-app-{app} (pkg/auth/declared), with
// that user's groups, when it comes from the namespace declared for that app; any other token is
// denied (CON-03). Exchange trades such a token for an access key that expires, for S3 clients
// that sign requests.
type Workload struct {
	cfg  config.KAOSWorkloadIdentity
	keys *keySet
	svc  auth.Service
	apps *declared.Loaded
	now  func() time.Time
}

// NewWorkload returns a Workload for cfg, or nil when cfg names no audience. apps are the
// declared apps, with their namespaces.
func NewWorkload(cfg config.KAOSWorkloadIdentity, svc auth.Service, apps *declared.Loaded) (*Workload, error) {
	if cfg.Audience == "" {
		return nil, nil
	}
	if cfg.Issuer == "" {
		raw, err := os.ReadFile(serviceAccountTokenPath)
		if err != nil {
			return nil, fmt.Errorf("kaos.workload_identity.issuer is empty and the cluster's issuer is unknown: %w", err)
		}
		claims := jwt.MapClaims{}
		if _, _, err := jwt.NewParser().ParseUnverified(strings.TrimSpace(string(raw)), claims); err != nil {
			return nil, fmt.Errorf("read issuer of %s: %w", serviceAccountTokenPath, err)
		}
		if cfg.Issuer, _ = claims.GetIssuer(); cfg.Issuer == "" {
			return nil, fmt.Errorf("%s names no issuer", serviceAccountTokenPath)
		}
	}
	return &Workload{
		cfg:  cfg,
		keys: newKeySet(cfg.Issuer, &http.Client{Timeout: 10 * time.Second}),
		svc:  svc,
		apps: apps,
		now:  time.Now,
	}, nil
}

// Owns reports whether token claims to come from this issuer. It does not verify anything.
func (w *Workload) Owns(token string) bool {
	claims := jwt.MapClaims{}
	if _, _, err := jwt.NewParser().ParseUnverified(token, claims); err != nil {
		return false
	}
	iss, _ := claims.GetIssuer()
	return strings.TrimSuffix(iss, "/") == strings.TrimSuffix(w.cfg.Issuer, "/")
}

// Resolve verifies token and returns its app user. Every error denies the request.
func (w *Workload) Resolve(ctx context.Context, token string) (*model.User, error) {
	claims := jwt.MapClaims{}
	keyFn := func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		return w.keys.key(ctx, kid)
	}
	_, err := jwt.ParseWithClaims(token, claims, keyFn,
		jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Name}),
		jwt.WithIssuer(w.cfg.Issuer),
		jwt.WithAudience(w.cfg.Audience),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(w.now),
	)
	if err != nil {
		return nil, fmt.Errorf("%w: %w: %w", auth.ErrAuthenticatingRequest, ErrDenied, err)
	}
	sub, _ := claims.GetSubject()
	username, ok := w.appUser(sub)
	if !ok {
		return nil, fmt.Errorf("%w: %w: %s is not an ML app's workflow here", auth.ErrAuthenticatingRequest, ErrDenied, sub)
	}
	user, err := w.svc.GetUser(ctx, username)
	if err != nil {
		return nil, fmt.Errorf("%w: %w: %s: %w", auth.ErrAuthenticatingRequest, ErrDenied, username, err)
	}
	return user, nil
}

// appUser maps subject system:serviceaccount:{namespace}:{app}-workflow to kaos-app-{app}, when
// that app is declared with that namespace.
func (w *Workload) appUser(sub string) (string, bool) {
	parts := strings.Split(sub, ":")
	if len(parts) != 4 || parts[0] != "system" || parts[1] != "serviceaccount" {
		return "", false
	}
	app, ok := strings.CutSuffix(parts[3], WorkflowSuffix)
	if !ok || app == "" {
		return "", false
	}
	declaredApp, ok := w.apps.Get(declared.UserPrefix + app)
	if !ok || declaredApp.Namespace == "" || declaredApp.Namespace != parts[2] {
		return "", false
	}
	return declaredApp.User, true
}

// ExchangeResponse is the body Exchange answers with.
type ExchangeResponse struct {
	AccessKeyID     string    `json:"access_key_id"`
	SecretAccessKey string    `json:"secret_access_key"`
	Expiration      time.Time `json:"expiration"`
}

// Exchange answers a POST carrying a workload token as bearer with a new access key of the
// token's app user, which works for the configured key TTL.
func (w *Workload) Exchange(rw http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	log := logging.FromContext(ctx).WithField("service", "kaos_workload_exchange")
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || !w.Owns(token) {
		http.Error(rw, "a workload token is required as bearer", http.StatusUnauthorized)
		return
	}
	user, err := w.Resolve(ctx, token)
	if err != nil {
		log.WithError(err).Warn("Workload token denied")
		http.Error(rw, "workload token not accepted", http.StatusUnauthorized)
		return
	}
	// An access key ID is at most 20 characters (pkg/gateway/sig).
	id := declared.ExchangeKeyPrefix + keys.KeyGenerator(20-len(declared.ExchangeKeyPrefix))
	cred, err := w.svc.AddCredentials(ctx, user.Username, id, keys.Base64StringGenerator(30))
	if err != nil {
		log.WithError(err).WithField("user", user.Username).Error("Exchanged key not created")
		http.Error(rw, "access key not created", http.StatusInternalServerError)
		return
	}
	log.WithField("user", user.Username).WithField("access_key_id", id).Info("Exchanged workload token for an access key")
	rw.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(rw).Encode(ExchangeResponse{
		AccessKeyID:     cred.AccessKeyID,
		SecretAccessKey: cred.SecretAccessKey,
		Expiration:      cred.IssuedDate.Add(w.cfg.KeyTTL),
	})
}

// activeWorkload is the server's Workload, set once at start by ConfigureWorkload; nil when off.
var activeWorkload *Workload

// ConfigureWorkload sets the server's Workload from cfg. Call before serving.
func ConfigureWorkload(cfg config.KAOSWorkloadIdentity, svc auth.Service, apps *declared.Loaded) error {
	w, err := NewWorkload(cfg, svc, apps)
	if err != nil {
		return err
	}
	activeWorkload = w
	return nil
}

// ActiveWorkload returns the server's Workload, or nil when workload identity is off.
func ActiveWorkload() *Workload {
	return activeWorkload
}
