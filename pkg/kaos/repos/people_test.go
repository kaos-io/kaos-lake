// KAOS (kaos-lake, PRD novelcore/kaos#1911 CON-02): new file, Apache-2.0.

package repos_test

import (
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
	"github.com/treeverse/lakefs/pkg/kaos/repos"
	"github.com/treeverse/lakefs/pkg/kv/kvtest"
	"github.com/treeverse/lakefs/pkg/permissions"
)

// peopleStore is a multi-user store as a fresh stack has it (base groups and their policies, an
// admin), with one person in each group.
func peopleStore(t *testing.T) auth.Service {
	t.Helper()
	ctx := t.Context()
	svc := &multiuser.Service{AuthService: acl.NewAuthService(kvtest.GetStore(ctx, t), crypt.NewSecretStore([]byte("k")), authparams.ServiceCache{})}
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

	changes, err := repos.ReconcilePeople(ctx, f, svc)
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

	again, err := repos.ReconcilePeople(ctx, f, svc)
	require.NoError(t, err)
	require.Empty(t, again, "a second pass changes nothing")

	// The policy follows the label: a newly produced dataset closes, one handed back to people opens.
	f.md[uploaded][repos.KeyProducer] = "hera"
	delete(f.md[produced], repos.KeyProducer)
	_, err = repos.ReconcilePeople(ctx, f, svc)
	require.NoError(t, err)
	check("dev@example.com", writes(uploaded, false))
	check("dev@example.com", writes(produced, true))

	// A detached policy comes back.
	require.NoError(t, svc.DetachPolicyFromGroup(ctx, repos.ProducedPolicy, setup.DevelopersGroup))
	check("dev@example.com", writes(uploaded, true))
	_, err = repos.ReconcilePeople(ctx, f, svc)
	require.NoError(t, err)
	check("dev@example.com", writes(uploaded, false))
}
