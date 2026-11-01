// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-07, D-05): new file, Apache-2.0.

// Package datacatalog serves the project's datasets and their versions, read-only, to the trusted
// step that offers them in pipelines' dataset dropdown: names, versions and locations, never data.
// Callers are pods of the project's own namespaces, by their service-account token.
package datacatalog

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
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

// Version is one released version of a dataset.
type Version struct {
	Tag    string `json:"tag"`
	Commit string `json:"commit"`
}

// Dataset is one dataset of the catalog.
type Dataset struct {
	Name        string    `json:"name"`
	Default     bool      `json:"default,omitempty"`
	Description string    `json:"description,omitempty"`
	Location    string    `json:"location,omitempty"`
	Latest      string    `json:"latest,omitempty"` // main's head commit
	Versions    []Version `json:"versions"`         // newest first
}

var versionTag = regexp.MustCompile(`^v(\d+)$`)

// List returns the datasets (dataset-* repositories labelled a dataset) with their versions.
func List(ctx context.Context, c Catalog) ([]Dataset, error) {
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
			if md[repos.KeyKind] != repos.KindDataset {
				continue
			}
			d := Dataset{Name: r.Name, Default: md[repos.KeyDefault] == "true",
				Description: md[repos.KeyDescription], Location: md[repos.KeyLocation]}
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

func versions(ctx context.Context, c Catalog, repo string) ([]Version, error) {
	out := []Version{}
	after := ""
	for {
		page, more, err := c.ListTags(ctx, repo, "v", 1000, after)
		if err != nil {
			return nil, err
		}
		for _, t := range page {
			after = t.ID
			if versionTag.MatchString(t.ID) {
				out = append(out, Version{Tag: t.ID, Commit: t.CommitID})
			}
		}
		if !more || len(page) == 0 {
			break
		}
	}
	n := func(tag string) int { v, _ := strconv.Atoi(strings.TrimPrefix(tag, "v")); return v }
	slices.SortFunc(out, func(a, b Version) int { return n(b.Tag) - n(a.Tag) })
	return out, nil
}

// Handler answers GET /kaos/v1/catalog to pods of Namespaces, by their service-account token.
type Handler struct {
	Catalog    Catalog
	Verify     func(ctx context.Context, token string, namespaces []string) (string, error)
	Namespaces []string
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
	writeJSON(w, http.StatusOK, map[string]any{"datasets": datasets})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
