// KAOS (kaos-lake, PRD novelcore/kaos#1911 CON-02, CON-03): new file, Apache-2.0.

package declared_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/treeverse/lakefs/pkg/auth"
	"github.com/treeverse/lakefs/pkg/auth/declared"
	"github.com/treeverse/lakefs/pkg/catalog"
	"github.com/treeverse/lakefs/pkg/graveler"
	"github.com/treeverse/lakefs/pkg/kaos/repos"
	"github.com/treeverse/lakefs/pkg/logging"
	"github.com/treeverse/lakefs/pkg/permissions"
)

const grantSettle = 200 * time.Millisecond

// lake runs the two loops the way cmd/lakefs does (repos.Run, declared.Run with the wake-up and the
// grant marker wired as in cmd/lakefs/cmd/run.go), over a real catalog and the multi-user store.
// Both loops have an interval of an hour, so only a wake-up can make either run again after its
// first pass: before the fix, the app was granted the right to write a dataset at its own next
// interval, up to a minute after the dataset existed.
type lake struct {
	t   *testing.T
	svc auth.Service
	c   *catalog.Catalog
	dir string

	mu       sync.Mutex
	producer map[string]string // dataset -> app, what repos.Run reads
}

func newLake(t *testing.T, c *catalog.Catalog, svc auth.Service) *lake {
	return &lake{t: t, svc: svc, c: c, dir: t.TempDir(), producer: map[string]string{}}
}

// declare makes app (an App, with its datasets) declare datasets, in both loops' inputs.
func (l *lake) declare(a declared.App) {
	l.t.Helper()
	b, err := json.Marshal(a)
	require.NoError(l.t, err)
	writeApp(l.t, l.dir, "app-"+a.User, string(b))
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, d := range a.Datasets {
		l.producer[d] = a.User[len(declared.UserPrefix):]
	}
}

// start runs both loops until the test ends. ready decides which datasets an app may write; the
// default is repos.ProducedBy, as in the server.
func (l *lake) start(ready func(ctx context.Context, dataset, app string) bool) {
	l.t.Helper()
	ctx, cancel := context.WithCancel(context.WithoutCancel(l.t.Context()))
	var wg sync.WaitGroup
	l.t.Cleanup(func() { cancel(); wg.Wait() })
	if ready == nil {
		ready = func(ctx context.Context, dataset, app string) bool {
			ok, err := repos.ProducedBy(ctx, l.c, dataset, app)
			require.NoError(l.t, err)
			return ok
		}
	}
	loaded := &declared.Loaded{}
	wg.Add(2)
	go func() {
		defer wg.Done()
		declared.Run(ctx, l.svc, l.dir, time.Hour, 0, loaded, logging.ContextUnavailable(),
			declared.WithProducedDatasets(ready),
			declared.WithWake(repos.Changed()),
			declared.WithRetry(20*time.Millisecond, time.Minute),
			declared.WithAfterRound(func(ctx context.Context) bool {
				_, waiting, err := repos.ReconcileGrants(ctx, l.c, func(ctx context.Context, dataset, app string) (time.Time, bool, error) {
					return declared.GrantedSince(ctx, l.svc, dataset, app)
				}, grantSettle, time.Now())
				require.NoError(l.t, err)
				return waiting
			}))
	}()
	go func() {
		defer wg.Done()
		repos.Run(ctx, l.c, l.svc, "", "mem://lake", "", "", func() map[string]string { return nil },
			func() map[string]string {
				l.mu.Lock()
				defer l.mu.Unlock()
				out := map[string]string{}
				for k, v := range l.producer {
					out[k] = v
				}
				return out
			}, time.Hour, grantSettle, repos.Retention{}, logging.ContextUnavailable())
	}()
}

func (l *lake) metadata(dataset string) graveler.RepositoryMetadata {
	md, err := l.c.GetRepositoryMetadata(l.t.Context(), dataset)
	if err != nil {
		return nil
	}
	return md
}

