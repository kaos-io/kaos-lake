// KAOS (kaos-lake, PRD novelcore/kaos#1911 CON-02, D-03): new file, Apache-2.0.

package api_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/treeverse/lakefs/pkg/api/apigen"
	"github.com/treeverse/lakefs/pkg/api/apiutil"
	"github.com/treeverse/lakefs/pkg/config"
	"github.com/treeverse/lakefs/pkg/graveler"
	"github.com/treeverse/lakefs/pkg/kaos/repos"
)

// PRD novelcore/kaos#1911 CON-02: nobody creates, through the API, a repository whose name an app
// declares as a dataset it produces: the platform creates it, labelled. Other names are free.
func TestController_CreateRepositoryRefusesDeclaredDatasets(t *testing.T) {
	clt, deps := setupClientWithAdmin(t)
	ctx := t.Context()
	repos.Declare(func() map[string]string { return map[string]string{"dataset-clean": "yolo"} })
	t.Cleanup(func() { repos.Declare(func() map[string]string { return nil }) })
	create := func(name string, bare bool) *apigen.CreateRepositoryResponse {
		resp, err := clt.CreateRepositoryWithResponse(ctx, &apigen.CreateRepositoryParams{Bare: &bare}, apigen.CreateRepositoryJSONRequestBody{
			DefaultBranch: apiutil.Ptr("main"), Name: name, StorageNamespace: onBlock(deps, name),
		})
		require.NoError(t, err)
		return resp
	}
	for _, bare := range []bool{false, true} {
		resp := create("dataset-clean", bare)
		require.Equal(t, http.StatusBadRequest, resp.StatusCode(), string(resp.Body))
		require.Contains(t, resp.JSON400.Message, "dataset-clean is a dataset produced by yolo")
	}
	_, err := deps.catalog.GetRepository(ctx, "dataset-clean")
	require.ErrorIs(t, err, graveler.ErrNotFound)
	require.Equal(t, http.StatusCreated, create("dataset-undeclared", false).StatusCode())
}

// PRD novelcore/kaos#1911 D-03: a dataset a pipeline produces has commits, latest and names, never
// numbered releases. A vN tag there is refused for every caller; a name is not, and vN on a
// dataset people upload is as before.
func TestController_NoNumberedVersionsOnProducedDatasets(t *testing.T) {
	clt, deps := setupClientWithAdmin(t)
	ctx := t.Context()
	heads := map[string]string{}
	for _, repo := range []string{"dataset-clean", "dataset-cars"} {
		_, err := deps.catalog.CreateRepository(ctx, repo, config.SingleBlockstoreID, onBlock(deps, repo), "main", false)
		require.NoError(t, err)
		heads[repo] = mainHeadOf(t, deps, repo)
	}
	require.NoError(t, deps.catalog.UpdateRepositoryMetadata(ctx, "dataset-clean", map[string]string{"kaos.kind": "dataset", "kaos.producer": "yolo"}))
	tag := func(repo, id string) *apigen.CreateTagResponse {
		resp, err := clt.CreateTagWithResponse(ctx, repo, apigen.CreateTagJSONRequestBody{Id: id, Ref: heads[repo]})
		require.NoError(t, err)
		return resp
	}
	for _, id := range []string{"v1", "v12"} {
		resp := tag("dataset-clean", id)
		require.Equal(t, http.StatusBadRequest, resp.StatusCode(), id)
		require.Contains(t, resp.JSON400.Message, "vN is for datasets people upload")
	}
	_, err := deps.catalog.GetTag(ctx, "dataset-clean", "v1")
	require.ErrorIs(t, err, graveler.ErrNotFound)
	for _, id := range []string{"golden", "v1.0", "vnext", "release-v1"} {
		require.Equal(t, http.StatusCreated, tag("dataset-clean", id).StatusCode(), id)
	}
	require.Equal(t, http.StatusCreated, tag("dataset-cars", "v1").StatusCode())
}

func mainHeadOf(t *testing.T, deps *dependencies, repo string) string {
	t.Helper()
	head, err := deps.catalog.GetBranchReference(t.Context(), repo, "main")
	require.NoError(t, err)
	return head
}
