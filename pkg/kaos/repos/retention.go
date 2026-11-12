// KAOS (kaos-lake, PRD novelcore/kaos#1911 CON-03): new file, Apache-2.0.

package repos

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/treeverse/lakefs/pkg/catalog"
	"github.com/treeverse/lakefs/pkg/graveler"
)

const (
	// RunBranchPrefix names the branches a pipeline step writes on, one per run (PRD
	// novelcore/kaos#1911 CON-03); the tags that keep each run's version are KeptTagPrefix's.
	RunBranchPrefix = "run-"
	// MinRunBranchTTL is the shortest age at which a run branch may be removed: far above any
	// step's time, so that a run still writing never loses its branch.
	MinRunBranchTTL = time.Hour

	retentionPage = 1000
)

// Retention is how long the platform keeps what pipelines leave in produced datasets.
type Retention struct {
	// RunBranchTTL is the age after which a run-* branch is removed; zero: never (a value under
	// MinRunBranchTTL is refused by the config and by ReconcileRunBranches).
	RunBranchTTL time.Duration
	// KeepDays is the age in days after which a version no promotion or other tag keeps loses its
	// k-* tag; zero: never.
	KeepDays int
}

// RetentionCatalog is what the retention passes need of the lakeFS catalog.
type RetentionCatalog interface {
	ListBranches(ctx context.Context, repositoryID string, prefix string, limit int, after string, opts ...graveler.ListOptionsFunc) ([]*catalog.Branch, bool, error)
	DeleteBranch(ctx context.Context, repositoryID string, branch string, opts ...graveler.SetOptionsFunc) error
	DiffUncommitted(ctx context.Context, repositoryID, branch, prefix, delimiter string, limit int, after string) (catalog.Differences, bool, error)
	ListCommits(ctx context.Context, repositoryID string, ref string, params catalog.LogParams) ([]*catalog.CommitLog, bool, error)
	ListTags(ctx context.Context, repositoryID string, prefix string, limit int, after string) ([]*catalog.Tag, bool, error)
	DeleteTag(ctx context.Context, repositoryID string, tagID string, opts ...graveler.SetOptionsFunc) error
}

var _ RetentionCatalog = (*catalog.Catalog)(nil)

// ReconcileRunBranches deletes the run-* branches of every produced dataset (label kaos.producer)
// that nothing has written to for longer than ttl: a killed pod or a cancelled run leaves its
// branch, and the objects staged on it, for ever (PRD novelcore/kaos#1911 CON-03).
//
// A branch's age is the newest of its own head commit (when the head is not one of main's) and
// its uncommitted objects. lakeFS records no creation time for a branch, and a new branch's head
// is main's, however old: so a branch with neither a commit of its own nor an uncommitted object
// has no age, and is kept. Main and branches not named run-* are never touched, nor repositories
// that are not produced datasets. A failure on one branch or repository does not stop the others.
func ReconcileRunBranches(ctx context.Context, c Catalog, h RetentionCatalog, ttl time.Duration, now time.Time) ([]string, error) {
	if ttl < MinRunBranchTTL {
		return nil, fmt.Errorf("run branch age %s is under the minimum %s: nothing deleted", ttl, MinRunBranchTTL)
	}
	var changes []string
	var failed []error
	err := forEachProduced(ctx, c, &failed, func(repo, producer string) {
		after := ""
		var main *history // main's history, read when a branch needs it
		for {
			page, more, err := h.ListBranches(ctx, repo, RunBranchPrefix, retentionPage, after)
			if err != nil {
				failed = append(failed, fmt.Errorf("repository %s: branches not read: %w", repo, err))
				return
			}
			for _, b := range page {
				after = b.Name
				if !strings.HasPrefix(b.Name, RunBranchPrefix) || b.Name == protectedBranch {
					continue
				}
				newest, err := lastWrite(ctx, h, repo, b, &main)
				if err != nil {
					failed = append(failed, fmt.Errorf("repository %s: branch %s: age not read, kept: %w", repo, b.Name, err))
					continue
				}
				if newest.IsZero() || now.Sub(newest) < ttl {
					continue
				}
				if err := h.DeleteBranch(ctx, repo, b.Name); err != nil && !errors.Is(err, graveler.ErrNotFound) {
					failed = append(failed, fmt.Errorf("repository %s: branch %s not deleted: %w", repo, b.Name, err))
					continue
				}
				changes = append(changes, fmt.Sprintf("deleted branch %s of %s, a dataset produced by %s: last written %s, over %s ago",
					b.Name, repo, producer, newest.UTC().Format(time.RFC3339), ttl))
			}
			if !more || len(page) == 0 {
				return
			}
		}
	})
	if err != nil {
		failed = append(failed, err)
	}
	return changes, errors.Join(failed...)
}

// lastWrite is the newest sign of a write on branch b; zero when there is none.
func lastWrite(ctx context.Context, h RetentionCatalog, repo string, b *catalog.Branch, main **history) (time.Time, error) {
	var newest time.Time
	head, err := commitOf(ctx, h, repo, b.Reference)
	if err != nil {
		return time.Time{}, err
	}
	if *main == nil {
		if *main, err = mainHistory(ctx, h, repo); err != nil {
			return time.Time{}, err
		}
	}
	if !(*main).commits[b.Reference] {
		newest = head.CreationDate
	}
	after := ""
	for {
		page, more, err := h.DiffUncommitted(ctx, repo, b.Name, "", "", retentionPage, after)
		if err != nil {
			return time.Time{}, err
		}
		for _, d := range page {
			after = d.Path
			if d.CreationDate.After(newest) {
				newest = d.CreationDate
			}
		}
		if !more || len(page) == 0 {
			return newest, nil
		}
	}
}

