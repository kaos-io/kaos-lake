// KAOS (kaos-lake, PRD novelcore/kaos#1217 F-05): new file, Apache-2.0.

package declared

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/treeverse/lakefs/pkg/auth"
	"github.com/treeverse/lakefs/pkg/auth/model"
)

// ExchangeKeyPrefix starts every access key issued to an app user in exchange for its workload
// token (pkg/auth/tokenid, F-05). Such a key works for a fixed time after it was issued; Reconcile
// deletes it once expired.
const ExchangeKeyPrefix = "KAOSX"

// Expired reports whether c is an exchanged key issued ttl or longer before now.
func Expired(c *model.Credential, ttl time.Duration, now time.Time) bool {
	return strings.HasPrefix(c.AccessKeyID, ExchangeKeyPrefix) && !now.Before(c.IssuedDate.Add(ttl))
}

// ExpiringKeys returns svc, except that an exchanged key older than ttl is not found, so no
// request signed with it is accepted.
func ExpiringKeys(svc auth.Service, ttl time.Duration) auth.Service {
	return expiringKeys{Service: svc, ttl: ttl}
}

type expiringKeys struct {
	auth.Service
	ttl time.Duration
}

func (s expiringKeys) GetCredentials(ctx context.Context, accessKeyID string) (*model.Credential, error) {
	c, err := s.Service.GetCredentials(ctx, accessKeyID)
	if err == nil && Expired(c, s.ttl, time.Now()) {
		return nil, fmt.Errorf("access key %s expired: %w", accessKeyID, auth.ErrNotFound)
	}
	return c, err
}
