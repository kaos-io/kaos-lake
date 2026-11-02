// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-12): new file, Apache-2.0.

package sharing

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"

	"github.com/treeverse/lakefs/pkg/auth"
	"github.com/treeverse/lakefs/pkg/auth/model"
	"github.com/treeverse/lakefs/pkg/kaos/datacatalog"
	"github.com/treeverse/lakefs/pkg/kaos/repos"
)

// GroupLister is what SharedHandler reads of the auth store.
type GroupLister interface {
	ListUserGroups(ctx context.Context, username string, params *model.PaginationParams) ([]*model.Group, *model.Paginator, error)
}

// SharedHandler answers GET /kaos/v1/shared on the owner side, behind the API's authentication.
//
// The answer depends only on the caller's kaos-consumer-<project> groups: the datasets
// (kaos.kind=dataset, dataset-*) whose kaos.shared_with names one of those projects, never who
// else they are shared with. Any other group grants nothing here: this project's own people and
// pipelines, outside consumer groups, get 401 like any caller without one, and read their datasets
// through the catalog and the regular API. A person who is also in another project of the
// organisation that something is shared with is in that project's consumer group, so sees what
// is shared with it, as the regular API already lets them read.
type SharedHandler struct {
	Catalog datacatalog.Catalog
	Groups  GroupLister
	// Owner is this project (kaos.token_identity.project), answered as owner.
	Owner string
}

func (h SharedHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, err := auth.GetUser(ctx)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"message": "sign in to read shared datasets"})
		return
	}
	groups, _, err := h.Groups.ListUserGroups(ctx, user.Username, &model.PaginationParams{Amount: -1})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": err.Error()})
		return
	}
	var projects []string
	for _, g := range groups {
		if p, ok := strings.CutPrefix(g.DisplayName, repos.ConsumerGroupPrefix); ok && repos.ValidProject(p) && !slices.Contains(projects, p) {
			projects = append(projects, p)
		}
	}
	if len(projects) == 0 {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"message": "nothing is shared with your project here"})
		return
	}
	datasets, err := datacatalog.SharedWith(ctx, h.Catalog, projects)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": err.Error()})
		return
	}
	if datasets == nil {
		datasets = []datacatalog.Dataset{}
	}
	writeJSON(w, http.StatusOK, SharedResponse{Owner: h.Owner, Datasets: datasets})
}

// PeersHandler answers GET /kaos/v1/peers, behind the API's authentication: the peers file as
// last read, or no peers when sharing is off (File nil).
type PeersHandler struct {
	File *PeersFile
}

func (h PeersHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if _, err := auth.GetUser(r.Context()); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"message": "sign in to read the peers"})
		return
	}
	peers := Peers{Peers: []Peer{}}
	if h.File != nil {
		peers = h.File.Get()
	}
	writeJSON(w, http.StatusOK, peers)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
