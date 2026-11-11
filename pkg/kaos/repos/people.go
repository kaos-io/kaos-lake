// KAOS (kaos-lake, PRD novelcore/kaos#1911 CON-02): new file, Apache-2.0.

package repos

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/treeverse/lakefs/pkg/auth"
	"github.com/treeverse/lakefs/pkg/auth/model"
	"github.com/treeverse/lakefs/pkg/auth/setup"
	"github.com/treeverse/lakefs/pkg/catalog"
	"github.com/treeverse/lakefs/pkg/permissions"
)

// ProducedPolicy is the name of the policy that keeps people from writing the datasets pipelines
// produce.
const ProducedPolicy = "KAOSProducedDatasets"

// peopleGroups are the groups people join from their project role that the policy is attached to.
// Not Admins: an admin can detach any policy and change any rule, so a denial there would only
// look like a guarantee, and repairing a dataset needs someone who can write it.
var peopleGroups = []string{setup.SuperUsersGroup, setup.DevelopersGroup, setup.ViewersGroup}

// PeoplePolicy denies, on each of the produced datasets, what would change its content or its run
// versions: writing and deleting objects, every branch change (create, delete, commit, merge,
// revert, reset), opening or changing pull requests, and creating or deleting the k-* tags of
// kept runs. A denial wins over any
// allowance. Reading stays, and so does naming a version: a tag that is not k-* on a commit that
// exists.
func PeoplePolicy(datasets []string) *model.Policy {
	st := model.Statements{}
	deny := func(resource string, actions ...string) {
		st = append(st, model.Statement{Effect: model.StatementEffectDeny, Action: actions, Resource: resource})
	}
	for _, d := range datasets {
		deny(permissions.ObjectArn(d, "*"), permissions.WriteObjectAction, permissions.DeleteObjectAction)
		deny(permissions.BranchArn(d, "*"), permissions.CreateBranchAction, permissions.DeleteBranchAction,
			permissions.CreateCommitAction, permissions.RevertBranchAction)
		deny(permissions.TagArn(d, KeptTagPrefix+"*"), permissions.CreateTagAction, permissions.DeleteTagAction)
		// Commit records, restored refs and pull requests are asked on the repository itself. A
		// pull request there could never merge, so none is opened or changed.
		deny(permissions.RepoArn(d), permissions.CreateCommitAction, permissions.CreateBranchAction, permissions.CreateTagAction,
			permissions.WritePullRequestAction)
	}
	return &model.Policy{CreatedAt: time.Now(), DisplayName: ProducedPolicy, Statement: st}
}

// ReconcilePeople keeps ProducedPolicy listing exactly the repositories labelled kaos.producer and
// attached to the people's groups that exist (PRD novelcore/kaos#1911 CON-02: a dataset is written
// by people or by one pipeline, never both). It returns a description of each change.
func ReconcilePeople(ctx context.Context, c Catalog, svc AuthService) ([]string, error) {
	var changes []string
	var produced []string
	err := forEachRepository(ctx, c, DatasetPrefix, func(r *catalog.Repository) error {
		md, err := c.GetRepositoryMetadata(ctx, r.Name)
		if err == nil && md[KeyProducer] != "" {
			produced = append(produced, r.Name)
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	slices.Sort(produced)
	want := PeoplePolicy(produced)
	have, err := svc.GetPolicy(ctx, ProducedPolicy)
	switch {
	case errors.Is(err, auth.ErrNotFound):
		if err := svc.WritePolicy(ctx, want, false); err != nil {
			return nil, err
		}
		changes = append(changes, fmt.Sprintf("people may not write the produced datasets %v", produced))
	case err != nil:
		return nil, err
	case !slices.EqualFunc(have.Statement, want.Statement, sameStatement):
		if err := svc.WritePolicy(ctx, want, true); err != nil {
			return nil, err
		}
		changes = append(changes, fmt.Sprintf("people may not write the produced datasets %v", produced))
	}
	for _, group := range peopleGroups {
		attached, _, err := svc.ListGroupPolicies(ctx, group, &model.PaginationParams{Amount: -1})
		if errors.Is(err, auth.ErrNotFound) {
			continue
		}
		if err != nil {
			return changes, fmt.Errorf("list %s policies: %w", group, err)
		}
		if slices.ContainsFunc(attached, func(p *model.Policy) bool { return p.DisplayName == ProducedPolicy }) {
			continue
		}
		if err := svc.AttachPolicyToGroup(ctx, ProducedPolicy, group); errors.Is(err, auth.ErrNotFound) {
			continue
		} else if err != nil {
			return changes, fmt.Errorf("attach %s to %s: %w", ProducedPolicy, group, err)
		}
		changes = append(changes, fmt.Sprintf("attached policy %s to %s", ProducedPolicy, group))
	}
	return changes, nil
}
