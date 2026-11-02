// KAOS (kaos-lake, dataset layouts): new file, Apache-2.0.

package templates

// The project's layout registry and dataset trees, read from the lakeFS catalog.
//
// The registry is main of the project's repository "layouts": one folder per layout holding its
// layout.yaml. People who may create datasets (Developers, Admins) may create and write that
// repository too, so a project adds or changes a layout with a commit (kaosctl layout publish
// opens a pull request), with lakeFS's history and review, and without a platform release. App
// keys cannot write it. kaos-lake reads it as the server, so any signed-in caller sees the
// project's layouts.

import (
	"context"
	"errors"
	"io"
	"regexp"

	"github.com/treeverse/lakefs/pkg/block"
	"github.com/treeverse/lakefs/pkg/catalog"
	"github.com/treeverse/lakefs/pkg/graveler"
)

// LayoutsRepository is the project's layout registry.
const LayoutsRepository = "layouts"

const (
	maxLayouts     = 100
	listPage       = 1000
	maxListed      = 200_000  // files a Validate looks at
	maxReadFiles   = 5_000    // files whose content a Validate reads (line and manifest rules)
	maxReadBytes   = 64 << 20 // bytes a Validate reads in all
	maxOneFileRead = 4 << 20  // a larger file's content is not read
)

var layoutPath = regexp.MustCompile(`^([^/]+)/layout\.yaml$`)

// Lake is what the store reads of the lakeFS catalog.
type Lake interface {
	GetRepository(ctx context.Context, repository string) (*catalog.Repository, error)
	ListEntries(ctx context.Context, repositoryID string, reference string, prefix string, after string, delimiter string, limit int) ([]*catalog.DBEntry, bool, error)
}

// CatalogStore reads layouts and dataset trees through the catalog and the block adapter.
type CatalogStore struct {
	Catalog Lake
	Blocks  block.Adapter
}

func (s CatalogStore) read(ctx context.Context, repo *catalog.Repository, e *catalog.DBEntry, limit int64) ([]byte, error) {
	r, err := s.Blocks.Get(ctx, block.ObjectPointer{
		StorageID:        repo.StorageID,
		StorageNamespace: repo.StorageNamespace,
		IdentifierType:   e.AddressType.ToIdentifierType(),
		Identifier:       e.PhysicalAddress,
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	return io.ReadAll(io.LimitReader(r, limit))
}

// walk calls fn for every object of ref, up to max; it reports whether it stopped early.
func (s CatalogStore) walk(ctx context.Context, repo, ref string, maxEntries int, fn func(*catalog.DBEntry) error) (bool, error) {
	after, n := "", 0
	for {
		page, more, err := s.Catalog.ListEntries(ctx, repo, ref, "", after, "", listPage)
		if err != nil {
			return false, err
		}
		for _, e := range page {
			if e.CommonLevel {
				continue
			}
			if n == maxEntries {
				return true, nil
			}
			n++
			if err := fn(e); err != nil {
				return false, err
			}
		}
		if !more || len(page) == 0 {
			return false, nil
		}
		after = page[len(page)-1].Path
	}
}

// Layouts returns <name>/layout.yaml of main of the layouts repository, by name (none when the
// project has no such repository).
func (s CatalogStore) Layouts(ctx context.Context) (map[string][]byte, error) {
	repo, err := s.Catalog.GetRepository(ctx, LayoutsRepository)
	if errors.Is(err, graveler.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	_, err = s.walk(ctx, repo.Name, repo.DefaultBranch, maxListed, func(e *catalog.DBEntry) error {
		m := layoutPath.FindStringSubmatch(e.Path)
		if m == nil || len(out) == maxLayouts {
			return nil
		}
		data, err := s.read(ctx, repo, e, maxSpecBytes+1)
		if err != nil {
			return err
		}
		out[m[1]] = data
		return nil
	})
	return out, err
}

// TreeReport is what Validate looked at.
type TreeReport struct {
	Files   int  `json:"files"`
	Read    int  `json:"read"`
	Skipped int  `json:"skipped"` // files whose content the budget left unread
	Partial bool `json:"partial"` // more files than were looked at, or content left unread
}

// Validate checks repository@ref against spec's declarative rules, within the read budget.
func (s CatalogStore) Validate(ctx context.Context, spec Spec, repository, ref string) ([]string, TreeReport, error) {
	var rep TreeReport
	repo, err := s.Catalog.GetRepository(ctx, repository)
	if err != nil {
		return nil, rep, err
	}
	entries := map[string]*catalog.DBEntry{}
	var paths []string
	stopped, err := s.walk(ctx, repository, ref, maxListed, func(e *catalog.DBEntry) error {
		entries[e.Path] = e
		paths = append(paths, e.Path)
		return nil
	})
	if err != nil {
		return nil, rep, err
	}
	rep.Files, rep.Partial = len(paths), stopped
	var bytesRead int64
	problems, err := Check(spec, paths, func(p string) ([]byte, error) {
		e := entries[p]
		if rep.Read >= maxReadFiles || e.Size > maxOneFileRead || bytesRead+e.Size > maxReadBytes {
			rep.Skipped++
			rep.Partial = true
			return nil, ErrNotRead
		}
		rep.Read++
		bytesRead += e.Size
		return s.read(ctx, repo, e, maxOneFileRead)
	})
	return problems, rep, err
}
