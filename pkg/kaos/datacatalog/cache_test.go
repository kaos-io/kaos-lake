// KAOS (kaos-lake, PRD novelcore/kaos#1911 AC-24): new file, Apache-2.0.

package datacatalog_test

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
	"github.com/treeverse/lakefs/pkg/catalog"
	"github.com/treeverse/lakefs/pkg/graveler"
	"github.com/treeverse/lakefs/pkg/kaos/datacatalog"
)

// counting is a Catalog that counts upstream passes and can be held, failed or changed.
type counting struct {
	passes   atomic.Int32 // ListRepositories calls: one per pass
	tags     atomic.Int32
	inflight atomic.Int32
	maxIn    atomic.Int32
	gate     chan struct{} // when set, ListRepositories waits for it
	fail     atomic.Bool
	version  atomic.Value // string: the tag the dataset has
}

func (c *counting) ListRepositories(ctx context.Context, _ int, _, _, _ string, _ ...catalog.ListRepositoriesOptionsFunc) ([]*catalog.Repository, bool, error) {
	c.passes.Add(1)
	if c.gate != nil {
		select {
		case <-c.gate:
		case <-ctx.Done():
			return nil, false, ctx.Err()
		}
	}
	if c.fail.Load() {
		return nil, false, errors.New("upstream down")
	}
	out := []*catalog.Repository{}
	for _, n := range []string{"dataset-a", "dataset-b", "dataset-c", "dataset-d", "dataset-e", "dataset-f", "dataset-g", "dataset-h", "dataset-i", "dataset-j"} {
		out = append(out, &catalog.Repository{Name: n, DefaultBranch: "main"})
	}
	return out, false, nil
}
func (c *counting) GetRepositoryMetadata(context.Context, string) (graveler.RepositoryMetadata, error) {
	n := c.inflight.Add(1)
	defer c.inflight.Add(-1)
	for {
		m := c.maxIn.Load()
		if n <= m || c.maxIn.CompareAndSwap(m, n) {
			break
		}
	}
	time.Sleep(time.Millisecond)
	return graveler.RepositoryMetadata{"kaos.kind": "dataset"}, nil
}
func (c *counting) ListTags(context.Context, string, string, int, string) ([]*catalog.Tag, bool, error) {
	c.tags.Add(1)
	v, _ := c.version.Load().(string)
	return []*catalog.Tag{{ID: v, CommitID: "c"}}, false, nil
}
func (c *counting) GetBranchReference(context.Context, string, string) (string, error) {
	return "h", nil
}

func newCounting() *counting {
	c := &counting{}
	c.version.Store("v1")
	return c
}

func TestCache_ConcurrentReadsShareOnePass(t *testing.T) {
	c := newCounting()
	c.gate = make(chan struct{})
	cache := datacatalog.NewCache(time.Minute)
	const n = 50
	var wg sync.WaitGroup
	results := make([][]datacatalog.Dataset, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l, err := cache.List(t.Context(), c)
			require.NoError(t, err)
			results[i] = l
		}()
	}
	require.Eventually(t, func() bool { return c.passes.Load() == 1 }, time.Second, time.Millisecond)
	time.Sleep(20 * time.Millisecond) // the other readers have joined the pass
	close(c.gate)
	wg.Wait()
	require.EqualValues(t, 1, c.passes.Load())
	require.EqualValues(t, 10, c.tags.Load(), "one pass reads each dataset's tags once")
	for _, r := range results {
		require.Len(t, r, 10)
		require.Equal(t, "dataset-a", r[0].Name, "catalog order is kept")
	}
	_, err := cache.List(t.Context(), c)
	require.NoError(t, err)
	require.EqualValues(t, 1, c.passes.Load(), "a later read inside the TTL costs nothing")
}

