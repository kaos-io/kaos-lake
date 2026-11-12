// KAOS (kaos-lake, PRD novelcore/kaos#1911 CON-02): new file, Apache-2.0.

package repos_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/treeverse/lakefs/contrib/auth/acl"
	"github.com/treeverse/lakefs/pkg/auth"
	"github.com/treeverse/lakefs/pkg/auth/crypt"
	"github.com/treeverse/lakefs/pkg/auth/model"
	"github.com/treeverse/lakefs/pkg/auth/multiuser"
	authparams "github.com/treeverse/lakefs/pkg/auth/params"
	"github.com/treeverse/lakefs/pkg/auth/setup"
	"github.com/treeverse/lakefs/pkg/graveler"
	"github.com/treeverse/lakefs/pkg/kaos/repos"
	"github.com/treeverse/lakefs/pkg/kv/kvtest"
	"github.com/treeverse/lakefs/pkg/logging"
	"github.com/treeverse/lakefs/pkg/permissions"
)

// peopleStore is a multi-user store as a fresh stack has it (base groups and their policies, an
// admin), with one person in each group.
func peopleStore(t *testing.T) auth.Service {
	t.Helper()
	return peopleStoreCached(t, authparams.ServiceCache{})
}

// peopleStoreCached is peopleStore with the auth cache the server runs with: a session keeps the
// policies it read for cache.TTL plus up to cache.Jitter.
func peopleStoreCached(t *testing.T, cache authparams.ServiceCache) auth.Service {
	t.Helper()
	ctx := t.Context()
	svc := &multiuser.Service{AuthService: acl.NewAuthService(kvtest.GetStore(ctx, t), crypt.NewSecretStore([]byte("k")), cache)}
	_, err := setup.CreateAdminUser(ctx, svc, &model.SuperuserConfiguration{
		User: model.User{Username: "admin", CreatedAt: time.Now()}, AccessKeyID: "AKIAJADMIN00000001Q", SecretAccessKey: "admin-secret",
	})
	require.NoError(t, err)
	for user, group := range map[string]string{
		"dev@example.com": setup.DevelopersGroup, "super@example.com": setup.SuperUsersGroup, "viewer@example.com": setup.ViewersGroup,
	} {
		_, err := svc.CreateUser(ctx, &model.User{Username: user, CreatedAt: time.Now()})
		require.NoError(t, err)
		require.NoError(t, svc.AddUserToGroup(ctx, user, group))
	}
	return svc
}

