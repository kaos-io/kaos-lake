// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-03): new file, Apache-2.0.

package cors_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/treeverse/lakefs/pkg/kaos/cors"
)

const console = "https://kaos-io.console.dev.kaos-labs.org"

func serve(origins []string, method, path, origin string, hdr map[string]string) *httptest.ResponseRecorder {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	r := httptest.NewRequest(method, path, nil)
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	cors.Middleware(origins, "/api/v1/")(next).ServeHTTP(rec, r)
	return rec
}

func noCORS(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	for k := range rec.Header() {
		require.NotContains(t, k, "Access-Control-", k)
	}
	require.Empty(t, rec.Header().Values("Vary"))
}

func TestAllowedOriginGet(t *testing.T) {
	rec := serve([]string{console}, http.MethodGet, "/api/v1/repositories", console, nil)
	require.Equal(t, http.StatusTeapot, rec.Code, "the request reaches the API")
	require.Equal(t, console, rec.Header().Get("Access-Control-Allow-Origin"), "echoed, never *")
	require.Contains(t, rec.Header().Values("Vary"), "Origin")
	require.Empty(t, rec.Header().Get("Access-Control-Allow-Credentials"))
}

func TestDisallowedOrigin(t *testing.T) {
	for _, o := range []string{"https://evil.example", console + ".evil.example", "http://kaos-io.console.dev.kaos-labs.org", ""} {
		noCORS(t, serve([]string{console}, http.MethodGet, "/api/v1/repositories", o, nil))
	}
	pre := serve([]string{console}, http.MethodOptions, "/api/v1/repositories", "https://evil.example",
		map[string]string{"Access-Control-Request-Method": "GET"})
	require.Equal(t, http.StatusTeapot, pre.Code, "not answered here: it goes the normal way")
	noCORS(t, pre)
}

func TestPreflight(t *testing.T) {
	rec := serve([]string{console}, http.MethodOptions, "/api/v1/repositories/x/branches", console,
		map[string]string{"Access-Control-Request-Method": "GET", "Access-Control-Request-Headers": "authorization"})
	require.Equal(t, http.StatusNoContent, rec.Code)
	h := rec.Header()
	require.Equal(t, console, h.Get("Access-Control-Allow-Origin"))
	require.Equal(t, "GET, OPTIONS", h.Get("Access-Control-Allow-Methods"))
	require.Equal(t, "Authorization, Content-Type, Accept", h.Get("Access-Control-Allow-Headers"))
	require.Equal(t, "600", h.Get("Access-Control-Max-Age"))
	require.Contains(t, h.Values("Vary"), "Origin")
	require.Empty(t, h.Get("Access-Control-Allow-Credentials"))
}

func TestNonGetRefused(t *testing.T) {
	for _, m := range []string{"POST", "PUT", "DELETE", "PATCH"} {
		rec := serve([]string{console}, http.MethodOptions, "/api/v1/repositories", console,
			map[string]string{"Access-Control-Request-Method": m})
		require.Equal(t, http.StatusForbidden, rec.Code, m)
		require.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"), m)
		require.Empty(t, rec.Header().Get("Access-Control-Allow-Methods"), m)
		// and the real call carries no CORS grant, so a browser cannot read its answer
		real := serve([]string{console}, m, "/api/v1/repositories", console, nil)
		require.Empty(t, real.Header().Get("Access-Control-Allow-Origin"), m)
	}
}

func TestNeverCredentialsAndOnlyAPI(t *testing.T) {
	for _, path := range []string{"/api/v1/repositories", "/oidc/login", "/"} {
		rec := serve([]string{console}, http.MethodGet, path, console, nil)
		require.Empty(t, rec.Header().Get("Access-Control-Allow-Credentials"), path)
		if path != "/api/v1/repositories" {
			noCORS(t, rec)
		}
	}
}

func TestOffAndInvalidEntries(t *testing.T) {
	noCORS(t, serve(nil, http.MethodGet, "/api/v1/repositories", console, nil))
	noCORS(t, serve([]string{"*", "https://a.example/path", "null"}, http.MethodGet, "/api/v1/repositories", "https://a.example", nil))
	noCORS(t, serve([]string{"*"}, http.MethodGet, "/api/v1/repositories", "*", nil))
}

// PRD novelcore/kaos#1283 F-11: the console's dataset page writes a repository's labels and
// releases versions, and nothing else.
func TestConsoleWrites(t *testing.T) {
	pre := func(path, m string) *httptest.ResponseRecorder {
		return serve([]string{console}, http.MethodOptions, path, console, map[string]string{"Access-Control-Request-Method": m})
	}
	for _, c := range []struct{ path, method, methods string }{
		{"/api/v1/repositories/dataset-coco/metadata", "POST", "GET, POST, DELETE, OPTIONS"},
		{"/api/v1/repositories/dataset-coco/metadata", "DELETE", "GET, POST, DELETE, OPTIONS"},
		{"/api/v1/repositories/dataset-coco/tags", "POST", "GET, POST, OPTIONS"},
	} {
		rec := pre(c.path, c.method)
		require.Equal(t, http.StatusNoContent, rec.Code, c.method+" "+c.path)
		require.Equal(t, c.methods, rec.Header().Get("Access-Control-Allow-Methods"))
		require.Empty(t, rec.Header().Get("Access-Control-Allow-Credentials"))
		real := serve([]string{console}, c.method, c.path, console, nil)
		require.Equal(t, http.StatusTeapot, real.Code, "authorization stays lakeFS's")
		require.Equal(t, console, real.Header().Get("Access-Control-Allow-Origin"))
	}
	for _, c := range []struct{ path, method string }{
		{"/api/v1/repositories/dataset-coco/tags", "DELETE"},
		{"/api/v1/repositories/dataset-coco/metadata", "PUT"},
		{"/api/v1/repositories/dataset-coco", "DELETE"},
		{"/api/v1/repositories/dataset-coco/branches", "POST"},
		{"/api/v1/repositories/dataset-coco/tags/v1", "DELETE"},
		{"/api/v1/repositories/a/b/metadata", "POST"},
		{"/api/v1/auth/users", "POST"},
	} {
		rec := pre(c.path, c.method)
		require.Equal(t, http.StatusForbidden, rec.Code, c.method+" "+c.path)
		require.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
		require.Empty(t, serve([]string{console}, c.method, c.path, console, nil).Header().Get("Access-Control-Allow-Origin"), c.method+" "+c.path)
	}
	// a write from another origin gets no grant
	noCORS(t, serve([]string{console}, http.MethodPost, "/api/v1/repositories/dataset-coco/metadata", "https://evil.example", nil))
}
