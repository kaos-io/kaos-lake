// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-02): new file, Apache-2.0.

package api_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/treeverse/lakefs/pkg/api/apigen"
	"github.com/treeverse/lakefs/pkg/api/apiutil"
)

// CON-02: the metadata API keeps repository labels meaningful and says why it refuses one.
func TestController_RepositoryLabels(t *testing.T) {
	clt, deps := setupClientWithAdmin(t)
	ctx := t.Context()
	create := func(name string) {
		resp, err := clt.CreateRepositoryWithResponse(ctx, &apigen.CreateRepositoryParams{}, apigen.CreateRepositoryJSONRequestBody{
			DefaultBranch: apiutil.Ptr("main"), Name: name, StorageNamespace: onBlock(deps, name),
		})
		verifyResponseOK(t, resp, err)
	}
	set := func(name string, md map[string]string) *apigen.SetRepositoryMetadataResponse {
		resp, err := clt.SetRepositoryMetadataWithResponse(ctx, name, apigen.SetRepositoryMetadataJSONRequestBody{
			Metadata: apigen.RepositoryMetadataSet_Metadata{AdditionalProperties: md},
		})
		require.NoError(t, err)
		return resp
	}
	create("dataset-cars")
	create("plain-repo")

	require.Equal(t, http.StatusNoContent, set("dataset-cars", map[string]string{"kaos.kind": "dataset", "kaos.description": "cars"}).StatusCode())
	refused := set("plain-repo", map[string]string{"kaos.kind": "dataset"})
	require.Equal(t, http.StatusBadRequest, refused.StatusCode())
	require.Contains(t, string(refused.Body), "dataset-")
	require.Equal(t, http.StatusBadRequest, set("dataset-cars", map[string]string{"kaos.default": "true"}).StatusCode())

	del, err := clt.DeleteRepositoryMetadataWithResponse(ctx, "dataset-cars", apigen.DeleteRepositoryMetadataJSONRequestBody{Keys: []string{"kaos.kind"}})
	require.NoError(t, err)
	require.Equal(t, http.StatusBadRequest, del.StatusCode())
	del, err = clt.DeleteRepositoryMetadataWithResponse(ctx, "dataset-cars", apigen.DeleteRepositoryMetadataJSONRequestBody{Keys: []string{"kaos.description"}})
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, del.StatusCode())
}

// D-09: an admin shares a dataset with other projects; non-datasets are not shared.
func TestController_DatasetSharing(t *testing.T) {
	clt, deps := setupClientWithAdmin(t)
	ctx := t.Context()
	for _, name := range []string{"dataset-shared", "plain-shared"} {
		resp, err := clt.CreateRepositoryWithResponse(ctx, &apigen.CreateRepositoryParams{}, apigen.CreateRepositoryJSONRequestBody{
			DefaultBranch: apiutil.Ptr("main"), Name: name, StorageNamespace: onBlock(deps, name),
		})
		verifyResponseOK(t, resp, err)
	}
	share := func(name string) int {
		resp, err := clt.SetRepositoryMetadataWithResponse(ctx, name, apigen.SetRepositoryMetadataJSONRequestBody{
			Metadata: apigen.RepositoryMetadataSet_Metadata{AdditionalProperties: map[string]string{"kaos.shared_with": "orbits"}},
		})
		require.NoError(t, err)
		return resp.StatusCode()
	}
	require.Equal(t, http.StatusNoContent, share("dataset-shared"))
	require.Equal(t, http.StatusBadRequest, share("plain-shared"))
}
