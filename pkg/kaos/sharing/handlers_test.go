// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-12): new file, Apache-2.0.

package sharing_test

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/treeverse/lakefs/pkg/auth"
	"github.com/treeverse/lakefs/pkg/auth/model"
	"github.com/treeverse/lakefs/pkg/catalog"
	"github.com/treeverse/lakefs/pkg/graveler"
	"github.com/treeverse/lakefs/pkg/kaos/datacatalog"
	"github.com/treeverse/lakefs/pkg/kaos/repos"
	"github.com/treeverse/lakefs/pkg/kaos/sharing"
)

// lake is a fake lakeFS catalog: what repos.ReconcileShares and the datacatalog read.
type lake struct {
	mu sync.Mutex
	md map[string]graveler.RepositoryMetadata
}

func newLake(md map[string]map[string]string) *lake {
	l := &lake{md: map[string]graveler.RepositoryMetadata{}}
	for name, m := range md {
		l.md[name] = maps.Clone(m)
	}
	return l
}

func (l *lake) GetRepository(_ context.Context, name string) (*catalog.Repository, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.md[name]; !ok {
		return nil, graveler.ErrRepositoryNotFound
	}
	return &catalog.Repository{Name: name, DefaultBranch: "main"}, nil
}

func (l *lake) CreateRepository(context.Context, string, string, string, string, bool) (*catalog.Repository, error) {
	return nil, errors.New("not in this test")
}

func (l *lake) GetCommit(context.Context, string, string) (*catalog.CommitLog, error) {
	return &catalog.CommitLog{Parents: []string{"initial"}}, nil
}

func (l *lake) GetRepositoryMetadata(_ context.Context, name string) (graveler.RepositoryMetadata, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return maps.Clone(l.md[name]), nil
}

func (l *lake) UpdateRepositoryMetadata(_ context.Context, name string, md graveler.RepositoryMetadata) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	maps.Copy(l.md[name], md)
	return nil
}

func (l *lake) DeleteRepositoryMetadata(_ context.Context, name string, keys []string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, k := range keys {
		delete(l.md[name], k)
	}
	return nil
}

func (l *lake) ListRepositories(_ context.Context, limit int, prefix, _, after string, _ ...catalog.ListRepositoriesOptionsFunc) ([]*catalog.Repository, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var page []*catalog.Repository
	for _, n := range slices.Sorted(maps.Keys(l.md)) {
		if strings.HasPrefix(n, prefix) && n > after {
			page = append(page, &catalog.Repository{Name: n, DefaultBranch: "main"})
		}
	}
	if len(page) > limit {
		return page[:limit], true, nil
	}
	return page, false, nil
}

func (l *lake) ListTags(_ context.Context, repo, _ string, _ int, _ string) ([]*catalog.Tag, bool, error) {
	return []*catalog.Tag{{ID: "v1", CommitID: "c1-" + repo}}, false, nil
}

func (l *lake) GetBranchReference(_ context.Context, repo, _ string) (string, error) {
	return "head-" + repo, nil
}

// ownerLake is project cars's lakeFS: datasets shared with vision, orbits, both, or nobody.
func ownerLake() *lake {
	return newLake(map[string]map[string]string{
		"dataset-roads":   {repos.KeyKind: "dataset", repos.KeySharedWith: "vision, orbits", repos.KeyDescription: "roads"},
		"dataset-signs":   {repos.KeyKind: "dataset", repos.KeySharedWith: "orbits"},
		"dataset-default": {repos.KeyKind: "dataset", repos.KeyDefault: "true", repos.KeySharedWith: "vision"},
		"dataset-private": {repos.KeyKind: "dataset"},
		"dataset-draft":   {repos.KeySharedWith: "vision"}, // not labelled a dataset (yet)
		"outputs-yolo":    {repos.KeyKind: "outputs", repos.KeyApp: "yolo", repos.KeySharedWith: "vision"},
	})
}

type groups map[string][]string

func (g groups) ListUserGroups(_ context.Context, user string, _ *model.PaginationParams) ([]*model.Group, *model.Paginator, error) {
	if user == "broken" {
		return nil, nil, errors.New("store down")
	}
	var out []*model.Group
	for _, name := range g[user] {
		out = append(out, &model.Group{DisplayName: name})
	}
	return out, &model.Paginator{}, nil
}