// PRD novelcore/kaos#1911 CON-02: the rights matrix for people. On a dataset a pipeline produces,
// people read and may name a version, and nothing else: no object, branch, commit, merge or k-*
// tag. On a dataset people upload, nothing changes. Admins are not restricted.
func TestReconcilePeople(t *testing.T) {
	ctx := t.Context()
	svc := peopleStore(t)
	f := newFake()
	f.add("dataset-clean", map[string]string{repos.KeyKind: "dataset", repos.KeyProducer: "yolo"})
	f.add("dataset-cars", map[string]string{repos.KeyKind: "dataset"})

	const produced, uploaded = "dataset-clean", "dataset-cars"
	branch, tag, obj := permissions.BranchArn, permissions.TagArn, permissions.ObjectArn
	type row struct {
		action, resource string
		want             bool
	}
	writes := func(repo string, want bool) []row {
		return []row{
			{permissions.WriteObjectAction, obj(repo, "images/1.jpg"), want},
			{permissions.DeleteObjectAction, obj(repo, "images/1.jpg"), want},
			{permissions.CreateBranchAction, branch(repo, "edit-1"), want},
			{permissions.CreateBranchAction, branch(repo, "run-7f3a"), want},
			{permissions.DeleteBranchAction, branch(repo, "run-7f3a"), want},
			{permissions.CreateCommitAction, branch(repo, "edit-1"), want},
			{permissions.CreateCommitAction, branch(repo, "main"), want}, // a merge, a cherry-pick
			{permissions.RevertBranchAction, branch(repo, "main"), want}, // a revert, a reset
			{permissions.CreateTagAction, tag(repo, "k-7f3a"), want},
			{permissions.DeleteTagAction, tag(repo, "k-7f3a"), want},
			{permissions.CreateCommitAction, permissions.RepoArn(repo), want}, // a commit record
		}
	}
	check := func(user string, rows []row) {
		t.Helper()
		for _, r := range rows {
			require.Equal(t, r.want, can(t, svc, user, r.action, r.resource), "%s: %s on %s", user, r.action, r.resource)
		}
	}
	reads := func(repo string) []row {
		return []row{
			{permissions.ReadObjectAction, obj(repo, "images/1.jpg"), true},
			{permissions.ListObjectsAction, permissions.RepoArn(repo), true},
			{permissions.ReadRepositoryAction, permissions.RepoArn(repo), true},
		}
	}
	names := func(repo string) []row {
		return []row{
			{permissions.CreateTagAction, tag(repo, "golden"), true},
			{permissions.DeleteTagAction, tag(repo, "golden"), true},
			{permissions.CreateTagAction, tag(repo, "v1"), true},
		}
	}

	pullRequests := func(repo string, want bool) []row {
		return []row{{permissions.WritePullRequestAction, permissions.RepoArn(repo), want}}
	}

	// Before the reconcile, editors write everything: the gap this closes.
	check("dev@example.com", writes(produced, true))
	check("dev@example.com", pullRequests(produced, true))

	_, changes, err := repos.ReconcilePeople(ctx, f, svc, nil)
	require.NoError(t, err)
	require.NotEmpty(t, changes)

	for _, editor := range []string{"dev@example.com", "super@example.com"} {
		check(editor, writes(produced, false))
		check(editor, reads(produced))
		check(editor, names(produced))
		check(editor, writes(uploaded, true))
	}
	// A pull request into a produced dataset could never merge: none is opened or changed there.
	check("dev@example.com", pullRequests(produced, false))
	check("dev@example.com", pullRequests(uploaded, true))
	check("viewer@example.com", writes(produced, false))
	check("viewer@example.com", reads(produced))
	check("admin", writes(produced, true))

	_, again, err := repos.ReconcilePeople(ctx, f, svc, nil)
	require.NoError(t, err)
	require.Empty(t, again, "a second pass changes nothing")

	// The policy follows the label: a newly produced dataset closes, one handed back to people opens.
	f.md[uploaded][repos.KeyProducer] = "hera"
	delete(f.md[produced], repos.KeyProducer)
	_, _, err = repos.ReconcilePeople(ctx, f, svc, nil)
	require.NoError(t, err)
	check("dev@example.com", writes(uploaded, false))
	check("dev@example.com", writes(produced, true))

	// A detached policy comes back.
	require.NoError(t, svc.DetachPolicyFromGroup(ctx, repos.ProducedPolicy, setup.DevelopersGroup))
	check("dev@example.com", writes(uploaded, true))
	_, _, err = repos.ReconcilePeople(ctx, f, svc, nil)
	require.NoError(t, err)
	check("dev@example.com", writes(uploaded, false))
}