// mayWrite is what the failed run hit: the library deletes its run branch before creating it, and
// the core asks for the right before it looks whether the branch exists.
func (l *lake) mayWrite(user, dataset string) bool {
	return allowed(l.t, l.svc, user, permissions.DeleteBranchAction, permissions.BranchArn(dataset, "run-1")) &&
		allowed(l.t, l.svc, user, permissions.CreateBranchAction, permissions.BranchArn(dataset, "run-1")) &&
		allowed(l.t, l.svc, user, permissions.CreateCommitAction, permissions.BranchArn(dataset, "run-1"))
}

// await waits until each dataset says it is ready (repos.Granted). Whenever one does, the app may
// really write it: the signal never comes before the grant.
func (l *lake) await(user string, datasets ...string) {
	l.t.Helper()
	require.Eventually(l.t, func() bool {
		all := true
		for _, d := range datasets {
			ready := repos.Granted(l.metadata(d))
			if ready {
				require.True(l.t, l.mayWrite(user, d), "%s says it is ready (%s) before %s may write it", d, l.metadata(d)[repos.KeyGrant], user)
			}
			all = all && ready
		}
		return all
	}, 20*time.Second, 10*time.Millisecond, "the loops have an interval of an hour: only the wake-up can get there")
}

// The failure the live run hit (PRD novelcore/kaos#1911): a pipeline declares a new produced
// dataset; the repository is created and protected by one loop, and the app's grant came from
// the other loop at its next pass, up to a minute later. Now the one wakes the other: the grant
// is there, and the dataset says so, a short settle after the repository exists, not an interval.
func TestProducedDataset_AppMayWriteRightAfterItIsMade(t *testing.T) {
	svc := newStore(t)
	l := newLake(t, realCatalog(t), svc)
	app := yolo
	app.Datasets = []string{"dataset-clean", "dataset-cars"} // two at once
	l.declare(app)
	l.start(nil)

	l.await(app.User, "dataset-clean", "dataset-cars")
	require.Equal(t, "yolo", l.metadata("dataset-clean")[repos.KeyProducer])
	require.True(t, l.mayWrite(app.User, "dataset-cars"))
	// Nobody but the platform sets the signal.
	require.ErrorIs(t, repos.ValidateMetadata("dataset-clean", map[string]string{repos.KeyGrant: "x@y"}), repos.ErrInvalid)
}

// The lake restarted between the two steps: the repository is made and protected, the app has no
// grant. The first pass of the repositories loop wakes the other loop, whose own first round came
// before.
func TestProducedDataset_GrantAfterARestartBetweenTheTwoSteps(t *testing.T) {
	ctx := t.Context()
	svc := newStore(t)
	c := realCatalog(t)
	app := yolo
	app.Datasets = []string{"dataset-clean"}
	_, err := repos.Reconcile(ctx, c, "", "mem://lake", "", repos.Desired("", nil, map[string]string{"dataset-clean": "yolo"}))
	require.NoError(t, err)
	_, err = repos.ReconcileProtection(ctx, c, c)
	require.NoError(t, err)

	l := newLake(t, c, svc)
	l.declare(app)
	l.start(nil)
	l.await(app.User, "dataset-clean")
}

// The stored rules say protected a few seconds before the write paths enforce it, so the dataset
// is not ready for its app for those seconds (repos.ProducedBy). Nothing wakes the loops when it
// becomes so: the declared-app loop looks again by itself while a dataset is not ready.
func TestProducedDataset_GrantWhenEnforcementCatchesUp(t *testing.T) {
	svc := newStore(t)
	c := realCatalog(t)
	app := yolo
	app.Datasets = []string{"dataset-clean"}
	var mu sync.Mutex
	asked := 0
	l := newLake(t, c, svc)
	l.declare(app)
	l.start(func(ctx context.Context, dataset, a string) bool {
		ok, err := repos.ProducedBy(ctx, c, dataset, a)
		require.NoError(t, err)
		if !ok {
			return false
		}
		mu.Lock()
		defer mu.Unlock()
		asked++
		return asked > 5 // the write paths enforce it only now
	})
	l.await(app.User, "dataset-clean")
	mu.Lock()
	defer mu.Unlock()
	require.Greater(t, asked, 5)
}

