// KAOS (kaos-lake, PRD novelcore/kaos#1911 CON-03): new file, Apache-2.0.

package api_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/treeverse/lakefs/pkg/api/apigen"
	"github.com/treeverse/lakefs/pkg/catalog"
	"github.com/treeverse/lakefs/pkg/config"
	"github.com/treeverse/lakefs/pkg/graveler"
)

// A main protected from commits moves only by a merge: a cherry-pick and a revert are answered
// 403 like a direct commit and leave main where it was. Other branches are unaffected.
func TestController_ProtectedBranchRefusesCherryPickAndRevert(t *testing.T) {
	clt, deps := setupClientWithAdmin(t)
	ctx := t.Context()
	repo := testUniqueRepoName()
	_, err := deps.catalog.CreateRepository(ctx, repo, config.SingleBlockstoreID, onBlock(deps, repo), "main", false)
	require.NoError(t, err)
	commit := func(branch, path string) string {
		require.NoError(t, deps.catalog.CreateEntry(ctx, repo, branch, catalog.DBEntry{
			Path: path, PhysicalAddress: path + "-addr", CreationDate: time.Now(), Size: 1, Checksum: path,
		}))
		c, err := deps.catalog.Commit(ctx, repo, branch, "add "+path, DefaultUserID, nil, nil, nil, false)
		require.NoError(t, err)
		return c.Reference
	}
	first := commit("main", "first.txt")
	_, err = deps.catalog.CreateBranch(ctx, repo, "work", "main")
	require.NoError(t, err)
	picked := commit("work", "picked.txt")
	_, err = deps.catalog.CreateBranch(ctx, repo, "open", "main")
	require.NoError(t, err)

	require.NoError(t, deps.catalog.SetBranchProtectionRules(ctx, repo, &graveler.BranchProtectionRules{
		BranchPatternToBlockedActions: map[string]*graveler.BranchProtectionBlockedActions{
			"main": {Value: []graveler.BranchProtectionBlockedAction{
				graveler.BranchProtectionBlockedAction_STAGING_WRITE, graveler.BranchProtectionBlockedAction_COMMIT,
			}},
		},
	}, nil))
	head := func(branch string) string {
		ref, err := deps.catalog.GetBranchReference(ctx, repo, branch)
		require.NoError(t, err)
		return ref
	}
	before := head("main")

	direct, err := clt.CommitWithResponse(ctx, repo, "main", &apigen.CommitParams{}, apigen.CommitJSONRequestBody{Message: "direct"})
	require.NoError(t, err)
	require.Equal(t, http.StatusForbidden, direct.StatusCode(), "the reference: a direct commit")

	pick, err := clt.CherryPickWithResponse(ctx, repo, "main", apigen.CherryPickJSONRequestBody{Ref: picked})
	require.NoError(t, err)
	require.Equal(t, http.StatusForbidden, pick.StatusCode(), string(pick.Body))
	require.Equal(t, string(direct.Body), string(pick.Body), "the same refusal as a direct commit")

	revert, err := clt.RevertBranchWithResponse(ctx, repo, "main", apigen.RevertBranchJSONRequestBody{Ref: first})
	require.NoError(t, err)
	require.Equal(t, http.StatusForbidden, revert.StatusCode(), string(revert.Body))
	require.Equal(t, string(direct.Body), string(revert.Body))

	require.Equal(t, before, head("main"), "main did not move")

	// An unprotected branch still takes both, and a merge still lands on the protected main.
	pick, err = clt.CherryPickWithResponse(ctx, repo, "open", apigen.CherryPickJSONRequestBody{Ref: picked})
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, pick.StatusCode(), string(pick.Body))
	revert, err = clt.RevertBranchWithResponse(ctx, repo, "open", apigen.RevertBranchJSONRequestBody{Ref: picked})
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, revert.StatusCode(), string(revert.Body))
	merge, err := clt.MergeIntoBranchWithResponse(ctx, repo, "work", "main", apigen.MergeIntoBranchJSONRequestBody{})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, merge.StatusCode(), string(merge.Body))
}
