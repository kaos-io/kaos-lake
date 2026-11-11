// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-03/F-05): new file, Apache-2.0.

package repos

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/treeverse/lakefs/pkg/catalog"
	"github.com/treeverse/lakefs/pkg/graveler"
)

// protectedBranch is the branch of a dataset that only a merge may move (PRD: a dataset's main
// changes through pull requests).
const protectedBranch = "main"

// protectedActions are blocked on protectedBranch: writing to its staging area and committing.
// A merge into it is neither, so pull requests still land.
var protectedActions = []graveler.BranchProtectionBlockedAction{
	graveler.BranchProtectionBlockedAction_STAGING_WRITE,
	graveler.BranchProtectionBlockedAction_COMMIT,
}

// ProtectionCatalog is what ReconcileProtection needs of the lakeFS catalog.
type ProtectionCatalog interface {
	GetBranchProtectionRules(ctx context.Context, repositoryID string) (*graveler.BranchProtectionRules, *string, error)
	SetBranchProtectionRules(ctx context.Context, repositoryID string, rules *graveler.BranchProtectionRules, lastKnownChecksum *string) error
}

// ReconcileProtection gives main of every repository labelled kaos.kind=dataset the branch
// protection rule that blocks staging writes and commits, adding only what is missing and leaving
// other rules alone. It keys off the label, not the name, and never removes a rule: a repository
// that loses its label stays protected. Outputs repositories are not touched (apps merge runs into
// main). It returns a description of each change.
func ReconcileProtection(ctx context.Context, c Catalog, p ProtectionCatalog) ([]string, error) {
	var changes []string
	var failed []error
	err := forEachRepository(ctx, c, DatasetPrefix, func(r *catalog.Repository) error {
		// One dataset that cannot be read or protected does not leave the others open.
		md, err := c.GetRepositoryMetadata(ctx, r.Name)
		if err != nil {
			failed = append(failed, fmt.Errorf("repository %s: %w", r.Name, err))
			return nil
		}
		if md[KeyKind] != KindDataset {
			return nil
		}
		changed, err := protect(ctx, p, r.Name)
		if err != nil {
			failed = append(failed, fmt.Errorf("repository %s: %w", r.Name, err))
			return nil
		}
		if changed {
			changes = append(changes, fmt.Sprintf("protected main of %s: only a merge may change it", r.Name))
		}
		return nil
	})
	if err != nil {
		failed = append(failed, err)
	}
	return changes, errors.Join(failed...)
}

// ProducerCatalog is what ProducedBy reads of the lakeFS catalog.
type ProducerCatalog interface {
	GetRepositoryMetadata(ctx context.Context, repository string) (graveler.RepositoryMetadata, error)
	ProtectionCatalog
}

// ProducedBy reports whether dataset is ready for app to write (PRD novelcore/kaos#1911 CON-02,
// CON-03): the repository exists, is labelled kaos.producer=app, and its main refuses direct
// writes and commits. pkg/auth/declared grants an app its write rights on a dataset only while
// this holds, so the app never holds the commit right on a main that is not its own or not
// protected, whichever of the two reconciles runs first.
func ProducedBy(ctx context.Context, c ProducerCatalog, dataset, app string) (bool, error) {
	md, err := c.GetRepositoryMetadata(ctx, dataset)
	if errors.Is(err, graveler.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if app == "" || md[KeyProducer] != app {
		return false, nil
	}
	rules, _, err := c.GetBranchProtectionRules(ctx, dataset)
	if err != nil {
		return false, err
	}
	if rules == nil {
		return false, nil
	}
	blocked := rules.BranchPatternToBlockedActions[protectedBranch]
	if blocked == nil {
		return false, nil
	}
	for _, a := range protectedActions {
		if !slices.Contains(blocked.Value, a) {
			return false, nil
		}
	}
	return true, nil
}

// protect makes sure main of repo blocks protectedActions; it reports whether it changed the rules.
func protect(ctx context.Context, p ProtectionCatalog, repo string) (bool, error) {
	rules, checksum, err := p.GetBranchProtectionRules(ctx, repo)
	if err != nil {
		return false, err
	}
	if rules == nil {
		rules = &graveler.BranchProtectionRules{}
	}
	if rules.BranchPatternToBlockedActions == nil {
		rules.BranchPatternToBlockedActions = map[string]*graveler.BranchProtectionBlockedActions{}
	}
	blocked := rules.BranchPatternToBlockedActions[protectedBranch]
	if blocked == nil {
		blocked = &graveler.BranchProtectionBlockedActions{}
	}
	missing := false
	for _, a := range protectedActions {
		if !slices.Contains(blocked.Value, a) {
			blocked.Value = append(blocked.Value, a)
			missing = true
		}
	}
	if !missing {
		return false, nil
	}
	rules.BranchPatternToBlockedActions[protectedBranch] = blocked
	// An empty checksum means no rule is stored (or an emptied rule set, which lakeFS cannot
	// update conditionally); write unconditionally then.
	if checksum != nil && *checksum == "" {
		checksum = nil
	}
	return true, p.SetBranchProtectionRules(ctx, repo, rules, checksum)
}