// The signal follows the grant, both ways: it is set only once the app's policy has held the
// allowance for longer than a session keeps a policy; and it is taken away the moment the allowance
// is gone, whether the dataset left the app's declaration, or the app was renamed.
func TestReconcileGrants(t *testing.T) {
	ctx := t.Context()
	svc := newStore(t)
	c := realCatalog(t)
	produced := repos.Desired("", nil, map[string]string{"dataset-clean": "yolo"})
	_, err := repos.Reconcile(ctx, c, "", "mem://lake", "", produced)
	require.NoError(t, err)
	_, err = repos.ReconcileProtection(ctx, c, c)
	require.NoError(t, err)

	app := yolo
	app.Datasets = []string{"dataset-clean"}
	ready := declared.WithProducedDatasets(func(ctx context.Context, dataset, a string) bool {
		ok, err := repos.ProducedBy(ctx, c, dataset, a)
		require.NoError(t, err)
		return ok
	})
	since := func(ctx context.Context, dataset, a string) (time.Time, bool, error) {
		return declared.GrantedSince(ctx, svc, dataset, a)
	}
	grants := func(now time.Time) (bool, string) {
		t.Helper()
		changes, waiting, err := repos.ReconcileGrants(ctx, c, since, time.Minute, now)
		require.NoError(t, err)
		return waiting, fmt.Sprint(changes)
	}
	marked := func() bool { return repos.Granted(labels(t, c)) }

	// No grant yet: nothing to mark, and the caller is told to look again.
	waiting, _ := grants(time.Now())
	require.True(t, waiting)
	require.False(t, marked())

	_, err = declared.Reconcile(ctx, svc, []declared.App{app}, 0, ready)
	require.NoError(t, err)
	// Granted just now: not old enough to rely on.
	waiting, _ = grants(time.Now())
	require.True(t, waiting)
	require.False(t, marked(), "a session may still hold the policy without the grant")

	// Old enough.
	waiting, changes := grants(time.Now().Add(2 * time.Minute))
	require.False(t, waiting)
	require.True(t, marked())
	require.Contains(t, changes, "may write it")
	md := labels(t, c)
	require.Contains(t, md[repos.KeyGrant], "yolo@")
	// Idempotent.
	_, changes = grants(time.Now().Add(2 * time.Minute))
	require.Equal(t, "[]", changes)

	// The dataset leaves the app's declaration: the next pass of the app loop takes the allowance
	// back and the signal goes with it.
	app.Datasets = nil
	_, err = declared.Reconcile(ctx, svc, []declared.App{app}, 0, ready)
	require.NoError(t, err)
	waiting, _ = grants(time.Now().Add(2 * time.Minute))
	require.True(t, waiting)
	require.False(t, marked())
	require.NotContains(t, labels(t, c), repos.KeyGrant)

	// Declared again: the grant starts over and has to age again.
	app.Datasets = []string{"dataset-clean"}
	_, err = declared.Reconcile(ctx, svc, []declared.App{app}, 0, ready)
	require.NoError(t, err)
	_, _ = grants(time.Now())
	require.False(t, marked())
	_, _ = grants(time.Now().Add(2 * time.Minute))
	require.True(t, marked())

	// The app is renamed: the dataset is still labelled with the old name, the new app does not
	// get it (it is not its own), and the old app's policy is deleted.
	renamed := declared.App{User: "kaos-app-yolo2", AccessKeyID: "AKIAJYOLO200000001Q", SecretAccessKey: "s", Datasets: []string{"dataset-clean"}}
	_, err = declared.Reconcile(ctx, svc, []declared.App{renamed}, 0, ready)
	require.NoError(t, err)
	waiting, _ = grants(time.Now().Add(2 * time.Minute))
	require.True(t, waiting)
	require.False(t, marked())
	require.False(t, allowed(t, svc, renamed.User, permissions.DeleteBranchAction, permissions.BranchArn("dataset-clean", "run-1")))
}

// labels reads dataset-clean's labels.
func labels(t *testing.T, c *catalog.Catalog) graveler.RepositoryMetadata {
	t.Helper()
	md, err := c.GetRepositoryMetadata(t.Context(), "dataset-clean")
	require.NoError(t, err)
	return md
}
