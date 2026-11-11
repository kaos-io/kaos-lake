// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-07, D-05, F-12): new file, Apache-2.0.

// Package datacatalog serves the project's datasets and their versions, read-only, to the trusted
// step that offers them in pipelines' dataset dropdown: names, versions and locations, never data.
// Callers are pods of the project's own namespaces, by their service-account token. With sharing
// on (pkg/kaos/sharing), the datasets sibling projects share with this one follow the project's
// own, named <owner>/<dataset>.
package datacatalog

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/treeverse/lakefs/pkg/catalog"
	"github.com/treeverse/lakefs/pkg/graveler"
	"github.com/treeverse/lakefs/pkg/kaos/repos"
)

// Catalog is what the handler reads of the lakeFS catalog.
type Catalog interface {
	ListRepositories(ctx context.Context, limit int, prefix, searchString, after string, opts ...catalog.ListRepositoriesOptionsFunc) ([]*catalog.Repository, bool, error)
	GetRepositoryMetadata(ctx context.Context, repository string) (graveler.RepositoryMetadata, error)
	ListTags(ctx context.Context, repositoryID string, prefix string, limit int, after string) ([]*catalog.Tag, bool, error)
	GetBranchReference(ctx context.Context, repositoryID string, branch string) (string, error)
}

// Version is one version of a dataset: a release people numbered (vN), or a commit someone named
// with any other tag (PRD novelcore/kaos#1911 D-03).
type Version struct {
	Tag    string `json:"tag"`
	Commit string `json:"commit"`
}

// Dataset is one dataset of the catalog.
type Dataset struct {
	Name string `json:"name"`
	// Owner is the project a shared dataset belongs to; empty for the project's own datasets.
	Owner       string    `json:"owner,omitempty"`
	Default     bool      `json:"default,omitempty"`
	Description string    `json:"description,omitempty"`
	Location    string    `json:"location,omitempty"`
	Template    string    `json:"template,omitempty"` // kaos.template, when made from one
	Latest      string    `json:"latest,omitempty"`   // main's head commit
	Versions    []Version `json:"versions"`           // vN newest first, then named versions by name
}

// List returns the datasets (dataset-* repositories labelled a dataset) with their versions.
func List(ctx context.Context, c Catalog) ([]Dataset, error) {
	return list(ctx, c, func(graveler.RepositoryMetadata) bool { return true })
}

// SharedWith returns the datasets whose kaos.shared_with names one of projects, as another project
// sees them: never the default, and nothing about who else they are shared with.
func SharedWith(ctx context.Context, c Catalog, projects []string) ([]Dataset, error) {
	out, err := list(ctx, c, func(md graveler.RepositoryMetadata) bool {
		return slices.ContainsFunc(repos.SharedWith(md[repos.KeySharedWith]), func(p string) bool {
			return slices.Contains(projects, p)
		})
	})
	for i := range out {
		out[i].Default = false
	}
	return out, err
}

// list returns the datasets whose metadata keep accepts.
func list(ctx context.Context, c Catalog, keep func(graveler.RepositoryMetadata) bool) ([]Dataset, error) {
	var out []Dataset
	after := ""
	for {
		page, more, err := c.ListRepositories(ctx, 100, repos.DatasetPrefix, "", after)
		if err != nil {
			return nil, err
		}
		for _, r := range page {
			after = r.Name
			md, err := c.GetRepositoryMetadata(ctx, r.Name)
			if err != nil {
				return nil, err
			}
			if md[repos.KeyKind] != repos.KindDataset || !keep(md) {
				continue
			}
			d := Dataset{Name: r.Name, Default: md[repos.KeyDefault] == "true",
				Description: md[repos.KeyDescription], Location: md[repos.KeyLocation], Template: md[repos.KeyTemplate]}
			if d.Versions, err = versions(ctx, c, r.Name); err != nil {
				return nil, err
			}
			if head, err := c.GetBranchReference(ctx, r.Name, r.DefaultBranch); err == nil {
				d.Latest = head
			}
			out = append(out, d)
		}
		if !more || len(page) == 0 {
			return out, nil
		}
	}
}

// versions lists repo's vN tags, newest first, then every other tag by name, as named versions.
// The k-* tags of kept runs are left out: they are not names anyone picks.
func versions(ctx context.Context, c Catalog, repo string) ([]Version, error) {
	numbered, named := []Version{}, []Version{}
	after := ""
	for {
		page, more, err := c.ListTags(ctx, repo, "", 1000, after)
		if err != nil {
			return nil, err
		}
		for _, t := range page {
			after = t.ID
			switch {
			case repos.VersionTag.MatchString(t.ID):
				numbered = append(numbered, Version{Tag: t.ID, Commit: t.CommitID})
			case repos.VersionName(t.ID):
				named = append(named, Version{Tag: t.ID, Commit: t.CommitID})
			}
		}
		if !more || len(page) == 0 {
			break
		}
	}
	n := func(tag string) int { v, _ := strconv.Atoi(strings.TrimPrefix(tag, "v")); return v }
	slices.SortFunc(numbered, func(a, b Version) int { return n(b.Tag) - n(a.Tag) })
	slices.SortFunc(named, func(a, b Version) int { return strings.Compare(a.Tag, b.Tag) })
	return append(numbered, named...), nil
}

// Handler answers GET /kaos/v1/catalog to pods of Namespaces, by their service-account token.
type Handler struct {
	Catalog    Catalog
	Verify     func(ctx context.Context, token string, namespaces []string) (string, error)
	Namespaces []string
	// Shared, when set, returns the datasets sibling projects share with this one, named
	// <owner>/<dataset>, to list after the project's own (pkg/kaos/sharing). It never fails: a
	// peer that cannot be read is left out.
	Shared func(ctx context.Context) []Dataset
}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"message": "a service-account token is required as bearer"})
		return
	}
	if _, err := h.Verify(r.Context(), token, h.Namespaces); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"message": "token not accepted for the catalog"})
		return
	}
	datasets, err := List(r.Context(), h.Catalog)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": err.Error()})
		return
	}
	if h.Shared != nil {
		datasets = append(datasets, h.Shared(r.Context())...)
	}
	writeJSON(w, http.StatusOK, map[string]any{"datasets": datasets})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
