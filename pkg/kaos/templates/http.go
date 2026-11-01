// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-04/F-05): new file, Apache-2.0.

package templates

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Handler serves the templates. It reads and renders only; it never writes to a repository.
type Handler struct {
	// StorageNamespacePrefix is where a new dataset's storage namespace goes: prefix/name.
	StorageNamespacePrefix string
}

// List answers the templates and the storage namespace prefix for new datasets.
func (h Handler) List(w http.ResponseWriter, _ *http.Request) {
	all, err := All()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"storage_namespace_prefix": h.StorageNamespacePrefix, "templates": all})
}

// Render answers the files of a new dataset: {name, description, values} in, {files} out.
func (h Handler) Render(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Values      map[string]any `json:"values"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "body: " + err.Error()})
		return
	}
	files, err := Render(chi.URLParam(r, "name"), body.Name, body.Description, body.Values)
	respond(w, map[string]any{"files": files}, err)
}

// Parse answers the form values of a dataset's form file: {content} in, {values} out.
func (h Handler) Parse(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "body: " + err.Error()})
		return
	}
	values, err := Parse(chi.URLParam(r, "name"), body.Content)
	respond(w, map[string]any{"values": values}, err)
}

func respond(w http.ResponseWriter, ok any, err error) {
	switch {
	case errors.Is(err, ErrInvalid):
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
	case err != nil:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": err.Error()})
	default:
		writeJSON(w, http.StatusOK, ok)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
