// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-12): new file, Apache-2.0.

package sharing_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/treeverse/lakefs/pkg/kaos/datacatalog"
	"github.com/treeverse/lakefs/pkg/kaos/sharing"
)

// peer is a fake owner-side lakeFS whose /kaos/v1/shared answer the test sets.
type peer struct {
	*httptest.Server
	project string
	hits    atomic.Int32

	mu     sync.Mutex
	status int
	ctype  string
	body   string
	delay  time.Duration
	auth   string // last Authorization header
}

func newPeer(t *testing.T, project, body string) *peer {
	t.Helper()
	p := &peer{project: project, status: http.StatusOK, ctype: "application/json", body: body}
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.hits.Add(1)
		p.mu.Lock()
		status, ctype, body, delay := p.status, p.ctype, p.body, p.delay
		p.auth = r.Header.Get("Authorization")
		p.mu.Unlock()
		if r.URL.Path != sharing.SharedPath {
			http.NotFound(w, r)
			return
		}
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("Content-Type", ctype)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(p.Close)
	return p
}

func (p *peer) set(status int, body string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.status, p.body = status, body
}

func (p *peer) asPeer() sharing.Peer {
	return sharing.Peer{Project: p.project, Endpoint: p.URL + "/", URL: "https://lakefs-" + p.project + ".acme.kaos.eu"}
}

const roadsV1 = `{"owner":"cars","datasets":[{"name":"dataset-roads","default":true,"description":"roads","location":"europe-west1 · Belgium",
 "latest":"h1","versions":[{"tag":"v1","commit":"c1"}]}]}`

var roads = datacatalog.Dataset{Name: "cars/dataset-roads", Owner: "cars", Description: "roads", Location: "europe-west1 · Belgium",
	Latest: "h1", Versions: []datacatalog.Version{{Tag: "v1", Commit: "c1"}}}

func aggregator(c *clock, peers ...*peer) *sharing.Aggregator {
	return &sharing.Aggregator{
		Peers: func() []sharing.Peer {
			var out []sharing.Peer
			for _, p := range peers {
				out = append(out, p.asPeer())
			}
			return out
		},
		Token: func(context.Context) (string, error) { return "machine-token", nil },
		Now:   c.Now,
	}
}

// A shared dataset is named <owner>/<dataset>, owner set, never the default; only datasets.
func TestAggregator_NamesAndOwner(t *testing.T) {
	cars := newPeer(t, "cars", `{"owner":"cars","datasets":[
	 {"name":"dataset-roads","default":true,"description":"roads","location":"europe-west1 · Belgium","latest":"h1","versions":[{"tag":"v1","commit":"c1"}]},
	 {"name":"outputs-yolo","versions":[]},
	 {"name":"dataset-../../x","versions":[]},
	 {"name":"cars/dataset-nested","versions":[]},
	 {"name":"dataset-empty"}]}`)
	got := aggregator(newClock(), cars).Shared(t.Context())
	require.Equal(t, []datacatalog.Dataset{roads,
		{Name: "cars/dataset-empty", Owner: "cars", Versions: []datacatalog.Version{}}}, got)
	require.Equal(t, "Bearer machine-token", cars.auth, "the minted machine token, never an access key")
}

// A peer answering for another owner is not believed.
func TestAggregator_OwnerMismatch(t *testing.T) {
	cars := newPeer(t, "cars", `{"owner":"orbits","datasets":[{"name":"dataset-roads","versions":[]}]}`)
	require.Empty(t, aggregator(newClock(), cars).Shared(t.Context()))
	cars.set(http.StatusOK, `{"datasets":[{"name":"dataset-roads","versions":[]}]}`)
	require.Empty(t, aggregator(newClock(), cars).Shared(t.Context()))
}

// A peer that times out, fails, is too old (404 or the web UI's page) or answers garbage leaves
// the others' datasets in place.
func TestAggregator_PeerFailuresLeaveOthers(t *testing.T) {
	good := newPeer(t, "cars", roadsV1)
	failing := newPeer(t, "fails", "")
	failing.set(http.StatusInternalServerError, "boom")
	old := newPeer(t, "old", "")
	old.set(http.StatusNotFound, "")
	ui := newPeer(t, "ui", "<html></html>")
	ui.ctype = "text/html"
	garbage := newPeer(t, "garbage", "{not json")
	slow := newPeer(t, "slow", roadsV1)
	slow.delay = 2 * time.Second
	a := aggregator(newClock(), failing, good, old, ui, garbage, slow)
	a.Timeout = 100 * time.Millisecond
	start := time.Now()
	require.Equal(t, []datacatalog.Dataset{roads}, a.Shared(t.Context()))
	require.Less(t, time.Since(start), time.Second)
}

// After a failure a peer's last good answer is served for up to 10 minutes, then dropped; it
// comes back when the peer does.
func TestAggregator_StaleCopyUpToTenMinutes(t *testing.T) {
	for name, fail := range map[string]func(p *peer){
		"5xx":     func(p *peer) { p.set(http.StatusBadGateway, "") },
		"404":     func(p *peer) { p.set(http.StatusNotFound, "") },
		"timeout": func(p *peer) { p.mu.Lock(); p.delay = 2 * time.Second; p.mu.Unlock() },
	} {
		t.Run(name, func(t *testing.T) {
			c := newClock()
			cars := newPeer(t, "cars", roadsV1)
			a := aggregator(c, cars)
			a.Timeout = 100 * time.Millisecond
			require.Equal(t, []datacatalog.Dataset{roads}, a.Shared(t.Context()))
			fail(cars)
			c.Add(61 * time.Second)
			require.Equal(t, []datacatalog.Dataset{roads}, a.Shared(t.Context()), "stale copy")
			require.EqualValues(t, 2, cars.hits.Load())
			c.Add(8 * time.Minute)
			require.Equal(t, []datacatalog.Dataset{roads}, a.Shared(t.Context()), "9m01s old: still served")
			c.Add(61 * time.Second)
			require.Empty(t, a.Shared(t.Context()), "older than 10 minutes: dropped")

			cars.mu.Lock()
			cars.status, cars.body, cars.delay = http.StatusOK, roadsV1, 0
			cars.mu.Unlock()
			c.Add(61 * time.Second)
			require.Equal(t, []datacatalog.Dataset{roads}, a.Shared(t.Context()), "back with the peer")
		})
	}
}

