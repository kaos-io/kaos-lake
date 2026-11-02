// KAOS (kaos-lake, dataset layouts): new file, Apache-2.0.

package templates_test

// The shared layout fixtures: kaos-lib runs the same files (tests/layout_fixtures) and gives the
// same answers. SHA256SUMS pins the shared bytes; kaos-lib's copy must be identical.

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/treeverse/lakefs/pkg/kaos/templates"
)

const fixtures = "testdata/layout_fixtures"

var roots = map[string]string{"shared": "shared", "fixtures": fixtures}

func cases(t *testing.T, name string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixtures, name+".yaml"))
	require.NoError(t, err)
	v, err := templates.LoadYAML(data)
	require.NoError(t, err)
	var out []map[string]any
	for _, c := range v.([]any) {
		out = append(out, c.(map[string]any))
	}
	return out
}

func specOf(t *testing.T, c map[string]any) templates.Spec {
	t.Helper()
	if b, ok := c["builtin"].(string); ok {
		l, err := templates.Get(t.Context(), nil, b)
		require.NoError(t, err)
		return l.Spec
	}
	s, err := templates.FromValue(c["layout"], c["case"].(string))
	require.NoError(t, err)
	return s
}

func TestSharedBytesMatchSHA256SUMS(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(fixtures, "SHA256SUMS"))
	require.NoError(t, err)
	want := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		digest, rel, _ := strings.Cut(line, "  ")
		root, tail, _ := strings.Cut(rel, "/")
		content, err := os.ReadFile(filepath.Join(roots[root], tail))
		require.NoError(t, err, rel)
		sum := sha256.Sum256(content)
		require.Equal(t, digest, hex.EncodeToString(sum[:]), rel)
		want[rel] = true
	}
	listed := map[string]bool{}
	require.NoError(t, filepath.WalkDir("shared", func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			listed[filepath.ToSlash(p)] = true
		}
		return err
	}))
	yamls, _ := filepath.Glob(filepath.Join(fixtures, "*.yaml"))
	for _, y := range yamls {
		listed["fixtures/"+filepath.Base(y)] = true
	}
	require.Equal(t, want, listed, "every shared file is pinned")
}

func TestFixtureSpecs(t *testing.T) {
	for _, c := range cases(t, "spec") {
		t.Run(c["case"].(string), func(t *testing.T) {
			errs := templates.Problems(c["layout"])
			if c["ok"] == true {
				require.Empty(t, errs)
				return
			}
			found := false
			for _, e := range errs {
				found = found || strings.Contains(e, c["error"].(string))
			}
			require.True(t, found, "%q not in %q", c["error"], errs)
		})
	}
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func TestFixtureRender(t *testing.T) {
	for _, c := range cases(t, "render") {
		t.Run(c["case"].(string), func(t *testing.T) {
			values, _ := c["values"].(map[string]any)
			files, err := templates.RenderSpec(specOf(t, c), str(c["name"]), str(c["description"]), values)
			if e, ok := c["error"].(string); ok {
				require.ErrorIs(t, err, templates.ErrInvalid)
				require.Contains(t, err.Error(), e)
				return
			}
			require.NoError(t, err)
			got := map[string]any{}
			for _, f := range files {
				got[f.Path] = f.Content
			}
			require.Equal(t, c["files"], got)
		})
	}
}

func TestFixtureParse(t *testing.T) {
	for _, c := range cases(t, "parse") {
		t.Run(c["case"].(string), func(t *testing.T) {
			values, err := templates.ParseForm(specOf(t, c), str(c["content"]))
			if e, ok := c["error"].(string); ok {
				require.ErrorIs(t, err, templates.ErrInvalid)
				require.Contains(t, err.Error(), e)
				return
			}
			require.NoError(t, err)
			got := map[string]any{}
			for k, v := range values {
				var items []any
				if l, ok := v.([]string); ok {
					for _, s := range l {
						items = append(items, s)
					}
					got[k] = items
				} else {
					got[k] = v
				}
			}
			require.Equal(t, c["values"], got)
		})
	}
}

func TestFixtureValidate(t *testing.T) {
	for _, c := range cases(t, "validate") {
		t.Run(c["case"].(string), func(t *testing.T) {
			tree := map[string][]byte{}
			var paths []string
			for p, v := range c["tree"].(map[string]any) {
				if m, ok := v.(map[string]any); ok {
					b, err := hex.DecodeString(m["hex"].(string))
					require.NoError(t, err)
					tree[p] = b
				} else {
					tree[p] = []byte(v.(string))
				}
				paths = append(paths, p)
			}
			got, err := templates.Check(specOf(t, c), paths, func(p string) ([]byte, error) { return tree[p], nil })
			require.NoError(t, err)
			want := []string{}
			for _, e := range c["errors"].([]any) {
				want = append(want, e.(string))
			}
			if got == nil {
				got = []string{}
			}
			require.Equal(t, want, got)
		})
	}
}
