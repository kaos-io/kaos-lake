// KAOS (kaos-lake, PRD novelcore/kaos#1911 CON-03): new file, Apache-2.0.

package graveler_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/treeverse/lakefs/pkg/graveler"
	"github.com/treeverse/lakefs/pkg/graveler/testutil"
)

// A branch protected from commits takes no commit by another door: a cherry-pick, a revert and an
// import each put a new commit at its head, and are refused like a direct commit, before anything
// is read or written.
func TestGraveler_ProtectedBranchRefusesEveryCommit(t *testing.T) {
	ctx := t.Context()
	refs := &testutil.RefsFake{RefType: graveler.ReferenceTypeBranch, StagingToken: "token",
		Commits: map[graveler.CommitID]*graveler.Commit{"": {}}}
	g := newGraveler(t, &testutil.CommittedFake{}, &testutil.StagingFake{}, refs, nil, testutil.NewProtectedBranchesManagerFake("main"))
	repo := &graveler.RepositoryRecord{RepositoryID: "repo", Repository: &graveler.Repository{}}

	_, err := g.CherryPick(ctx, repo, "main", "c1", nil, "tester", nil)
	require.ErrorIs(t, err, graveler.ErrCommitToProtectedBranch, "cherry-pick")
	_, err = g.Revert(ctx, repo, "main", "c1", 0, graveler.CommitParams{}, nil)
	require.ErrorIs(t, err, graveler.ErrCommitToProtectedBranch, "revert")
	_, err = g.Import(ctx, repo, "main", "mr1", graveler.CommitParams{}, nil)
	require.ErrorIs(t, err, graveler.ErrCommitToProtectedBranch, "import")

}
