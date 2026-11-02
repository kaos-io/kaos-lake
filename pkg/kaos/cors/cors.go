// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-03): new file, Apache-2.0.

// Package cors lets the KAOS console use this lakeFS from the browser. It allows cross-origin
// calls from an exact list of origins, with a bearer token only: it never sends
// Access-Control-Allow-Credentials, so the lakeFS session cookie is never usable from another
// origin. Reads (GET) are allowed on the whole API; the only writes are the two the console's
// dataset page makes (PRD novelcore/kaos#1283 F-11): a repository's labels (POST, DELETE
// .../metadata) and releasing a version (POST .../tags). lakeFS's own authorization still decides
// whether the person may make them.
package cors

import (
	"net/http"
	"regexp"
	"strings"
)

const (
	readMethods  = "GET, OPTIONS"
	allowHeaders = "Authorization, Content-Type, Accept"
	maxAge       = "600"
)

// writable maps the API paths the console may write cross-origin to the methods allowed there.
var writable = []struct {
	path    *regexp.Regexp
	methods string
}{
	{regexp.MustCompile(`^/api/v1/repositories/[^/]+/metadata$`), "GET, POST, DELETE, OPTIONS"},
	{regexp.MustCompile(`^/api/v1/repositories/[^/]+/tags$`), "GET, POST, OPTIONS"},
}

// methodsFor is the Access-Control-Allow-Methods value for path.
func methodsFor(path string) string {
	for _, w := range writable {
		if w.path.MatchString(path) {
			return w.methods
		}
	}
	return readMethods
}

func allows(methods, m string) bool {
	return m != "" && m != http.MethodOptions && strings.Contains(", "+methods+",", ", "+m+",")
}

// origin is scheme://host[:port]: no path, no wildcard.
var origin = regexp.MustCompile(`^https?://[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?(:[0-9]{1,5})?$`)

// Middleware returns the CORS middleware for requests whose path starts with pathPrefix, for the
// exact origins listed. Entries that are not a plain origin (a "*", a path) match nothing. With no
// valid origin it returns next unchanged.
func Middleware(origins []string, pathPrefix string) func(http.Handler) http.Handler {
	allowed := map[string]bool{}
	for _, o := range origins {
		if o = strings.TrimSpace(o); origin.MatchString(o) {
			allowed[o] = true
		}
	}
	return func(next http.Handler) http.Handler {
		if len(allowed) == 0 {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			o := r.Header.Get("Origin")
			if o == "" || !allowed[o] || !strings.HasPrefix(r.URL.Path, pathPrefix) {
				next.ServeHTTP(w, r)
				return
			}
			h := w.Header()
			h.Add("Vary", "Origin")
			switch {
			case r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "":
				preflight(w, r, o)
			case allows(methodsFor(r.URL.Path), r.Method):
				h.Set("Access-Control-Allow-Origin", o)
				next.ServeHTTP(w, r)
			default: // not allowed cross-origin here: no CORS grant, so the browser cannot read it
				next.ServeHTTP(w, r)
			}
		})
	}
}

// preflight answers a CORS preflight without authentication: it grants a method allowed on the
// path and refuses (granting nothing) any other.
func preflight(w http.ResponseWriter, r *http.Request, o string) {
	methods := methodsFor(r.URL.Path)
	if !allows(methods, r.Header.Get("Access-Control-Request-Method")) {
		http.Error(w, "this method is not allowed cross-origin on this path", http.StatusForbidden)
		return
	}
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", o)
	h.Set("Access-Control-Allow-Methods", methods)
	h.Set("Access-Control-Allow-Headers", allowHeaders)
	h.Set("Access-Control-Max-Age", maxAge)
	w.WriteHeader(http.StatusNoContent)
}
