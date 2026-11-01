// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-04/F-05): new file, Apache-2.0.

package templates_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/treeverse/lakefs/pkg/kaos/templates"
)

func files(t *testing.T, fs []templates.File) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, f := range fs {
		out[f.Path] = f.Content
	}
	return out
}

// AC-02: the YOLO template makes the layout Ultralytics reads, with the classes in id order.
func TestRender_YOLO(t *testing.T) {
	fs, err := templates.Render("yolo", "dataset-cars", "Street cars", map[string]any{
		"classes": []any{" car ", "truck", "", `bus "double"`},
	})
	require.NoError(t, err)
	got := files(t, fs)
	for _, p := range []string{"images/train/.keep", "images/val/.keep", "images/test/.keep",
		"labels/train/.keep", "labels/val/.keep", "labels/test/.keep", "README.md", "data.yaml"} {
		require.Contains(t, got, p)
	}
	var doc struct {
		Path  string         `yaml:"path"`
		Train string         `yaml:"train"`
		Val   string         `yaml:"val"`
		Test  string         `yaml:"test"`
		NC    int            `yaml:"nc"`
		Names map[int]string `yaml:"names"`
	}
	require.NoError(t, yaml.Unmarshal([]byte(got["data.yaml"]), &doc))
	require.Equal(t, "images/train", doc.Train)
	require.Equal(t, "images/val", doc.Val)
	require.Equal(t, 3, doc.NC)
	require.Equal(t, map[int]string{0: "car", 1: "truck", 2: `bus "double"`}, doc.Names)
	require.Contains(t, got["README.md"], "# dataset-cars")
	require.Contains(t, got["README.md"], "Street cars")
	require.Contains(t, got["README.md"], "--dataset dataset-cars")
}

// The edit form round-trips: what Render writes, Parse reads back; both YOLO name forms parse.
func TestParse_YOLO(t *testing.T) {
	fs, err := templates.Render("yolo", "dataset-cars", "", map[string]any{"classes": []string{"car", "truck"}})
	require.NoError(t, err)
	values, err := templates.Parse("yolo", files(t, fs)["data.yaml"])
	require.NoError(t, err)
	require.Equal(t, map[string]any{"classes": []string{"car", "truck"}}, values)

	values, err = templates.Parse("yolo", "nc: 2\nnames: [cat, dog]\n")
	require.NoError(t, err)
	require.Equal(t, []string{"cat", "dog"}, values["classes"])

	for _, bad := range []string{"nc: 3\nnames: [cat, dog]\n", "names:\n  0: cat\n  2: dog\n", "train: x\n", "names: [\n"} {
		_, err := templates.Parse("yolo", bad)
		require.ErrorIs(t, err, templates.ErrInvalid, bad)
	}
	_, err = templates.Parse("empty", "anything")
	require.ErrorIs(t, err, templates.ErrInvalid)
}

// Refusals name the rule (P-9).
func TestRender_Refusals(t *testing.T) {
	for _, c := range []struct {
		tmpl, name string
		values     map[string]any
		why        string
	}{
		{"yolo", "dataset-cars", map[string]any{"classes": []any{}}, "at least one"},
		{"yolo", "dataset-cars", map[string]any{"classes": []any{"car", "car"}}, "twice"},
		{"yolo", "dataset-cars", map[string]any{"classes": []any{"a\nb"}}, "spans lines"},
		{"yolo", "cars", map[string]any{"classes": []any{"car"}}, "dataset-"},
		{"yolo", "dataset-cars", map[string]any{"classes": "car"}, "must be a list"},
		{"nope", "dataset-cars", nil, "no template"},
		{"../yolo", "dataset-cars", nil, "no template"},
	} {
		_, err := templates.Render(c.tmpl, c.name, "", c.values)
		require.ErrorIs(t, err, templates.ErrInvalid, c)
		require.Contains(t, err.Error(), c.why, c)
	}
	fs, err := templates.Render("empty", "dataset-free", "", nil)
	require.NoError(t, err)
	require.Equal(t, []string{"README.md"}, []string{fs[0].Path})
}

func TestHandler(t *testing.T) {
	r := chi.NewRouter()
	h := templates.Handler{StorageNamespacePrefix: "gs://bucket/lakefs"}
	r.Get("/kaos/v1/dataset-templates", h.List)
	r.Post("/kaos/v1/dataset-templates/{name}/render", h.Render)
	r.Post("/kaos/v1/dataset-templates/{name}/parse", h.Parse)
	do := func(method, path, body string) (int, map[string]any) {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
		var out map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
		return rec.Code, out
	}
	code, out := do(http.MethodGet, "/kaos/v1/dataset-templates", "")
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, "gs://bucket/lakefs", out["storage_namespace_prefix"])
	require.Len(t, out["templates"], 2)

	code, out = do(http.MethodPost, "/kaos/v1/dataset-templates/yolo/render",
		`{"name":"dataset-cars","values":{"classes":["car"]}}`)
	require.Equal(t, http.StatusOK, code)
	require.Len(t, out["files"], 8)

	code, out = do(http.MethodPost, "/kaos/v1/dataset-templates/yolo/render", `{"name":"cars","values":{"classes":["car"]}}`)
	require.Equal(t, http.StatusBadRequest, code)
	require.Contains(t, out["message"], "dataset-")

	code, out = do(http.MethodPost, "/kaos/v1/dataset-templates/yolo/parse", `{"content":"names: [a, b]"}`)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, map[string]any{"classes": []any{"a", "b"}}, out["values"])
}
