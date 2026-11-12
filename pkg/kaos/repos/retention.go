// KAOS (kaos-lake, PRD novelcore/kaos#1911 CON-03): new file, Apache-2.0.

package repos

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/treeverse/lakefs/pkg/catalog"
	"github.com/treeverse/lakefs/pkg/graveler"
	"github.com/treeverse/lakefs/pkg/logging"
)

const (
	// RunBranchPrefix names the branches a pipeline step writes on, one per run (PRD
	// novelcore/kaos#1911 CON-03); the tags that keep each run's version are KeptTagPrefix's.
	RunBranchPrefix = "run-"
	// MinRunBranchTTL is the shortest age at which a run branch may be removed. A living run
	// branch is one whose upload is in progress (objects staged, not yet committed); a step's
	// upload takes hours at most, so a day is far above it.
	MinRunBranchTTL = 24 * time.Hour
	// MinRetentionInterval is the shortest time between two retention passes.
	MinRetentionInterval = 5 * time.Minute
	// KeyPromoted is the commit metadata a promotion sets on the merge commit: the full ID of the
	// sealed commit it merged. A tag whose commit is named there is kept.
	KeyPromoted = "kaos.promoted"

	retentionPage = 1000
	// maxTagDeletes and maxTagCommitReads bound what one pass does in one repository; the rest
	// is done by the next pass.
	maxTagDeletes     = 200
	maxTagCommitReads = 500
	// maxBranchDeletes bounds the run branches deleted in one repository in one pass.
	maxBranchDeletes = 200
)

