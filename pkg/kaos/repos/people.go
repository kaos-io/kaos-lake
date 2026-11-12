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
	"github.com/treeverse/lakefs/pkg/graveler"
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

// PeopleState is what ReconcilePeople left in the store.
type PeopleState struct {
	// Listed are the datasets ProducedPolicy names.
	Listed []string
	// Since is when ProducedPolicy last changed: the policy's own timestamp, stored with it, so
	// it survives a restart and every replica reads the same.
	Since time.Time
}

// Closed reports whether nobody's session can still hold a version of the policy that does not
// name dataset: it is listed, and the policy has not changed for longer than settle, the longest
// a session keeps the policies it last read.
func (s PeopleState) Closed(dataset string, settle time.Duration, now time.Time) bool {
	return slices.Contains(s.Listed, dataset) && !s.Since.IsZero() && now.Sub(s.Since) > settle
}

// ReconcilePeople keeps ProducedPolicy listing exactly the repositories labelled kaos.producer,
// and the declared datasets (declared: names apps declare) that do not exist yet, and attached to
// the people's groups that exist (PRD novelcore/kaos#1911 CON-02: a dataset is written by people
// or by one pipeline, never both). The policy names a dataset before its repository exists:
// sessions keep the policies they read for a while, so Reconcile creates the repository only once
// the listing is older than that (PeopleState.Closed). A declared name whose repository exists
// without the label is someone else's and is not listed. It returns the state it left and a
// description of each change.
func ReconcilePeople(ctx context.Context, c Catalog, svc AuthService, declared []string) (PeopleState, []string, error) {
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
		return PeopleState{}, nil, err
	}
	for _, d := range declared {
		if slices.Contains(produced, d) {
			continue
		}
		if _, err := c.GetRepository(ctx, d); errors.Is(err, graveler.ErrNotFound) {
			produced = append(produced, d)
		} else if err != nil {
			return PeopleState{}, nil, fmt.Errorf("repository %s: %w", d, err)
		}
	}
	slices.Sort(produced)
	want := PeoplePolicy(produced)
	state := PeopleState{Listed: produced, Since: want.CreatedAt}
	have, err := svc.GetPolicy(ctx, ProducedPolicy)
	switch {
	case errors.Is(err, auth.ErrNotFound):
		if err := svc.WritePolicy(ctx, want, false); err != nil {
			return PeopleState{}, nil, err
		}
		changes = append(changes, fmt.Sprintf("people may not write the produced datasets %v", produced))
	case err != nil:
		return PeopleState{}, nil, err
	case !slices.EqualFunc(have.Statement, want.Statement, sameStatement):
		if err := svc.WritePolicy(ctx, want, true); err != nil {
			return PeopleState{}, nil, err
		}
		changes = append(changes, fmt.Sprintf("people may not write the produced datasets %v", produced))
	default:
		state.Since = have.CreatedAt
	}
	attached := false
	for _, group := range peopleGroups {
		policies, _, err := svc.ListGroupPolicies(ctx, group, &model.PaginationParams{Amount: -1})
		if errors.Is(err, auth.ErrNotFound) {
			continue
		}
		if err != nil {
			return PeopleState{}, changes, fmt.Errorf("list %s policies: %w", group, err)
		}
		if slices.ContainsFunc(policies, func(p *model.Policy) bool { return p.DisplayName == ProducedPolicy }) {
			continue
		}
		if err := svc.AttachPolicyToGroup(ctx, ProducedPolicy, group); errors.Is(err, auth.ErrNotFound) {
			continue
		} else if err != nil {
			return PeopleState{}, changes, fmt.Errorf("attach %s to %s: %w", ProducedPolicy, group, err)
		}
		attached = true
		changes = append(changes, fmt.Sprintf("attached policy %s to %s", ProducedPolicy, group))
	}
	// A group that just got the policy has sessions that never read it: the clock starts again.
	if attached && state.Since != want.CreatedAt {
		if err := svc.WritePolicy(ctx, want, true); err != nil {
			return PeopleState{}, changes, err
		}
		state.Since = want.CreatedAt
	}
	return state, changes, nil
}
