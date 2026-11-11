// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-08): new file, Apache-2.0.

package declared

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/treeverse/lakefs/pkg/auth"
	"github.com/treeverse/lakefs/pkg/auth/model"
	"github.com/treeverse/lakefs/pkg/kaos/repos"
	"github.com/treeverse/lakefs/pkg/permissions"
)

// AppPolicyPrefix starts the name of each app user's own policy: KAOSApp-<app>.
const AppPolicyPrefix = "KAOSApp-"

var (
	readActions = []string{"fs:Read*", "fs:List*"}
	// writeActions is upstream's FSReadWrite set: read, write, commit, branch and tag, never
	// deleting the repository itself or changing its settings.
	writeActions = []string{
		"fs:Read*", "fs:List*",
		permissions.WriteObjectAction, permissions.DeleteObjectAction, permissions.RevertBranchAction,
		permissions.CreateBranchAction, permissions.DeleteBranchAction,
		permissions.CreateTagAction, permissions.DeleteTagAction, permissions.CreateCommitAction,
	}
)

// The branches and tags of a dataset its producer works with (PRD novelcore/kaos#1911 D-04): a run
// stages on its own run-* branch, a promotion merges it into main, and a kept version is tagged k-*.
const (
	runBranches    = "run-*"
	promotedBranch = "main"
	keptTags       = repos.KeptTagPrefix + "*"
)

// AppPolicyName is the name of the policy of app user user.
func AppPolicyName(user string) string { return AppPolicyPrefix + strings.TrimPrefix(user, UserPrefix) }

// AppPolicy is what an app may do (PRD novelcore/kaos#1283 D-04, T-01): read every dataset;
// read and write its own outputs repository; and, while the project still has apps from before
// outputs repositories, read and write legacyRepository. Nothing else: not another app's outputs,
// not a dataset. Except the datasets it produces (PRD novelcore/kaos#1911 D-04), produced: those
// it declares that are ready (WithProducedDatasets). On those it also writes and deletes objects,
// creates and deletes run-* branches, commits on run-* and on main, and creates k-* tags. Never a
// v* tag, another branch, or the repository's settings. The commit right on main lands only a
// merge: main is protected before the right is given, and the core refuses a direct commit, a
// revert, a cherry-pick and an import on a protected branch.
func AppPolicy(a App, legacyRepository string, produced []string) *model.Policy {
	var st model.Statements
	allow := func(actions []string, resource string) {
		st = append(st, model.Statement{Effect: model.StatementEffectAllow, Action: actions, Resource: resource})
	}
	// Names only, and the server's configuration, which API clients read on start.
	allow([]string{permissions.ListRepositoriesAction, permissions.ReadConfigAction}, permissions.All)
	for _, r := range []string{permissions.RepoArn(repos.DatasetPrefix + "*"), permissions.RepoArn(repos.DatasetPrefix+"*") + "/*"} {
		allow(readActions, r)
	}
	for _, name := range []string{a.OutputsRepository, legacyRepository} {
		if name == "" {
			continue
		}
		for _, r := range []string{permissions.RepoArn(name), permissions.RepoArn(name) + "/*"} {
			allow(writeActions, r)
		}
	}
	for _, d := range produced {
		allow([]string{permissions.WriteObjectAction, permissions.DeleteObjectAction}, permissions.ObjectArn(d, "*"))
		allow([]string{permissions.CreateBranchAction, permissions.DeleteBranchAction, permissions.CreateCommitAction},
			permissions.BranchArn(d, runBranches))
		allow([]string{permissions.CreateCommitAction}, permissions.BranchArn(d, promotedBranch))
		allow([]string{permissions.CreateTagAction}, permissions.TagArn(d, keptTags))
	}
	return &model.Policy{CreatedAt: time.Now(), DisplayName: AppPolicyName(a.User), Statement: st}
}

// ensureAppPolicy makes a's policy exist as AppPolicy says, attach to a and only to a, and
// detaches any other policy from a (drift).
func ensureAppPolicy(ctx context.Context, svc auth.Service, a App, legacyRepository string, produced []string, note func(string, ...any)) error {
	want := AppPolicy(a, legacyRepository, produced)
	have, err := svc.GetPolicy(ctx, want.DisplayName)
	switch {
	case errors.Is(err, auth.ErrNotFound):
		if err := svc.WritePolicy(ctx, want, false); err != nil {
			return fmt.Errorf("create policy %s: %w", want.DisplayName, err)
		}
		note("created policy %s", want.DisplayName)
	case err != nil:
		return fmt.Errorf("get policy %s: %w", want.DisplayName, err)
	case !slices.EqualFunc(have.Statement, want.Statement, sameStatement):
		if err := svc.WritePolicy(ctx, want, true); err != nil {
			return fmt.Errorf("update policy %s: %w", want.DisplayName, err)
		}
		note("updated policy %s", want.DisplayName)
	}
	attached, _, err := svc.ListUserPolicies(ctx, a.User, &model.PaginationParams{Amount: -1})
	if err != nil {
		return fmt.Errorf("list policies: %w", err)
	}
	has := false
	for _, p := range attached {
		if p.DisplayName == want.DisplayName {
			has = true
			continue
		}
		if err := svc.DetachPolicyFromUser(ctx, p.DisplayName, a.User); err != nil {
			return fmt.Errorf("detach %s: %w", p.DisplayName, err)
		}
		note("detached policy %s from %s", p.DisplayName, a.User)
	}
	if !has {
		if err := svc.AttachPolicyToUser(ctx, want.DisplayName, a.User); err != nil {
			return fmt.Errorf("attach %s: %w", want.DisplayName, err)
		}
		note("attached policy %s to %s", want.DisplayName, a.User)
	}
	return nil
}

// removeStaleAppPolicies deletes the KAOSApp-* policies of apps that are no longer declared.
func removeStaleAppPolicies(ctx context.Context, svc auth.Service, declared map[string]bool, note func(string, ...any)) error {
	params := &model.PaginationParams{Prefix: AppPolicyPrefix, Amount: -1}
	for {
		policies, page, err := svc.ListPolicies(ctx, params)
		if err != nil {
			return fmt.Errorf("list app policies: %w", err)
		}
		for _, p := range policies {
			if !strings.HasPrefix(p.DisplayName, AppPolicyPrefix) || declared[UserPrefix+strings.TrimPrefix(p.DisplayName, AppPolicyPrefix)] {
				continue
			}
			if err := svc.DeletePolicy(ctx, p.DisplayName); err != nil && !errors.Is(err, auth.ErrNotFound) {
				return fmt.Errorf("delete policy %s: %w", p.DisplayName, err)
			}
			note("deleted policy %s", p.DisplayName)
		}
		if page == nil || page.NextPageToken == "" {
			return nil
		}
		params.After = page.NextPageToken
	}
}
