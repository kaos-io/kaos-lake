// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-12): new file, Apache-2.0.

package sharing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/treeverse/lakefs/pkg/kaos/datacatalog"
	"github.com/treeverse/lakefs/pkg/kaos/repos"
	"github.com/treeverse/lakefs/pkg/logging"
	"golang.org/x/sync/singleflight"
)

const (
	// SharedPath is the owner side's endpoint, on every peer.
	SharedPath = "/kaos/v1/shared"
	// PeerTimeout bounds each peer's answer; peers are asked in parallel.
	PeerTimeout = 5 * time.Second
	// StaleFor is how long a peer's last good answer is still served while it cannot be read.
	StaleFor = 10 * time.Minute
	// DefaultCacheTTL is how long a peer's answer is reused when kaos.sharing.cache_ttl is unset.
	DefaultCacheTTL = 60 * time.Second
	// maxSharedBody bounds a peer's answer.
	maxSharedBody = 8 << 20
)

// ErrOlderPeer marks a peer whose lakeFS has no /kaos/v1/shared yet: it answers 404, or 200 with
// its web UI's HTML page (lakeFS serves the UI for unknown paths).
var ErrOlderPeer = errors.New("peer's lakeFS is too old to share datasets (no " + SharedPath + ")")

// SharedResponse is what GET /kaos/v1/shared answers.
type SharedResponse struct {
	Owner    string                `json:"owner"`
	Datasets []datacatalog.Dataset `json:"datasets"`
}

// Aggregator reads the datasets every peer shares with this project, for the catalog.
//
// Each peer's answer is reused for TTL, also after a failure, so a peer that is down costs at
// most one PeerTimeout per TTL. A peer that times out or answers an error contributes its last
// good answer while that is at most StaleFor old, else nothing; the other peers are unaffected.
// 401 is a good, empty answer: the peer shares nothing with this project (it has no consumer
// group for it), which is how an unshare of the last dataset arrives.
type Aggregator struct {
	// Peers returns the peers to ask (the peers file).
	Peers func() []Peer
	// Token returns the bearer token sent to peers (Minter.Token).
	Token  func(ctx context.Context) (string, error)
	Client *http.Client
	TTL    time.Duration // zero: DefaultCacheTTL
	// Timeout per peer; zero: PeerTimeout.
	Timeout time.Duration
	// Stale is how long a last good answer outlives failures; zero: StaleFor.
	Stale time.Duration
	Log   logging.Logger
	Now   func() time.Time

	mu    sync.Mutex
	cache map[string]*entry // by peer project
	group singleflight.Group
}

type entry struct {
	endpoint string
	datasets []datacatalog.Dataset // the last good answer, named <owner>/<dataset>
	good     time.Time             // when datasets were read; zero: never
	checked  time.Time             // the last attempt
}

func (a *Aggregator) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func orDefault(d, def time.Duration) time.Duration {
	if d <= 0 {
		return def
	}
	return d
}

func (a *Aggregator) log() logging.Logger {
	if a.Log == nil {
		return logging.Dummy()
	}
	return a.Log
}

// Shared returns the datasets the peers share with this project, peer by peer in the peers
// file's order, named <owner>/<dataset> with Owner set and never a default.
func (a *Aggregator) Shared(ctx context.Context) []datacatalog.Dataset {
	peers := a.Peers()
	a.mu.Lock()
	if a.cache == nil {
		a.cache = map[string]*entry{}
	}
	for project := range a.cache { // a peer no longer in the file is forgotten at once
		if !slices.ContainsFunc(peers, func(p Peer) bool { return p.Project == project }) {
			delete(a.cache, project)
		}
	}
	a.mu.Unlock()

	answers := make([][]datacatalog.Dataset, len(peers))
	var wg sync.WaitGroup
	for i, p := range peers {
		wg.Go(func() { answers[i] = a.peer(ctx, p) })
	}
	wg.Wait()
	var out []datacatalog.Dataset
	for _, ds := range answers {
		out = append(out, ds...)
	}
	return out
}

// peer returns p's answer, from the cache while it is fresh.
func (a *Aggregator) peer(ctx context.Context, p Peer) []datacatalog.Dataset {
	a.mu.Lock()
	e := a.cache[p.Project]
	fresh := e != nil && e.endpoint == p.Endpoint && a.now().Sub(e.checked) < orDefault(a.TTL, DefaultCacheTTL)
	a.mu.Unlock()
	if !fresh {
		// Concurrent catalog calls share one request per peer; it is not cut short when the
		// caller that started it goes away.
		_, _, _ = a.group.Do(p.Project+" "+p.Endpoint, func() (any, error) {
			fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), orDefault(a.Timeout, PeerTimeout))
			defer cancel()
			ds, err := a.fetch(fctx, p)
			a.mu.Lock()
			defer a.mu.Unlock()
			e := a.cache[p.Project]
			if e == nil || e.endpoint != p.Endpoint {
				e = &entry{endpoint: p.Endpoint}
				a.cache[p.Project] = e
			}
			e.checked = a.now()
			if err != nil {
				log := a.log().WithError(err).WithField("peer", p.Project).WithField("endpoint", p.Endpoint)
				if e.good.IsZero() {
					log.Warn("Datasets shared by a peer not read")
				} else {
					log.WithField("last_good", e.good).Warn("Datasets shared by a peer not read; its last good answer is served for now")
				}
				return nil, nil
			}
			e.datasets, e.good = ds, e.checked
			return nil, nil
		})
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	e = a.cache[p.Project]
	if e == nil || e.endpoint != p.Endpoint || e.good.IsZero() || a.now().Sub(e.good) > orDefault(a.Stale, StaleFor) {
		return nil
	}
	return slices.Clone(e.datasets)
}

// fetch asks p for the datasets it shares with this project.
func (a *Aggregator) fetch(ctx context.Context, p Peer) ([]datacatalog.Dataset, error) {
	token, err := a.Token(ctx)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSuffix(p.Endpoint, "/")+SharedPath, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	client := a.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		// An older lakeFS answers an unknown path with its web UI's page.
		if !strings.Contains(resp.Header.Get("Content-Type"), "json") {
			return nil, ErrOlderPeer
		}
	case http.StatusUnauthorized:
		return []datacatalog.Dataset{}, nil // nothing is shared with this project
	case http.StatusNotFound:
		return nil, ErrOlderPeer
	default:
		return nil, fmt.Errorf("GET %s: %s", SharedPath, resp.Status)
	}
	var body SharedResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxSharedBody)).Decode(&body); err != nil {
		return nil, fmt.Errorf("GET %s: %w", SharedPath, err)
	}
	if body.Owner != p.Project {
		return nil, fmt.Errorf("GET %s: answered for owner %q, want %q", SharedPath, body.Owner, p.Project)
	}
	out := make([]datacatalog.Dataset, 0, len(body.Datasets))
	for _, d := range body.Datasets {
		if !repos.ValidName(d.Name, repos.DatasetPrefix) {
			continue // only datasets are shared
		}
		d.Name, d.Owner, d.Default = p.Project+"/"+d.Name, p.Project, false
		if d.Versions == nil {
			d.Versions = []datacatalog.Version{}
		}
		out = append(out, d)
	}
	return out, nil
}
