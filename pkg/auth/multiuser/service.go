// KAOS (kaos-lake, PRD novelcore/kaos#1217 F-02): new file, Apache-2.0.

// Package multiuser runs the lakeFS v1.86 key-value auth store (contrib/auth/acl) inside the lakeFS
// server as a multi-user auth service with users, groups and policies. It is selected by
// auth.ui_config.rbac "internal" with no auth.api.endpoint; every other configuration keeps
// upstream's auth service unchanged.
package multiuser

import (
	"context"

	"github.com/treeverse/lakefs/contrib/auth/acl"
	"github.com/treeverse/lakefs/pkg/auth"
	"github.com/treeverse/lakefs/pkg/auth/crypt"
	authparams "github.com/treeverse/lakefs/pkg/auth/params"
	"github.com/treeverse/lakefs/pkg/config"
	"github.com/treeverse/lakefs/pkg/kv"
	"github.com/treeverse/lakefs/pkg/logging"
)

// Service is the contrib store with full RBAC: policies are evaluated by the unchanged core
// (auth.CheckPermissions) and any valid policy can be written.
type Service struct {
	*acl.AuthService
}

// IsAdvancedAuth is true so that setup creates the RBAC base groups and policies.
func (s *Service) IsAdvancedAuth() bool {
	return true
}

// Enabled reports whether cfg selects the in-process multi-user service.
func Enabled(cfg config.Config) bool {
	authCfg := cfg.AuthConfig()
	return authCfg.GetAuthUIConfig().RBAC == config.AuthRBACInternal && !authCfg.GetBaseAuthConfig().IsAuthTypeAPI()
}

// NewAuthService returns the in-process multi-user service when Enabled, migrating the basic-auth
// admin on first start, and upstream's auth.NewAuthService otherwise.
func NewAuthService(ctx context.Context, cfg config.Config, logger logging.Logger, kvStore kv.Store, metadataManager *auth.KVMetadataManager) auth.Service {
	if !Enabled(cfg) {
		return auth.NewAuthService(ctx, cfg, logger, kvStore, metadataManager)
	}
	baseAuthCfg := cfg.AuthConfig().GetBaseAuthConfig()
	secretStore := crypt.NewSecretStore([]byte(baseAuthCfg.Encrypt.SecretKey))
	svc := &Service{AuthService: acl.NewAuthService(kvStore, secretStore, authparams.ServiceCache(baseAuthCfg.Cache))}

	initialized, err := metadataManager.IsInitialized(ctx)
	if err != nil {
		logger.WithError(err).Fatal("failed to get lakeFS init status")
	}
	if initialized {
		username, err := MigrateFromBasic(ctx, kvStore, secretStore, svc)
		if err != nil {
			logger.WithError(err).Fatal("migration from basic auth to multi-user auth failed")
		}
		if username != "" {
			logger.WithField("user", username).Info("basic auth admin migrated to multi-user auth (group Admins)")
		}
	}
	logger.WithField("service", "auth_service").Info("initialized in-process multi-user auth service")
	return auth.NewMonitoredAuthService(svc)
}
