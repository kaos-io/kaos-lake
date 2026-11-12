// KAOS (kaos-lake, PRD novelcore/kaos#1911 CON-03): new file, Apache-2.0.

package repos

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/treeverse/lakefs/pkg/catalog"
)

const (
	// strayNames is how many discarded objects a log line names.
	strayNames = 20
	// strayPage is how many uncommitted changes are read at a time.
	strayPage = 1000
)

// StrayCatalog is what ReconcileStrays needs of the lakeFS catalog.
type StrayCatalog interface {
	DiffUncommitted(ctx context.Context, repositoryID, branch, prefix, delimiter string, limit int, after string) (catalog.Differences, bool, error)
	// DiscardUncommitted drops the uncommitted changes of a branch, protected or not.
	DiscardUncommitted(ctx context.Context, repositoryID string, branch string) error
}

// ReconcileStrays discards the uncommitted changes on main of every repository labelled
// kaos.producer (PRD novelcore/kaos#1911 CON-03). That main moves only by a merge, and no run
// stages on it, so an uncommitted object there is not a run's: it got there while an admin had
// the protection off, it can never be committed, and lakeFS refuses every merge into the branch
// while it is there. Nobody can reset a protected branch through the API, so the platform does.
// Committed data, other branches and other repositories are never touched. A repository whose
// state cannot be read is left alone and reported. It returns a description of each discard.
func ReconcileStrays(ctx context.Context, c Catalog, h StrayCatalog) ([]string, error) {
	var changes []string
	var failed []error
	err := forEachRepository(ctx, c, DatasetPrefix, func(r *catalog.Repository) error {
		md, err := c.GetRepositoryMetadata(ctx, r.Name)
		if err != nil {
			failed = append(failed, fmt.Errorf("repository %s: %w", r.Name, err))
			return nil
		}
		if md[KeyProducer] == "" {
			return nil
		}
		count, names, err := strays(ctx, h, r.Name)
		if err != nil {
			failed = append(failed, fmt.Errorf("repository %s: uncommitted changes not read, nothing discarded: %w", r.Name, err))
			return nil
		}
		if count == 0 {
			return nil
		}
		if err := h.DiscardUncommitted(ctx, r.Name, protectedBranch); err != nil {
			failed = append(failed, fmt.Errorf("repository %s: %d uncommitted objects not discarded: %w", r.Name, count, err))
			return nil
		}
		more := ""
		if count > len(names) {
			more = ", …"
		}
		changes = append(changes, fmt.Sprintf("discarded %d uncommitted objects on %s of %s, a dataset produced by %s: no run wrote them: %s%s",
			count, protectedBranch, r.Name, md[KeyProducer], strings.Join(names, ", "), more))
		return nil
	})
	if err != nil {
		failed = append(failed, err)
	}
	return changes, errors.Join(failed...)
}

// strays counts the uncommitted changes on main of repo and names the first of them.
func strays(ctx context.Context, h StrayCatalog, repo string) (int, []string, error) {
	count, after := 0, ""
	var names []string
	for {
		page, more, err := h.DiffUncommitted(ctx, repo, protectedBranch, "", "", strayPage, after)
		if err != nil {
			return 0, nil, err
		}
		for _, d := range page {
			if len(names) < strayNames {
				names = append(names, d.Path)
			}
			after = d.Path
		}
		count += len(page)
		if !more || len(page) == 0 {
			return count, names, nil
		}
	}
}
