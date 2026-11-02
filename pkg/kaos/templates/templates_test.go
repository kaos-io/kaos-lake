// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-04/F-05; dataset layouts): new file, Apache-2.0.

package templates_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"

	"github.com/treeverse/lakefs/pkg/auth"
	"github.com/treeverse/lakefs/pkg/auth/model"
	"github.com/treeverse/lakefs/pkg/block"
	"github.com/treeverse/lakefs/pkg/cache"
	"github.com/treeverse/lakefs/pkg/catalog"
	"github.com/treeverse/lakefs/pkg/config"
	"github.com/treeverse/lakefs/pkg/graveler/settings"
	"github.com/treeverse/lakefs/pkg/kaos/templates"
	"github.com/treeverse/lakefs/pkg/kv/kvtest"
	"github.com/treeverse/lakefs/pkg/kv/mem"
	"github.com/treeverse/lakefs/pkg/upload"
)

const samples = `layout: 1
name: samples
title: Samples
version: "3"
parameters:
  - {key: classes, label: Classes, type: list, required: true, form_key: classes}
form_file: meta.yaml
skeleton:
  folders: [data]
  files:
    meta.yaml: "classes:\n{{#each classes}}\n  - {{@item|quote}}\n{{/each}}\n"
rules:
  required: [meta.yaml, "data/*.json"]
  lines: [{files: "data/*.json", pattern: '\{.*\}', message: one JSON object per line}]
hook: team_layouts.samples
`

