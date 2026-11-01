// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-03): new file, Apache-2.0.

// Package cors lets the KAOS console read this lakeFS from the browser. It allows read-only
// (GET) cross-origin calls from an exact list of origins, with a bearer token only: it never
// sends Access-Control-Allow-Credentials, so the lakeFS session cookie is never usable from
// another origin.
package cors

import (
	"net/http"
	"regexp"
	"strings"
)

const (
	allowMethods = "GET, OPTIONS"
	allowHeaders = "Authorization, Content-Type, Accept"
	maxAge       = "600"
)

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
			case r.Method == http.MethodGet:
				h.Set("Access-Control-Allow-Origin", o)
				next.ServeHTTP(w, r)
			default: // only GET is allowed cross-origin; no CORS headers for anything else
				next.ServeHTTP(w, r)
			}
		})
	}
}

// preflight answers a CORS preflight without authentication: for GET it grants, for any other
// method it refuses and grants nothing.
func preflight(w http.ResponseWriter, r *http.Request, o string) {
	if r.Header.Get("Access-Control-Request-Method") != http.MethodGet {
		http.Error(w, "only GET is allowed cross-origin", http.StatusForbidden)
		return
	}
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", o)
	h.Set("Access-Control-Allow-Methods", allowMethods)
	h.Set("Access-Control-Allow-Headers", allowHeaders)
	h.Set("Access-Control-Max-Age", maxAge)
	w.WriteHeader(http.StatusNoContent)
}
