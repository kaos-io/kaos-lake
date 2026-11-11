// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-02): new file, Apache-2.0.

package api_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/treeverse/lakefs/pkg/api/apigen"
	"github.com/treeverse/lakefs/pkg/api/apiutil"
	"github.com/treeverse/lakefs/pkg/config"
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
	// PRD novelcore/kaos#1911 CON-02: nobody names a dataset's producer through the API.
	refused = set("dataset-cars", map[string]string{"kaos.producer": "yolo"})
	require.Equal(t, http.StatusBadRequest, refused.StatusCode())
	require.Contains(t, string(refused.Body), "kaos.producer is set by the platform only")
	require.Equal(t, http.StatusBadRequest, set("dataset-cars", map[string]string{"kaos.producer.filtered": "yolo"}).StatusCode())

	del, err := clt.DeleteRepositoryMetadataWithResponse(ctx, "dataset-cars", apigen.DeleteRepositoryMetadataJSONRequestBody{Keys: []string{"kaos.kind"}})
	require.NoError(t, err)
	require.Equal(t, http.StatusBadRequest, del.StatusCode())
	del, err = clt.DeleteRepositoryMetadataWithResponse(ctx, "dataset-cars", apigen.DeleteRepositoryMetadataJSONRequestBody{Keys: []string{"kaos.producer"}})
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

// A deleted repository leaves its storage namespace's _lakefs/dummy marker behind (lakeFS keeps the data), so
// the namespace cannot be reused: creating the same name at the same namespace again is refused. Creators
// therefore choose a new namespace for every creation (kaos-lib `_storage_namespace`, the UI's `newStorageNamespace`).
func TestController_RecreateSameNameNeedsNewStorageNamespace(t *testing.T) {
	clt, deps := setupClientWithAdmin(t)
	ctx := t.Context()
	create := func(name, ns string) *apigen.CreateRepositoryResponse {
		resp, err := clt.CreateRepositoryWithResponse(ctx, &apigen.CreateRepositoryParams{}, apigen.CreateRepositoryJSONRequestBody{
			DefaultBranch: apiutil.Ptr("main"), Name: name, StorageNamespace: onBlock(deps, ns),
		})
		require.NoError(t, err)
		return resp
	}
	const name = "dataset-recreate"

	first := create(name, "datasets/dataset-recreate-a1b2c3d4")
	require.Equal(t, http.StatusCreated, first.StatusCode(), string(first.Body))
	del, err := clt.DeleteRepositoryWithResponse(ctx, name, &apigen.DeleteRepositoryParams{})
	verifyResponseOK(t, del, err)

	// The old namespace is refused: this is the bug when both creators chose <prefix>/<name>.
	same := create(name, "datasets/dataset-recreate-a1b2c3d4")
	require.Equal(t, http.StatusBadRequest, same.StatusCode())
	require.Contains(t, string(same.Body), "storage namespace already in use")

	// A fresh namespace for the same name works, and so does deleting and recreating again.
	for _, id := range []string{"e5f6a7b8", "c9d0e1f2"} {
		again := create(name, "datasets/dataset-recreate-"+id)
		require.Equal(t, http.StatusCreated, again.StatusCode(), string(again.Body))
		del, err := clt.DeleteRepositoryWithResponse(ctx, name, &apigen.DeleteRepositoryParams{})
		verifyResponseOK(t, del, err)
	}
}

// Platform repositories (pkg/kaos/repos) are created through the catalog, which neither writes nor checks the
// dummy marker, so they come back at their old namespace after a delete.
func TestController_CatalogRecreateReusesNamespace(t *testing.T) {
	_, deps := setupClientWithAdmin(t)
	ctx := t.Context()
	const name = "dataset-default"
	ns := onBlock(deps, "datasets/dataset-default")
	for range 2 {
		_, err := deps.catalog.CreateRepository(ctx, name, config.SingleBlockstoreID, ns, "main", false)
		require.NoError(t, err)
		require.NoError(t, deps.catalog.DeleteRepository(ctx, name))
	}
}