// commitOf reads one commit through the log, which is all the retention interface has.
func commitOf(ctx context.Context, h RetentionCatalog, repo, ref string) (*catalog.CommitLog, error) {
	log, _, err := h.ListCommits(ctx, repo, ref, catalog.LogParams{Amount: 1, Limit: true})
	if err != nil {
		return nil, err
	}
	if len(log) == 0 {
		return nil, fmt.Errorf("commit %s: %w", ref, graveler.ErrNotFound)
	}
	return log[0], nil
}

// history is what main holds: every commit reachable from it, and the content (metarange) of each.
type history struct{ commits, contents map[string]bool }

// holds reports whether commit cm is one of main's or has the content of one: a promotion that
// squashes leaves the run's own commit out of main's ancestry but main holds what it held.
func (m *history) holds(cm *catalog.CommitLog) bool {
	return m.commits[cm.Reference] || (cm.MetaRangeID != "" && m.contents[cm.MetaRangeID])
}

func mainHistory(ctx context.Context, h RetentionCatalog, repo string) (*history, error) {
	seen := &history{commits: map[string]bool{}, contents: map[string]bool{}}
	from := ""
	for {
		log, more, err := h.ListCommits(ctx, repo, protectedBranch, catalog.LogParams{Amount: retentionPage, FromReference: from})
		if err != nil {
			return nil, err
		}
		for _, cm := range log {
			seen.commits[cm.Reference] = true
			seen.contents[cm.MetaRangeID] = true
			from = cm.Reference
		}
		if !more || len(log) == 0 {
			return seen, nil
		}
	}
}

// forEachProduced calls fn for each dataset labelled kaos.producer; a repository whose labels
// cannot be read is reported in failed and skipped.
func forEachProduced(ctx context.Context, c Catalog, failed *[]error, fn func(repo, producer string)) error {
	return forEachRepository(ctx, c, DatasetPrefix, func(r *catalog.Repository) error {
		md, err := c.GetRepositoryMetadata(ctx, r.Name)
		if err != nil {
			*failed = append(*failed, fmt.Errorf("repository %s: %w", r.Name, err))
			return nil
		}
		if md[KeyProducer] != "" {
			fn(r.Name, md[KeyProducer])
		}
		return nil
	})
}

// ReconcileKeptVersions deletes the k-* tags older than keepDays days from every produced dataset
// (PRD novelcore/kaos#1911 CON-03): each run's version is kept by one, and one that was never
// promoted stops being kept after that. A tag stays when its commit is reachable from main (it was
// promoted, or is part of what main holds, by ancestry or by content) or when a tag that is not a k-* tag points to the same
// commit (a model, or a name someone gave it). Age is the tagged commit's creation date. Only the
// tag goes: no object is deleted. keepDays 0 keeps every tag. A failure on one tag or repository
// does not stop the others.
func ReconcileKeptVersions(ctx context.Context, c Catalog, h RetentionCatalog, keepDays int, now time.Time) ([]string, error) {
	if keepDays < 0 {
		return nil, fmt.Errorf("keep days %d is negative: nothing deleted", keepDays)
	}
	if keepDays == 0 {
		return nil, nil
	}
	maxAge := time.Duration(keepDays) * 24 * time.Hour
	var changes []string
	var failed []error
	err := forEachProduced(ctx, c, &failed, func(repo, producer string) {
		tags, err := allTags(ctx, h, repo)
		if err != nil {
			failed = append(failed, fmt.Errorf("repository %s: tags not read, none deleted: %w", repo, err))
			return
		}
		named := map[string]bool{} // commits a tag that is not a k-* tag points to
		for _, t := range tags {
			if VersionName(t.ID) {
				named[t.CommitID] = true
			}
		}
		var main *history
		for _, t := range tags {
			if !strings.HasPrefix(t.ID, KeptTagPrefix) || named[t.CommitID] {
				continue
			}
			cm, err := commitOf(ctx, h, repo, t.CommitID)
			if err != nil {
				failed = append(failed, fmt.Errorf("repository %s: tag %s: commit not read, kept: %w", repo, t.ID, err))
				continue
			}
			if now.Sub(cm.CreationDate) < maxAge {
				continue
			}
			if main == nil {
				if main, err = mainHistory(ctx, h, repo); err != nil {
					failed = append(failed, fmt.Errorf("repository %s: main's history not read, no tag deleted: %w", repo, err))
					return
				}
			}
			if main.holds(cm) {
				continue
			}
			if err := h.DeleteTag(ctx, repo, t.ID); err != nil && !errors.Is(err, graveler.ErrNotFound) {
				failed = append(failed, fmt.Errorf("repository %s: tag %s not deleted: %w", repo, t.ID, err))
				continue
			}
			changes = append(changes, fmt.Sprintf("deleted tag %s of %s, a dataset produced by %s: its version is %s old and was never promoted",
				t.ID, repo, producer, now.Sub(cm.CreationDate).Truncate(time.Hour)))
		}
	})
	if err != nil {
		failed = append(failed, err)
	}
	return changes, errors.Join(failed...)
}

func allTags(ctx context.Context, h RetentionCatalog, repo string) ([]*catalog.Tag, error) {
	var tags []*catalog.Tag
	after := ""
	for {
		page, more, err := h.ListTags(ctx, repo, "", retentionPage, after)
		if err != nil {
			return nil, err
		}
		tags = append(tags, page...)
		if !more || len(page) == 0 {
			return tags, nil
		}
		after = page[len(page)-1].ID
	}
}
