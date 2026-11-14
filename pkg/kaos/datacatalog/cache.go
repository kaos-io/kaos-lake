// KAOS (kaos-lake, PRD novelcore/kaos#1911 AC-24): new file, Apache-2.0.

package datacatalog

import (
	"context"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

const (
	// CacheTTL is how long a listing is reused. A release becomes visible within it (and the
	// dataset event that tells CI to re-read the catalog is sent after a 10 s window, so it
	// always finds the release); a commit to a dataset's main moves Latest within it too. No
	// in-process signal covers commits, and other replicas of this server see no signal at all,
	// so a bounded age is the one rule that holds everywhere.
	CacheTTL = 5 * time.Second
	// computeTimeout bounds one pass over the data store, whoever asked for it.
	computeTimeout = 2 * time.Minute
)

// Cache keeps the last good listing of the project's own datasets. Concurrent reads share one
// pass over the data store (single flight); a failed pass is never kept. A nil *Cache lists on
// every call.
type Cache struct {
	ttl time.Duration
	now func() time.Time

	flight singleflight.Group

	mu   sync.Mutex
	list []Dataset
	at   time.Time
	ok   bool
}

// NewCache returns a Cache that reuses a listing for ttl.
func NewCache(ttl time.Duration) *Cache { return &Cache{ttl: ttl, now: time.Now} }

// List returns the project's datasets as List does, from the cache when it is younger than the
// TTL. The result is shared: callers must not modify it.
//
// A pass starts only when the cached listing is expired, one at a time, and replaces it whole, so
// a reader of this process never goes back to an older listing. The pass runs under its own
// context (not the first asker's, whose cancellation would fail every waiter), bounded by
// computeTimeout; each waiter still returns when its own context ends.
func (c *Cache) List(ctx context.Context, cat Catalog) ([]Dataset, error) {
	if c == nil {
		return List(ctx, cat)
	}
	if l, ok := c.fresh(); ok {
		return l, nil
	}
	ch := c.flight.DoChan("own", func() (any, error) {
		// A flight that began while a previous one was finishing finds its result.
		if l, ok := c.fresh(); ok {
			return l, nil
		}
		pass, cancel := context.WithTimeout(context.WithoutCancel(ctx), computeTimeout)
		defer cancel()
		l, err := List(pass, cat)
		if err != nil {
			return nil, err
		}
		c.mu.Lock()
		c.list, c.at, c.ok = l, c.now(), true
		c.mu.Unlock()
		return l, nil
	})
	select {
	case res := <-ch:
		if res.Err != nil {
			return nil, res.Err
		}
		return res.Val.([]Dataset), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (c *Cache) fresh() ([]Dataset, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ok && c.now().Sub(c.at) < c.ttl {
		return c.list, true
	}
	return nil, false
}
