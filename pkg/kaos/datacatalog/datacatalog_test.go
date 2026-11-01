// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-07): new file, Apache-2.0.

package datacatalog_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/treeverse/lakefs/pkg/catalog"
	"github.com/treeverse/lakefs/pkg/graveler"
	"github.com/treeverse/lakefs/pkg/kaos/datacatalog"
)

type fake struct{}

var repos = map[string]graveler.RepositoryMetadata{
	"dataset-cars":    {"kaos.kind": "dataset", "kaos.location": "europe-west1 · Belgium", "kaos.description": "cars"},
	"dataset-default": {"kaos.kind": "dataset", "kaos.default": "true"},
	"dataset-draft":   {}, // a dataset- name nobody labelled: not offered
}

func (fake) ListRepositories(_ context.Context, _ int, prefix, _, _ string, _ ...catalog.ListRepositoriesOptionsFunc) ([]*catalog.Repository, bool, error) {
	return []*catalog.Repository{{Name: "dataset-cars", DefaultBranch: "main"}, {Name: "dataset-default", DefaultBranch: "main"},
		{Name: "dataset-draft", DefaultBranch: "main"}}, false, nil
}
func (fake) GetRepositoryMetadata(_ context.Context, r string) (graveler.RepositoryMetadata, error) {
	return repos[r], nil
}
func (fake) ListTags(_ context.Context, r, _ string, _ int, _ string) ([]*catalog.Tag, bool, error) {
	if r != "dataset-cars" {
		return nil, false, nil
	}
	return []*catalog.Tag{{ID: "v2", CommitID: "c2"}, {ID: "v10", CommitID: "c10"}, {ID: "vnext", CommitID: "x"}}, false, nil
}
func (fake) GetBranchReference(_ context.Context, r, b string) (string, error) { return "head-" + r, nil }

func TestList(t *testing.T) {
	got, err := datacatalog.List(t.Context(), fake{})
	require.NoError(t, err)
	require.Equal(t, []datacatalog.Dataset{
		{Name: "dataset-cars", Description: "cars", Location: "europe-west1 · Belgium", Latest: "head-dataset-cars",
			Versions: []datacatalog.Version{{Tag: "v10", Commit: "c10"}, {Tag: "v2", Commit: "c2"}}},
		{Name: "dataset-default", Default: true, Latest: "head-dataset-default", Versions: []datacatalog.Version{}},
	}, got)
}

// D-05: only a service-account token the workload check accepts for the project's namespaces.
func TestHandler(t *testing.T) {
	h := datacatalog.Handler{Catalog: fake{}, Namespaces: []string{"vision-ci"},
		Verify: func(_ context.Context, token string, ns []string) (string, error) {
			if token == "ci-token" && ns[0] == "vision-ci" {
				return "vision-ci", nil
			}
			return "", errors.New("denied")
		}}
	do := func(auth string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/kaos/v1/catalog", nil)
		if auth != "" {
			r.Header.Set("Authorization", auth)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec
	}
	require.Equal(t, http.StatusUnauthorized, do("").Code)
	require.Equal(t, http.StatusUnauthorized, do("Bearer other").Code)
	ok := do("Bearer ci-token")
	require.Equal(t, http.StatusOK, ok.Code)
	var body struct{ Datasets []datacatalog.Dataset }
	require.NoError(t, json.Unmarshal(ok.Body.Bytes(), &body))
	require.Len(t, body.Datasets, 2)
}