func TestCache_TTLShowsANewRelease(t *testing.T) {
	c := newCounting()
	cache := datacatalog.NewCache(5 * time.Second)
	now := time.Unix(1000, 0)
	cache.SetClock(func() time.Time { return now })
	l, _ := cache.List(t.Context(), c)
	require.Equal(t, "v1", l[0].Versions[0].Tag)

	c.version.Store("v2")
	now = now.Add(4 * time.Second)
	l, _ = cache.List(t.Context(), c)
	require.Equal(t, "v1", l[0].Versions[0].Tag, "inside the TTL")
	now = now.Add(2 * time.Second)
	l, _ = cache.List(t.Context(), c)
	require.Equal(t, "v2", l[0].Versions[0].Tag, "after the TTL")
	require.EqualValues(t, 2, c.passes.Load())
}

func TestCache_ErrorIsNotCached(t *testing.T) {
	c := newCounting()
	c.fail.Store(true)
	cache := datacatalog.NewCache(time.Minute)
	_, err := cache.List(t.Context(), c)
	require.Error(t, err)
	c.fail.Store(false)
	l, err := cache.List(t.Context(), c)
	require.NoError(t, err)
	require.Len(t, l, 10)
	c.fail.Store(true)
	l, err = cache.List(t.Context(), c)
	require.NoError(t, err, "a good listing inside the TTL is served; a later failure is not kept either")
	require.Len(t, l, 10)
}

func TestCache_WaiterLeavesWithItsOwnContext_PassSurvives(t *testing.T) {
	c := newCounting()
	c.gate = make(chan struct{})
	cache := datacatalog.NewCache(time.Minute)
	ctx, cancel := context.WithCancel(t.Context())
	first := make(chan error, 1)
	go func() { _, err := cache.List(ctx, c); first <- err }()
	require.Eventually(t, func() bool { return c.passes.Load() == 1 }, time.Second, time.Millisecond)
	second := make(chan error, 1)
	go func() { _, err := cache.List(t.Context(), c); second <- err }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	require.ErrorIs(t, <-first, context.Canceled)
	close(c.gate)
	require.NoError(t, <-second, "the first asker leaving does not fail the others")
	require.EqualValues(t, 1, c.passes.Load())
}

func TestList_ParallelIsBounded(t *testing.T) {
	c := newCounting()
	_, err := datacatalog.List(t.Context(), c)
	require.NoError(t, err)
	require.LessOrEqual(t, c.maxIn.Load(), int32(8))
}

func TestCache_NilListsEveryTime(t *testing.T) {
	c := newCounting()
	var cache *datacatalog.Cache
	_, _ = cache.List(t.Context(), c)
	_, _ = cache.List(t.Context(), c)
	require.EqualValues(t, 2, c.passes.Load())
}

// The handler's answer is the same for every caller that passes Verify, so one key serves them;
// a failed Verify never reaches the cache, and appending peers' datasets never touches it.
func TestHandler_CacheIsolation(t *testing.T) {
	c := newCounting()
	h := datacatalog.Handler{Catalog: c, Namespaces: []string{"a"}, Cache: datacatalog.NewCache(time.Minute),
		Verify: func(_ context.Context, tok string, _ []string) (string, error) {
			if tok == "ok" {
				return "a", nil
			}
			return "", errors.New("denied")
		},
		Shared: func(context.Context) []datacatalog.Dataset {
			return []datacatalog.Dataset{{Name: "p/dataset-x", Owner: "p"}}
		}}
	get := func(tok string) int {
		r := httptest.NewRequest(http.MethodGet, "/kaos/v1/catalog", nil)
		r.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec.Code
	}
	require.Equal(t, http.StatusUnauthorized, get("bad"))
	require.EqualValues(t, 0, c.passes.Load(), "a refused caller starts no pass and reads no cache")
	require.Equal(t, http.StatusOK, get("ok"))
	require.Equal(t, http.StatusOK, get("ok"))
	require.EqualValues(t, 1, c.passes.Load())
	l, _ := h.Cache.List(t.Context(), c)
	require.Len(t, l, 10, "the peers' datasets were appended to a copy")
	require.Equal(t, http.StatusUnauthorized, get("bad"))
}