// realCatalog is a lakeFS catalog over in-memory storage.
func realCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	ctx := context.WithoutCancel(t.Context())
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set(config.BlockstoreTypeKey, block.BlockstoreTypeMem)
	viper.Set("database.type", mem.DriverName)
	viper.Set("committed.local_cache.size_bytes", 24*1024*1024)
	viper.Set("committed.sstable.memory.cache_size_bytes", 2*1024*1024)
	cfg := &config.ConfigImpl{}
	_, err := config.NewConfig("", cfg)
	require.NoError(t, err)
	cfg.Committed.LocalCache.Dir = path.Join(t.TempDir(), "cache")
	c, err := catalog.New(ctx, catalog.Config{
		Config: cfg, KVStore: kvtest.GetStore(ctx, t), SettingsManagerOption: settings.WithCache(cache.NoCache),
		PathProvider: upload.DefaultPathProvider,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// commitFiles makes repo (if missing) and commits files to main.
func commitFiles(t *testing.T, c *catalog.Catalog, repo string, files map[string]string) {
	t.Helper()
	ctx := t.Context()
	ns := "mem://" + repo
	if _, err := c.GetRepository(ctx, repo); err != nil {
		_, err := c.CreateRepository(ctx, repo, "", ns, "main", false)
		require.NoError(t, err)
	}
	for p, content := range files {
		id := "obj-" + strings.ReplaceAll(p, "/", "_")
		_, err := c.BlockAdapter.Put(ctx, block.ObjectPointer{StorageNamespace: ns, Identifier: id,
			IdentifierType: block.IdentifierTypeRelative}, int64(len(content)), strings.NewReader(content), block.PutOpts{})
		require.NoError(t, err)
		require.NoError(t, c.CreateEntry(ctx, repo, "main", catalog.DBEntry{Path: p, PhysicalAddress: id,
			AddressType: catalog.AddressTypeRelative, CreationDate: time.Now(), Size: int64(len(content)), Checksum: id}))
	}
	_, err := c.Commit(ctx, repo, "main", "files", "tester", nil, nil, nil, false)
	require.NoError(t, err)
}

func names(ls []templates.Layout) []string {
	var out []string
	for _, l := range ls {
		out = append(out, l.Name+":"+l.Source)
	}
	return out
}

func TestBuiltins(t *testing.T) {
	all, problems, err := templates.All(t.Context(), nil)
	require.NoError(t, err)
	require.Empty(t, problems)
	require.Equal(t, []string{"raw:builtin", "yolo:builtin"}, names(all))
	yolo := all[1]
	require.Equal(t, "YOLO (txt annotations)", yolo.Title)
	require.Equal(t, "data.yaml", yolo.FormFile)
	require.Equal(t, "kaos_lib.layouts.yolo", yolo.Hook)
	require.Contains(t, yolo.Files, "labels/val/.keep")
	require.Equal(t, []templates.Param{{Key: "classes", Label: "Class names", Type: "list", Required: true,
		Help: "One per line, in label-id order: the first class is id 0.", FormKey: "names"}}, yolo.Parameters)
}

// Datasets labelled with the old id keep reading, rendering, parsing and checking as yolo; the old id
// is never listed and never written.
func TestLegacyIDResolvesToYolo(t *testing.T) {
	ctx := t.Context()
	require.Equal(t, "yolo", templates.Canonical("ultralytics"))
	l, err := templates.Get(ctx, nil, "ultralytics")
	require.NoError(t, err)
	require.Equal(t, "yolo", l.Name)
	files, err := templates.Render(l, "dataset-cars", "", map[string]any{"classes": []any{"car"}})
	require.NoError(t, err)
	require.Contains(t, files[1].Content, `0: "car"`)
	all, _, _ := templates.All(ctx, nil)
	require.NotContains(t, names(all), "ultralytics:builtin")

	c := realCatalog(t)
	commitFiles(t, c, "layouts", map[string]string{"ultralytics/layout.yaml": strings.ReplaceAll(samples, "name: samples", "name: ultralytics")})
	_, problems, err := templates.All(ctx, templates.CatalogStore{Catalog: c, Blocks: c.BlockAdapter})
	require.NoError(t, err)
	require.Equal(t, "ultralytics is a built-in layout's name", problems[0].Error)
}

func TestRenderRefusesNonDatasetNamesAndUnknownLayouts(t *testing.T) {
	l, err := templates.Get(t.Context(), nil, "raw")
	require.NoError(t, err)
	_, err = templates.Render(l, "cars", "", nil)
	require.ErrorIs(t, err, templates.ErrInvalid)
	require.Contains(t, err.Error(), "dataset-")
	for _, bad := range []string{"nope", "../yolo", "yolo/x"} {
		_, err := templates.Get(t.Context(), nil, bad)
		require.ErrorIs(t, err, templates.ErrInvalid, bad)
	}
}

// The project's layouts come from main of its "layouts" repository; bad ones are reported, not served.
func TestProjectLayouts(t *testing.T) {
	ctx := t.Context()
	c := realCatalog(t)
	store := templates.CatalogStore{Catalog: c, Blocks: c.BlockAdapter}
	all, problems, err := templates.All(ctx, store)
	require.NoError(t, err)
	require.Empty(t, problems)
	require.Len(t, all, 2, "no layouts repository: built-ins only")

	commitFiles(t, c, "layouts", map[string]string{
		"samples/layout.yaml":  samples,
		"README.md":            "ignored",
		"samples/notes.txt":    "ignored",
		"misnamed/layout.yaml": samples,
		"raw/layout.yaml":      strings.ReplaceAll(samples, "name: samples", "name: raw"),
		"broken/layout.yaml":   "layout: 1\nname: broken\n",
		"deep/x/layout.yaml":   samples,
	})
	all, problems, err = templates.All(ctx, store)
	require.NoError(t, err)
	require.Equal(t, []string{"raw:builtin", "yolo:builtin", "samples:project"}, names(all))
	require.Equal(t, []templates.Problem{
		{Path: "layouts/broken/layout.yaml", Error: "layouts/broken/layout.yaml is not a valid layout: (root): title is required"},
		{Path: "layouts/misnamed/layout.yaml", Error: "its name samples is not its folder's name misnamed"},
		{Path: "layouts/raw/layout.yaml", Error: "raw is a built-in layout's name"},
	}, problems)
	s := all[2]
	require.Equal(t, "3", s.Version)
	require.Equal(t, "team_layouts.samples", s.Hook)
	require.Equal(t, []string{"data/.keep", "meta.yaml"}, s.Files)

	l, err := templates.Get(ctx, store, "samples")
	require.NoError(t, err)
	files, err := templates.Render(l, "dataset-pets", "", map[string]any{"classes": []any{"cat", "dog"}})
	require.NoError(t, err)
	require.Equal(t, "classes:\n  - \"cat\"\n  - \"dog\"\n", files[1].Content)
}

type fakeAuth struct{ allow bool }

func (f fakeAuth) Authorize(context.Context, *auth.AuthorizationRequest) (*auth.AuthorizationResponse, error) {
	return &auth.AuthorizationResponse{Allowed: f.allow}, nil
}

func router(h templates.Handler, user string) http.Handler {
	r := chi.NewRouter()
	if user != "" {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				next.ServeHTTP(w, r.WithContext(auth.WithUser(r.Context(), &model.User{Username: user})))
			})
		})
	}
	r.Get("/kaos/v1/dataset-templates", h.List)
	r.Post("/kaos/v1/dataset-templates/{name}/render", h.Render)
	r.Post("/kaos/v1/dataset-templates/{name}/parse", h.Parse)
	r.Post("/kaos/v1/dataset-templates/{name}/validate", h.Validate)
	return r
}

func do(t *testing.T, r http.Handler, method, p, body string) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(method, p, strings.NewReader(body)))
	var out map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out), rec.Body.String())
	return rec.Code, out
}