// Retention is how long the platform keeps what pipelines leave in produced datasets.
type Retention struct {
	// RunBranchTTL is the age after which a run-* branch is removed; zero: never. Otherwise at
	// least MinRunBranchTTL (the config refuses less).
	RunBranchTTL time.Duration
	// KeepDays is the age in days after which a version no promotion or other tag keeps loses its
	// k-* tag; zero: never.
	KeepDays int
	// Interval is the time between retention passes, at least MinRetentionInterval; zero: the
	// passes do not run.
	Interval time.Duration
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

// historyPage is how many commits of main's history are read at a time: one call for any ordinary
// project, because each further call skips the commits already read.
var historyPage = 10000

var _ RetentionCatalog = (*catalog.Catalog)(nil)

// RunRetention runs the retention passes every ret.Interval until ctx ends, on their own, so that
// they never delay the creation and protection of repositories.
func RunRetention(ctx context.Context, c Catalog, ret Retention, log logging.Logger) {
	h, ok := c.(RetentionCatalog)
	if !ok || ret.Interval <= 0 || (ret.RunBranchTTL <= 0 && ret.KeepDays <= 0) {
		return
	}
	log = log.WithField("service", "kaos_retention")
	ticker := time.NewTicker(ret.Interval)
	defer ticker.Stop()
	for {
		if ret.RunBranchTTL > 0 {
			changes, err := ReconcileRunBranches(ctx, c, h, ret.RunBranchTTL, time.Now())
			for _, ch := range changes {
				log.Info(ch)
			}
			if err != nil {
				log.WithError(err).Error("Leftover run branches not cleared")
			}
		}
		if ret.KeepDays > 0 {
			changes, err := ReconcileKeptVersions(ctx, c, h, ret.KeepDays, time.Now())
			for _, ch := range changes {
				log.Info(ch)
			}
			if err != nil {
				log.WithError(err).Error("Expired kept versions not cleared")
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// ReconcileRunBranches deletes the run-* branches of every produced dataset (label kaos.producer)
// that nothing has written to for longer than ttl: a killed pod or a cancelled run leaves its
// branch, and the objects staged on it, for ever (PRD novelcore/kaos#1911 CON-03).
//
// A run branch is alive while its upload is in progress, so a branch's age is the newest of its
// own head commit (when the head is not one of main's) and its uncommitted objects: a step that
// keeps staging objects keeps its branch, and ttl is far above the time between two of them.
// lakeFS records no creation time for a branch, and a new branch's head is main's, however old:
// so a branch with neither a commit of its own nor an uncommitted object has no age, and is kept.
// Main and branches not named run-* are never touched, nor repositories that are not produced
// datasets. At most maxBranchDeletes branches go from one repository in one pass. A failure on
// one branch or repository does not stop the others.
func ReconcileRunBranches(ctx context.Context, c Catalog, h RetentionCatalog, ttl time.Duration, now time.Time) ([]string, error) {
	if ttl < MinRunBranchTTL {
		return nil, fmt.Errorf("run branch age %s is under the minimum %s: nothing deleted", ttl, MinRunBranchTTL)
	}
	var changes []string
	var failed []error
	err := forEachProduced(ctx, c, &failed, func(repo, producer string) {
		after, deleted := "", 0
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
				if deleted >= maxBranchDeletes {
					return
				}
				if err := h.DeleteBranch(ctx, repo, b.Name); err != nil && !errors.Is(err, graveler.ErrNotFound) {
					failed = append(failed, fmt.Errorf("repository %s: branch %s not deleted: %w", repo, b.Name, err))
					continue
				}
				deleted++
				changes = append(changes, fmt.Sprintf("deleted branch %s of %s (dataset produced by %s), it was at commit %s: nothing written since %s, %s ago, over run_branch_ttl %s; restore with a branch of that name at that commit (staged objects are gone)",
					b.Name, repo, producer, b.Reference, newest.UTC().Format(time.RFC3339), now.Sub(newest).Truncate(time.Minute), ttl))
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

// history is what main holds: every commit reachable from it, the content (metarange) of each,
// and the sealed commits its commits name as promoted (KeyPromoted).
type history struct{ commits, contents, promoted map[string]bool }

// holds reports whether commit cm is part of main: one of its commits, one with the same content
// (a promotion that squashes leaves the run's own commit out of main's ancestry), or one a
// promotion named (a squash into a main that moved holds the union of two runs, which equals
// neither).
func (m *history) holds(cm *catalog.CommitLog) bool {
	return m.commits[cm.Reference] || m.promoted[cm.Reference] || (cm.MetaRangeID != "" && m.contents[cm.MetaRangeID])
}

// mainHistory reads main's log once, newest first: each further page would skip the commits
// already read, so a page is large (historyPage) and the next starts at the last commit read.
func mainHistory(ctx context.Context, h RetentionCatalog, repo string) (*history, error) {
	seen := &history{commits: map[string]bool{}, contents: map[string]bool{}, promoted: map[string]bool{}}
	from := ""
	for {
		log, more, err := h.ListCommits(ctx, repo, protectedBranch, catalog.LogParams{Amount: historyPage, FromReference: from})
		if err != nil {
			return nil, err
		}
		for _, cm := range log {
			seen.commits[cm.Reference] = true
			seen.contents[cm.MetaRangeID] = true
			if id := cm.Metadata[KeyPromoted]; id != "" {
				seen.promoted[id] = true
			}
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

// keptTime is the UTC time of sealing in a kept tag's name: k-<YYYYMMDDTHHMMSSZ>-<run>-<step>-<commit12>.
var keptTime = regexp.MustCompile(`^k-(\d{8}T\d{6}Z)-.+`)

// sealedAt is the time in the name of kept tag id; false for the old form k-<run>-<step>-<commit12>
// and for a name whose time does not parse.
func sealedAt(id string) (time.Time, bool) {
	m := keptTime.FindStringSubmatch(id)
	if m == nil {
		return time.Time{}, false
	}
	t, err := time.Parse("20060102T150405Z", m[1])
	return t, err == nil
}

// ReconcileKeptVersions deletes the k-* tags older than keepDays days from every produced dataset
// (PRD novelcore/kaos#1911 CON-03): each run's version is kept by one, and one that was never
// promoted stops being kept after that. A tag's age is the time of sealing in its name; a k-* tag
// without one (the old form) is as old as its commit. A tag stays when main holds its commit
// (reachable from it, the same content as one of its commits, or named by a promotion's
// kaos.promoted metadata) or when a tag that is not a k-* tag points to the same commit (`latest`,
// a model, a name someone gave). Only the tag goes: no object is deleted. keepDays 0 keeps every
// tag. Per repository and pass at most maxTagCommitReads commits are read and maxTagDeletes tags
// deleted; the rest is done by the next pass. A failure on one tag or repository does not stop
// the others.
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
		deleted, reads := 0, 0
		read := map[string]*catalog.CommitLog{} // commits read in this pass: many tags may share one
		for _, t := range tags {
			if VersionName(t.ID) || named[t.CommitID] {
				continue
			}
			var sealed time.Time
			var cm *catalog.CommitLog
			if at, ok := sealedAt(t.ID); ok {
				if now.Sub(at) < maxAge {
					continue // by its name: no commit is read for a tag that is not old
				}
				sealed = at
			} else {
				if reads >= maxTagCommitReads {
					continue
				}
				if cm = read[t.CommitID]; cm == nil {
					reads++
					if cm, err = commitOf(ctx, h, repo, t.CommitID); err != nil {
						failed = append(failed, fmt.Errorf("repository %s: tag %s: commit not read, kept: %w", repo, t.ID, err))
						continue
					}
					read[t.CommitID] = cm
				}
				if now.Sub(cm.CreationDate) < maxAge {
					continue
				}
				sealed = cm.CreationDate
			}
			if deleted >= maxTagDeletes {
				return
			}
			if main == nil {
				if main, err = mainHistory(ctx, h, repo); err != nil {
					failed = append(failed, fmt.Errorf("repository %s: main's history not read, no tag deleted: %w", repo, err))
					return
				}
			}
			if main.commits[t.CommitID] || main.promoted[t.CommitID] {
				continue // held by ancestry or by a promotion's metadata: no commit is read
			}
			if cm == nil { // the content rule needs the commit
				if cm = read[t.CommitID]; cm == nil {
					if reads >= maxTagCommitReads {
						continue
					}
					reads++
					if cm, err = commitOf(ctx, h, repo, t.CommitID); err != nil {
						failed = append(failed, fmt.Errorf("repository %s: tag %s: commit not read, kept: %w", repo, t.ID, err))
						continue
					}
					read[t.CommitID] = cm
				}
			}
			if main.holds(cm) {
				continue
			}
			if err := h.DeleteTag(ctx, repo, t.ID); err != nil && !errors.Is(err, graveler.ErrNotFound) {
				failed = append(failed, fmt.Errorf("repository %s: tag %s not deleted: %w", repo, t.ID, err))
				continue
			}
			deleted++
			changes = append(changes, fmt.Sprintf("deleted tag %s of %s (dataset produced by %s), it pointed at commit %s: sealed %s, %s ago, over keep_days %d and never promoted; restore with a tag of that name at that commit",
				t.ID, repo, producer, t.CommitID, sealed.UTC().Format(time.RFC3339), now.Sub(sealed).Truncate(time.Hour), keepDays))
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