func serve(h http.Handler, user string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if user != "" {
		r = r.WithContext(auth.WithUser(r.Context(), &model.User{Username: user}))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func names(t *testing.T, rec *httptest.ResponseRecorder) (string, []string) {
	t.Helper()
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	var body sharing.SharedResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	var out []string
	for _, d := range body.Datasets {
		require.False(t, d.Default, "a shared dataset is never the consumer's default")
		require.Empty(t, d.Owner, "the owner side names the owner once, at the top")
		out = append(out, d.Name)
	}
	return body.Owner, out
}

func TestSharedHandler(t *testing.T) {
	h := sharing.SharedHandler{Catalog: ownerLake(), Owner: "cars", Groups: groups{
		"vision-ml@acme":   {repos.ConsumerGroupPrefix + "vision"},
		"orbits-ml@acme":   {repos.ConsumerGroupPrefix + "orbits"},
		"both@acme":        {repos.ConsumerGroupPrefix + "orbits", repos.ConsumerGroupPrefix + "vision", "Viewers"},
		"admin@cars":       {"Admins"},
		"dev@cars":         {"Developers"},
		"kaos-app-yolo":    nil, // a pipeline of cars: its own policy, no groups
		"odd@acme":         {repos.ConsumerGroupPrefix, repos.ConsumerGroupPrefix + "Bad_Name"},
		"cars-and-vision@": {"Admins", repos.ConsumerGroupPrefix + "vision"},
	}}

	// Only datasets (dataset-*, kaos.kind=dataset) shared with the caller's consumer projects.
	owner, got := names(t, serve(h, "vision-ml@acme"))
	require.Equal(t, "cars", owner)
	require.Equal(t, []string{"dataset-default", "dataset-roads"}, got)
	rec := serve(h, "vision-ml@acme")
	require.NotContains(t, rec.Body.String(), "orbits", "another consumer is never revealed")
	require.NotContains(t, rec.Body.String(), "shared_with")
	require.NotContains(t, rec.Body.String(), "outputs-")

	_, got = names(t, serve(h, "orbits-ml@acme"))
	require.Equal(t, []string{"dataset-roads", "dataset-signs"}, got)
	require.NotContains(t, serve(h, "orbits-ml@acme").Body.String(), "vision")
	_, got = names(t, serve(h, "both@acme"))
	require.Equal(t, []string{"dataset-default", "dataset-roads", "dataset-signs"}, got)

	// No consumer group: 401, whoever the caller is here.
	for _, user := range []string{"admin@cars", "dev@cars", "kaos-app-yolo", "odd@acme", "nobody@acme"} {
		require.Equal(t, http.StatusUnauthorized, serve(h, user).Code, user)
	}
	// An owner-project person who is also in a consumer group sees only what that group gets.
	_, got = names(t, serve(h, "cars-and-vision@"))
	require.Equal(t, []string{"dataset-default", "dataset-roads"}, got)

	require.Equal(t, http.StatusUnauthorized, serve(h, "").Code, "not signed in")
	require.Equal(t, http.StatusInternalServerError, serve(h, "broken").Code)
}

// A consumer group with nothing left in it answers an empty list, never null.
func TestSharedHandler_Empty(t *testing.T) {
	h := sharing.SharedHandler{Catalog: newLake(nil), Owner: "cars",
		Groups: groups{"v@acme": {repos.ConsumerGroupPrefix + "vision"}}}
	rec := serve(h, "v@acme")
	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"owner":"cars","datasets":[]}`, rec.Body.String())
}

func TestPeersHandler(t *testing.T) {
	path := filepath.Join(t.TempDir(), "peers.json")
	write(t, path, peersJSON)
	f := sharing.NewPeersFile(path, "vision")
	_, err := f.Load()
	require.NoError(t, err)

	for _, user := range []string{"kaos-app-yolo", "alice@acme.com"} { // an app key, a person
		rec := serve(sharing.PeersHandler{File: f}, user)
		require.Equal(t, http.StatusOK, rec.Code)
		require.JSONEq(t, `{"project":"vision","peers":[
		 {"project":"cars","endpoint":"https://lakefs-cars.acme.kaos.eu","url":"https://lakefs-cars.acme.kaos.eu"},
		 {"project":"orbits","endpoint":"http://orbits-lakefs.ml-orbits.svc.cluster.local:80","url":"https://lakefs-orbits.acme.kaos.eu"}]}`,
			rec.Body.String())
	}
	off := serve(sharing.PeersHandler{}, "alice@acme.com")
	require.Equal(t, http.StatusOK, off.Code)
	require.JSONEq(t, `{"project":"","peers":[]}`, off.Body.String(), "sharing off: no peers")
	require.Equal(t, http.StatusUnauthorized, serve(sharing.PeersHandler{File: f}, "").Code)
}

// The datacatalog's SharedWith reads labels only: never another consumer, never the default.
func TestSharedWith(t *testing.T) {
	got, err := datacatalog.SharedWith(t.Context(), ownerLake(), []string{"vision"})
	require.NoError(t, err)
	require.Equal(t, []datacatalog.Dataset{
		{Name: "dataset-default", Latest: "head-dataset-default", Versions: []datacatalog.Version{{Tag: "v1", Commit: "c1-dataset-default"}}},
		{Name: "dataset-roads", Description: "roads", Latest: "head-dataset-roads", Versions: []datacatalog.Version{{Tag: "v1", Commit: "c1-dataset-roads"}}},
	}, got)
	got, err = datacatalog.SharedWith(t.Context(), ownerLake(), nil)
	require.NoError(t, err)
	require.Empty(t, got)
}
