// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-12): new file, Apache-2.0.

package sharing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
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

// SharedWithUsPath is the consumer side's endpoint for people: what peers share with this project.
const SharedWithUsPath = "/kaos/v1/shared-with-us"

// SharedDataset is one dataset another project shares with this one, as people see it.
type SharedDataset struct {
	Name        string                `json:"name"` // <owner>/<repo>, as pipelines name it
	Owner       string                `json:"owner"`
	Repo        string                `json:"repo"` // the repository in the owner's lakeFS
	Location    string                `json:"location,omitempty"`
	Description string                `json:"description,omitempty"`
	Template    string                `json:"template,omitempty"`
	Latest      string                `json:"latest,omitempty"`
	Versions    []datacatalog.Version `json:"versions"`
	// URL is the dataset in the owner's lakeFS web UI (the peer's public url).
	URL string `json:"url"`
}

// Note says why an owner's datasets are missing or may be out of date.
type Note struct {
	Owner string `json:"owner"`
	Note  string `json:"note"`
}

// SharedWithUsResponse is what GET /kaos/v1/shared-with-us answers.
type SharedWithUsResponse struct {
	Project  string          `json:"project"`
	Datasets []SharedDataset `json:"datasets"`
	Notes    []Note          `json:"notes"`
}

// SharedWithUsHandler answers GET /kaos/v1/shared-with-us, behind the API's authentication: the
// datasets the peers share with this project, from the same aggregation and cache as the catalog,
// for the Datasets page, and a note for each owner that could not be read.
//
// Any signed-in caller of this lakeFS (a person or an app key) gets the same answer. That is meant:
// what is shared with the project is shared with its people and pipelines, who read it in the
// owner's lakeFS through its consumer group for this project, and the answer carries names,
// versions and links only, never data. It never carries the token sent to
// peers, their in-cluster endpoints, their errors, or who else a dataset is shared with (the owner
// side never sends that). Aggregator nil (no kaos.sharing): nothing shared.
type SharedWithUsHandler struct {
	Project    string // this project (kaos.token_identity.project)
	Aggregator *Aggregator
}

func (h SharedWithUsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if _, err := auth.GetUser(r.Context()); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"message": "sign in to read the datasets shared with this project"})
		return
	}
	out := SharedWithUsResponse{Project: h.Project, Datasets: []SharedDataset{}, Notes: []Note{}}
	if h.Aggregator != nil {
		for _, p := range h.Aggregator.Collect(r.Context()) {
			if note := noteFor(p); note != "" {
				out.Notes = append(out.Notes, Note{Owner: p.Peer.Project, Note: note})
			}
			for _, d := range p.Datasets {
				repo := strings.TrimPrefix(d.Name, p.Peer.Project+"/")
				out.Datasets = append(out.Datasets, SharedDataset{
					Name: d.Name, Owner: p.Peer.Project, Repo: repo, Location: d.Location, Description: d.Description,
					Template: d.Template, Latest: d.Latest, Versions: d.Versions,
					URL: strings.TrimSuffix(p.Peer.URL, "/") + "/repositories/" + url.PathEscape(repo) + "/objects",
				})
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// noteFor says why p's datasets are missing or may be out of date; "" when they are current.
func noteFor(p PeerShares) string {
	switch {
	case p.Err == nil:
		return ""
	case p.Datasets != nil:
		return fmt.Sprintf("Project %s's lakeFS did not answer; showing its last known list.", p.Peer.Project)
	case errors.Is(p.Err, ErrOlderPeer):
		return fmt.Sprintf("Project %s's lakeFS is too old to share datasets; what it shares is not shown.", p.Peer.Project)
	default:
		return fmt.Sprintf("Project %s's lakeFS did not answer; what it shares is not shown.", p.Peer.Project)
	}
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
