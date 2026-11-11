// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-03/F-05): new file, Apache-2.0.

package repos

import (
	"context"
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

// MergeNeedsExpectedHead reports whether a merge into branch of a repository labelled md must name
// the head it expects (PRD novelcore/kaos#1911 CON-03): main of a dataset a pipeline produces.
// Each promotion replaces main's content with one run's, and a merge keeps what only the
// destination has, so a promotion staged from an older head would mix two runs.
func MergeNeedsExpectedHead(md graveler.RepositoryMetadata, branch string) bool {
	return md[KeyProducer] != "" && branch == protectedBranch
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
	err := forEachRepository(ctx, c, DatasetPrefix, func(r *catalog.Repository) error {
		md, err := c.GetRepositoryMetadata(ctx, r.Name)
		if err != nil || md[KeyKind] != KindDataset {
			return err
		}
		changed, err := protect(ctx, p, r.Name)
		if changed {
			changes = append(changes, fmt.Sprintf("protected main of %s: only a merge may change it", r.Name))
		}
		return err
	})
	return changes, err
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