// PRD novelcore/kaos#1911 CON-02: the policy names a declared dataset before its repository
// exists, and names one that exists without the label never. Its timestamp is stored with it: a
// second process (a restart, another replica) reads the same "since".
func TestReconcilePeople_ListsDeclaredDatasetsFirst(t *testing.T) {
	ctx := t.Context()
	svc := peopleStore(t)
	f := newFake()
	f.add("dataset-cars", map[string]string{repos.KeyKind: "dataset"}) // people's, and an app declares the name
	declared := []string{"dataset-cars", "dataset-new"}

	state, _, err := repos.ReconcilePeople(ctx, f, svc, declared)
	require.NoError(t, err)
	require.Equal(t, []string{"dataset-new"}, state.Listed)
	require.NotContains(t, f.repos, "dataset-new", "listing creates nothing")
	require.False(t, can(t, svc, "dev@example.com", permissions.WriteObjectAction, permissions.ObjectArn("dataset-new", "a")))
	require.False(t, can(t, svc, "dev@example.com", permissions.CreateBranchAction, permissions.BranchArn("dataset-new", "edit-1")))
	require.True(t, can(t, svc, "dev@example.com", permissions.WriteObjectAction, permissions.ObjectArn("dataset-cars", "a")))

	now := time.Now()
	require.False(t, state.Closed("dataset-new", 30*time.Second, now), "just listed")
	require.True(t, state.Closed("dataset-new", 30*time.Second, now.Add(31*time.Second)))
	require.False(t, state.Closed("dataset-cars", 0, now.Add(time.Hour)), "never listed, never closed")

	// Another process reads the same timestamp; nothing is rewritten.
	again, changes, err := repos.ReconcilePeople(ctx, f, svc, declared)
	require.NoError(t, err)
	require.Empty(t, changes)
	require.WithinDuration(t, state.Since, again.Since, time.Millisecond)

	// A change to the list, and a group that gets the policy back, both restart the clock.
	time.Sleep(5 * time.Millisecond)
	changed, _, err := repos.ReconcilePeople(ctx, f, svc, append(declared, "dataset-more"))
	require.NoError(t, err)
	require.True(t, changed.Since.After(state.Since))
	require.NoError(t, svc.DetachPolicyFromGroup(ctx, repos.ProducedPolicy, setup.DevelopersGroup))
	time.Sleep(5 * time.Millisecond)
	reattached, _, err := repos.ReconcilePeople(ctx, f, svc, append(declared, "dataset-more"))
	require.NoError(t, err)
	require.True(t, reattached.Since.After(changed.Since))

	// Created by the reconcile, it stays listed without a rewrite: by its label now.
	f.add("dataset-new", map[string]string{repos.KeyKind: "dataset", repos.KeyProducer: "yolo"})
	created, changes, err := repos.ReconcilePeople(ctx, f, svc, append(declared, "dataset-more"))
	require.NoError(t, err)
	require.Empty(t, changes)
	require.WithinDuration(t, reattached.Since, created.Since, time.Millisecond)
}

// PRD novelcore/kaos#1911 CON-02: an editor with a session open keeps the policies it read for the
// auth cache's lifetime, so a rule written when a dataset appears reaches them too late. The
// reconcile loop therefore lists the dataset first and creates it only when no session can still
// be without the rule: from the first instant the repository exists, the editor is refused.
func TestRun_PeopleAreKeptOutBeforeTheDatasetExists(t *testing.T) {
	const ttl, jitter, settle = 1500 * time.Millisecond, 300 * time.Millisecond, 2500 * time.Millisecond
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	svc := peopleStoreCached(t, authparams.ServiceCache{Enabled: true, Size: 100, TTL: ttl, Jitter: jitter})
	f := newFake()
	var declared atomic.Pointer[map[string]string]
	declared.Store(&map[string]string{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		repos.Run(ctx, f, svc, "", prefix, "", "", func() map[string]string { return nil },
			func() map[string]string { return *declared.Load() }, 50*time.Millisecond, settle, logging.ContextUnavailable())
	}()
	writes := func() bool {
		return can(t, svc, "dev@example.com", permissions.WriteObjectAction, permissions.ObjectArn("dataset-new", "people.jpg")) ||
			can(t, svc, "dev@example.com", permissions.CreateBranchAction, permissions.BranchArn("dataset-new", "edit-1")) ||
			can(t, svc, "dev@example.com", permissions.CreateCommitAction, permissions.BranchArn("dataset-new", "main"))
	}
	exists := func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.repos["dataset-new"] != nil
	}
	require.True(t, writes(), "an editor with a session open, before anything is declared")

	declaredAt := time.Now()
	declared.Store(&map[string]string{"dataset-new": "yolo"})
	staleAfterListing, createdAt := false, time.Time{}
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		there := exists() // read before asking: a refusal after this is a refusal while it exists
		allowed := writes()
		if !there {
			staleAfterListing = staleAfterListing || (allowed && time.Since(declaredAt) > 500*time.Millisecond)
			continue
		}
		if createdAt.IsZero() {
			createdAt = time.Now()
		}
		require.False(t, allowed, "the editor writes a produced dataset %s after it appeared", time.Since(createdAt))
		if time.Since(createdAt) > 2*time.Second {
			break
		}
	}
	require.False(t, createdAt.IsZero(), "the dataset was never created")
	require.True(t, staleAfterListing, "the session did keep its old policies after the rule was written: the order is what protects")
	require.GreaterOrEqual(t, createdAt.Sub(declaredAt), settle, "created only after the listing settled")
	require.Equal(t, graveler.RepositoryMetadata{repos.KeyKind: "dataset", repos.KeyProducer: "yolo"}, f.md["dataset-new"])
	cancel()
	<-done
}