// Peers are asked in parallel, each bounded by the 5 s default timeout.
func TestAggregator_ParallelFiveSecondTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for the real 5 s timeout")
	}
	require.Equal(t, 5*time.Second, sharing.PeerTimeout)
	var hanging []*peer
	for _, name := range []string{"h1", "h2", "h3"} {
		p := newPeer(t, name, roadsV1)
		p.delay = 30 * time.Second
		hanging = append(hanging, p)
	}
	cars := newPeer(t, "cars", roadsV1)
	a := aggregator(newClock(), append(hanging, cars)...)
	start := time.Now()
	got := a.Shared(t.Context())
	elapsed := time.Since(start)
	require.Equal(t, []datacatalog.Dataset{roads}, got)
	require.GreaterOrEqual(t, elapsed, 5*time.Second)
	require.Less(t, elapsed, 7*time.Second, "three hanging peers cost one timeout, not three")
}

// An answer is reused for cache_ttl (default 60 s), a failure too: a peer that is down is not
// asked on every catalog call.
func TestAggregator_CacheTTL(t *testing.T) {
	c := newClock()
	cars := newPeer(t, "cars", roadsV1)
	a := aggregator(c, cars)
	a.Shared(t.Context())
	c.Add(59 * time.Second)
	a.Shared(t.Context())
	require.EqualValues(t, 1, cars.hits.Load(), "default TTL 60s")
	c.Add(2 * time.Second)
	a.Shared(t.Context())
	require.EqualValues(t, 2, cars.hits.Load())

	cars.set(http.StatusServiceUnavailable, "")
	c.Add(61 * time.Second)
	a.Shared(t.Context())
	a.Shared(t.Context())
	require.EqualValues(t, 3, cars.hits.Load(), "a failure is reused for the TTL too")

	short := aggregator(c, cars)
	short.TTL = 10 * time.Second
	short.Shared(t.Context())
	c.Add(11 * time.Second)
	short.Shared(t.Context())
	require.EqualValues(t, 5, cars.hits.Load(), "cache_ttl is respected")
}

// An unshare arrives once the cache expires: the peer answers 401 (no consumer group any more)
// or an empty list, and the shared entry is gone at once, with no stale copy.
func TestAggregator_UnsharePropagates(t *testing.T) {
	for name, unshared := range map[string]func(p *peer){
		"401, nothing shared any more": func(p *peer) { p.set(http.StatusUnauthorized, `{"message":"nothing is shared"}`) },
		"another dataset still shared": func(p *peer) { p.set(http.StatusOK, `{"owner":"cars","datasets":[]}`) },
	} {
		t.Run(name, func(t *testing.T) {
			c := newClock()
			cars := newPeer(t, "cars", roadsV1)
			a := aggregator(c, cars)
			require.Equal(t, []datacatalog.Dataset{roads}, a.Shared(t.Context()))
			unshared(cars)
			c.Add(30 * time.Second)
			require.Equal(t, []datacatalog.Dataset{roads}, a.Shared(t.Context()), "within the TTL the cache answers")
			c.Add(31 * time.Second)
			require.Empty(t, a.Shared(t.Context()))
		})
	}
}

// Concurrent catalog calls ask each peer once.
func TestAggregator_ConcurrentCallsShareOneRequest(t *testing.T) {
	cars := newPeer(t, "cars", roadsV1)
	cars.delay = 100 * time.Millisecond
	a := aggregator(newClock(), cars)
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() { require.Equal(t, []datacatalog.Dataset{roads}, a.Shared(t.Context())) })
	}
	wg.Wait()
	require.EqualValues(t, 1, cars.hits.Load())
}

// A peer removed from the peers file is forgotten at once, stale copy included.
func TestAggregator_RemovedPeerForgotten(t *testing.T) {
	c := newClock()
	cars := newPeer(t, "cars", roadsV1)
	var peers atomic.Value
	peers.Store([]sharing.Peer{cars.asPeer()})
	a := aggregator(c, cars)
	a.Peers = func() []sharing.Peer { return peers.Load().([]sharing.Peer) }
	require.Len(t, a.Shared(t.Context()), 1)
	peers.Store([]sharing.Peer{})
	require.Empty(t, a.Shared(t.Context()))
	peers.Store([]sharing.Peer{cars.asPeer()})
	cars.set(http.StatusInternalServerError, "")
	require.Empty(t, a.Shared(t.Context()), "no stale copy survives a removal")
}

// Without a token nothing is sent; the last good answers stay as for any failure.
func TestAggregator_TokenError(t *testing.T) {
	c := newClock()
	cars := newPeer(t, "cars", roadsV1)
	a := aggregator(c, cars)
	require.Len(t, a.Shared(t.Context()), 1)
	a.Token = func(context.Context) (string, error) { return "", errors.New("no machine key") }
	c.Add(61 * time.Second)
	require.Len(t, a.Shared(t.Context()), 1, "stale copy")
	require.EqualValues(t, 1, cars.hits.Load(), "nothing sent without a token")
}
