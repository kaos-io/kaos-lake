// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-04): new file, Apache-2.0.

// Package uiconfig serves the few KAOS settings the web UI needs: where the KAOS console is, so the
// header can link back to it, and which repository is the project's legacy one, so it is badged as
// such rather than as "Other".
package uiconfig

import (
	"encoding/json"
	"net/http"
	"net/url"
)

// Handler answers GET /kaos/v1/ui with {"console_url": "...", "legacy_repository": "..."}. Neither
// is a secret, so the answer needs no sign-in (the header shows before sign-in too).
type Handler struct {
	ConsoleURL       string
	LegacyRepository string
}

// Valid reports whether u is empty (no link) or an absolute http(s) URL.
func Valid(u string) bool {
	if u == "" {
		return true
	}
	p, err := url.Parse(u)
	return err == nil && (p.Scheme == "https" || p.Scheme == "http") && p.Host != ""
}

func (h Handler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	console := h.ConsoleURL
	if !Valid(console) { // never hand the UI a javascript: or other scheme to put in a link
		console = ""
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"console_url": console, "legacy_repository": h.LegacyRepository})
}
