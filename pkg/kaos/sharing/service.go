// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-12): new file, Apache-2.0.

package sharing

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/treeverse/lakefs/pkg/config"
	"github.com/treeverse/lakefs/pkg/logging"
)

const defaultInterval = time.Minute

// Service is the consumer side: the peers file and the catalog's aggregation.
type Service struct {
	Peers      *PeersFile
	Aggregator *Aggregator
	interval   time.Duration
	log        logging.Logger
}

// New returns the Service of cfg, or nil when cfg names no peers file. Peers accept only the
// identity provider's tokens, so it needs token identity: the issuer mints the token, and the
// first audience (the organisation's Zitadel project) is what the token is audienced to.
func New(cfg config.KAOSSharing, identity config.KAOSTokenIdentity, log logging.Logger) (*Service, error) {
	if cfg.PeersPath == "" {
		return nil, nil
	}
	if identity.Issuer == "" || len(identity.Audiences) == 0 {
		return nil, errors.New("kaos.sharing needs kaos.token_identity (issuer and audiences): peers accept only its tokens")
	}
	peers := NewPeersFile(cfg.PeersPath, identity.Project)
	minter := &Minter{KeyPath: cfg.MachineKeyPath, Issuer: identity.Issuer, ProjectID: identity.Audiences[0],
		Client: &http.Client{Timeout: 10 * time.Second}}
	if log == nil {
		log = logging.Dummy()
	}
	log = log.WithField("service", "kaos_sharing")
	return &Service{
		Peers: peers,
		Aggregator: &Aggregator{
			Peers:  func() []Peer { return peers.Get().Peers },
			Token:  minter.Token,
			Client: &http.Client{},
			TTL:    orDefault(cfg.CacheTTL, DefaultCacheTTL),
			Log:    log,
		},
		interval: orDefault(cfg.Interval, defaultInterval),
		log:      log,
	}, nil
}

// Run reads the peers file now and every kaos.sharing.interval until ctx ends.
func (s *Service) Run(ctx context.Context) {
	s.Peers.Run(ctx, s.interval, s.log)
}

var (
	activeMu sync.RWMutex
	active   *Service
)

// Configure makes the Service of cfg the one the API serves (called once, by lakefs run). It
// returns nil when sharing is off.
func Configure(cfg config.KAOSSharing, identity config.KAOSTokenIdentity, log logging.Logger) (*Service, error) {
	s, err := New(cfg, identity, log)
	if err != nil {
		return nil, err
	}
	activeMu.Lock()
	defer activeMu.Unlock()
	active = s
	return s, nil
}

// Active returns the configured Service, or nil when sharing is off.
func Active() *Service {
	activeMu.RLock()
	defer activeMu.RUnlock()
	return active
}