func TestHandler(t *testing.T) {
	c := realCatalog(t)
	commitFiles(t, c, "layouts", map[string]string{"samples/layout.yaml": samples, "bad/layout.yaml": "{"})
	commitFiles(t, c, "dataset-pets", map[string]string{
		"meta.yaml": "classes: [a]\n", "data/1.json": "{}\n", "data/2.json": "not json\n", "data/.keep": "",
	})
	h := templates.Handler{StorageNamespacePrefix: "gs://bucket/lakefs",
		Store: &templates.CatalogStore{Catalog: c, Blocks: c.BlockAdapter}, Auth: fakeAuth{true}}
	r := router(h, "alice@acme")

	code, out := do(t, r, http.MethodGet, "/kaos/v1/dataset-templates", "")
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, "gs://bucket/lakefs", out["storage_namespace_prefix"])
	require.Equal(t, "layouts", out["layouts_repository"])
	require.Contains(t, out["server_checks"], "never runs its hook")
	list := out["templates"].([]any)
	require.Len(t, list, 3)
	project := list[2].(map[string]any)
	require.Equal(t, "project", project["source"])
	require.Equal(t, "samples", project["spec"].(map[string]any)["name"]) // the whole spec, for kaos-lib
	require.Equal(t, "layouts/bad/layout.yaml", out["problems"].([]any)[0].(map[string]any)["path"])

	code, out = do(t, r, http.MethodPost, "/kaos/v1/dataset-templates/yolo/render", `{"name":"dataset-cars","values":{"classes":["car"]}}`)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, "yolo", out["layout"])
	require.Len(t, out["files"], 8)
	code, out = do(t, r, http.MethodPost, "/kaos/v1/dataset-templates/ultralytics/render", `{"name":"dataset-cars","values":{"classes":["car"]}}`)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, "yolo", out["layout"], "the old id renders as yolo")
	code, out = do(t, r, http.MethodPost, "/kaos/v1/dataset-templates/yolo/render", `{"name":"cars","values":{"classes":["car"]}}`)
	require.Equal(t, http.StatusBadRequest, code)
	require.Contains(t, out["message"], "dataset-")
	code, out = do(t, r, http.MethodPost, "/kaos/v1/dataset-templates/samples/render", `{"name":"dataset-pigs","values":{"classes":["a"]}}`)
	require.Equal(t, http.StatusOK, code, out)

	code, out = do(t, r, http.MethodPost, "/kaos/v1/dataset-templates/ultralytics/parse", `{"content":"names: [a, b]"}`)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, map[string]any{"classes": []any{"a", "b"}}, out["values"])
	code, _ = do(t, r, http.MethodPost, "/kaos/v1/dataset-templates/raw/parse", `{"content":"x: 1"}`)
	require.Equal(t, http.StatusBadRequest, code)

	code, out = do(t, r, http.MethodPost, "/kaos/v1/dataset-templates/samples/validate", `{"repository":"dataset-pets","ref":"main"}`)
	require.Equal(t, http.StatusOK, code, out)
	require.Equal(t, []any{"data/2.json:1: one JSON object per line: not json"}, out["problems"])
	require.Equal(t, map[string]any{"files": 4.0, "read": 2.0, "skipped": 0.0, "partial": false}, out["checked"])
	require.Equal(t, "team_layouts.samples", out["hook"])
	code, out = do(t, r, http.MethodPost, "/kaos/v1/dataset-templates/yolo/validate", `{"repository":"dataset-pets","ref":"main"}`)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, []any{"data.yaml: no file matches (required)", "images/**: no file matches (required)"}, out["problems"])
	code, _ = do(t, r, http.MethodPost, "/kaos/v1/dataset-templates/yolo/validate", `{"repository":"dataset-ghost","ref":"main"}`)
	require.Equal(t, http.StatusNotFound, code)
	code, _ = do(t, r, http.MethodPost, "/kaos/v1/dataset-templates/yolo/validate", `{"repository":""}`)
	require.Equal(t, http.StatusBadRequest, code)

	denied := h
	denied.Auth = fakeAuth{false}
	code, _ = do(t, router(denied, "bob@acme"), http.MethodPost, "/kaos/v1/dataset-templates/yolo/validate", `{"repository":"dataset-pets","ref":"main"}`)
	require.Equal(t, http.StatusForbidden, code)
	code, _ = do(t, router(h, ""), http.MethodPost, "/kaos/v1/dataset-templates/yolo/validate", `{"repository":"dataset-pets","ref":"main"}`)
	require.Equal(t, http.StatusUnauthorized, code)
}

// Content beyond the read budget is skipped, and the answer says the check was partial.
func TestValidateSkipsLargeFiles(t *testing.T) {
	c := realCatalog(t)
	big := bytes.Repeat([]byte("{}\n"), (4<<20)/3+10)
	commitFiles(t, c, "dataset-big", map[string]string{"meta.yaml": "classes: [a]\n", "data/1.json": string(big)})
	s, err := templates.ParseSpec([]byte(samples), "samples")
	require.NoError(t, err)
	problems, rep, err := templates.CatalogStore{Catalog: c, Blocks: c.BlockAdapter}.Validate(t.Context(), s, "dataset-big", "main")
	require.NoError(t, err)
	require.Empty(t, problems)
	require.Equal(t, templates.TreeReport{Files: 2, Read: 0, Skipped: 1, Partial: true}, rep)
}
