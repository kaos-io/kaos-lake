// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-04/F-05; dataset layouts): new file, Apache-2.0.

package templates

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/treeverse/lakefs/pkg/auth"
	"github.com/treeverse/lakefs/pkg/graveler"
	"github.com/treeverse/lakefs/pkg/permissions"
)

// ServerChecks says what kaos-lake checks of a dataset, in every answer that involves rules.
const ServerChecks = "kaos-lake checks a layout's declarative rules only and never runs its hook; " +
	"kaosctl layout validate runs both"

// Authorizer decides whether a caller may read a repository (the auth service).
type Authorizer interface {
	Authorize(ctx context.Context, req *auth.AuthorizationRequest) (*auth.AuthorizationResponse, error)
}

// Handler serves the layouts. It reads, renders and checks; it never writes to a repository.
type Handler struct {
	// StorageNamespacePrefix is where a new dataset's storage namespace goes: prefix/name.
	StorageNamespacePrefix string
	// Store is the project's layout registry and the reader of dataset trees; nil: built-ins only.
	Store *CatalogStore
	// Auth decides who may have a repository checked (Validate).
	Auth Authorizer
}

func (h Handler) store() ProjectStore {
	if h.Store == nil {
		return nil
	}
	return h.Store
}

// List answers the layouts (built-in and the project's, each with its source and whole spec), the
// project layouts left out and why, and the storage namespace prefix for new datasets.
func (h Handler) List(w http.ResponseWriter, r *http.Request) {
	if !signedIn(w, r) {
		return
	}
	all, problems, err := All(r.Context(), h.store())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": err.Error()})
		return
	}
	if problems == nil {
		problems = []Problem{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"storage_namespace_prefix": h.StorageNamespacePrefix, "layouts_repository": LayoutsRepository,
		"server_checks": ServerChecks, "templates": all, "problems": problems,
	})
}

// Render answers the files of a new dataset: {name, description, values} in, {files} out.
func (h Handler) Render(w http.ResponseWriter, r *http.Request) {
	if !signedIn(w, r) {
		return
	}
	var body struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Values      map[string]any `json:"values"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "body: " + err.Error()})
		return
	}
	l, err := Get(r.Context(), h.store(), chi.URLParam(r, "name"))
	if err != nil {
		respond(w, nil, err)
		return
	}
	files, err := Render(l, body.Name, body.Description, body.Values)
	respond(w, map[string]any{"layout": l.Name, "files": files}, err)
}

// Parse answers the form values of a dataset's form file: {content} in, {values} out.
func (h Handler) Parse(w http.ResponseWriter, r *http.Request) {
	if !signedIn(w, r) {
		return
	}
	var body struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "body: " + err.Error()})
		return
	}
	l, err := Get(r.Context(), h.store(), chi.URLParam(r, "name"))
	if err != nil {
		respond(w, nil, err)
		return
	}
	values, err := ParseForm(l.Spec, body.Content)
	respond(w, map[string]any{"values": values}, err)
}

// Validate answers the problems the layout's declarative rules find in {repository, ref}, to a
// caller who may list and read that repository.
func (h Handler) Validate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Repository string `json:"repository"`
		Ref        string `json:"ref"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Repository == "" || body.Ref == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "body: want {repository, ref}"})
		return
	}
	ctx := r.Context()
	user, err := auth.GetUser(ctx)
	if err != nil || h.Auth == nil || h.Store == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"message": "sign in to check a dataset"})
		return
	}
	resp, err := h.Auth.Authorize(ctx, &auth.AuthorizationRequest{Username: user.Username, RequiredPermissions: permissions.Node{
		Type: permissions.NodeTypeAnd,
		Nodes: []permissions.Node{
			{Permission: permissions.Permission{Action: permissions.ListObjectsAction, Resource: permissions.RepoArn(body.Repository)}},
			{Permission: permissions.Permission{Action: permissions.ReadObjectAction, Resource: permissions.ObjectArn(body.Repository, "*")}},
		},
	}})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": err.Error()})
		return
	}
	if resp.Error != nil || !resp.Allowed {
		writeJSON(w, http.StatusForbidden, map[string]string{"message": "you may not read " + body.Repository})
		return
	}
	l, err := Get(ctx, h.store(), chi.URLParam(r, "name"))
	if err != nil {
		respond(w, nil, err)
		return
	}
	problems, report, err := h.Store.Validate(ctx, l.Spec, body.Repository, body.Ref)
	if errors.Is(err, graveler.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": err.Error()})
		return
	}
	if problems == nil {
		problems = []string{}
	}
	respond(w, map[string]any{"layout": l.Name, "repository": body.Repository, "ref": body.Ref, "problems": problems,
		"checked": report, "hook": l.Hook, "server_checks": ServerChecks}, err)
}

// signedIn answers 401 to a caller without a user. The routes sit behind GenericAuthMiddleware,
// which refuses bad credentials but passes a request that carries none; the layouts (the
// project's own, from its layouts repository) and the storage namespace prefix are for the
// project's signed-in people, not for an anonymous caller.
func signedIn(w http.ResponseWriter, r *http.Request) bool {
	if _, err := auth.GetUser(r.Context()); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"message": "sign in to use dataset layouts"})
		return false
	}
	return true
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
