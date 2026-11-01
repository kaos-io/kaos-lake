// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-12): new file, Apache-2.0.

package repos

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/treeverse/lakefs/pkg/auth"
	"github.com/treeverse/lakefs/pkg/auth/model"
	"github.com/treeverse/lakefs/pkg/catalog"
	"github.com/treeverse/lakefs/pkg/permissions"
)

const (
	// ConsumerGroupPrefix names the group of a project that datasets here are shared with:
	// kaos-consumer-<project>. pkg/auth/tokenid puts that project's people and machine user in it.
	ConsumerGroupPrefix = "kaos-consumer-"
	sharePolicyPrefix   = "KAOSShare-"
)

// readActions are all a consuming project may do with a shared dataset (D-09 invariant).
var readActions = []string{"fs:Read*", "fs:List*"}

// AuthService is what ReconcileShares needs of the multi-user store.
type AuthService interface {
	CreateGroup(ctx context.Context, group *model.Group) (*model.Group, error)
	DeleteGroup(ctx context.Context, groupID string) error
	GetGroup(ctx context.Context, groupID string) (*model.Group, error)
	ListGroups(ctx context.Context, params *model.PaginationParams) ([]*model.Group, *model.Paginator, error)
	WritePolicy(ctx context.Context, policy *model.Policy, update bool) error
	GetPolicy(ctx context.Context, policyDisplayName string) (*model.Policy, error)
	DeletePolicy(ctx context.Context, policyDisplayName string) error
	AttachPolicyToGroup(ctx context.Context, policyDisplayName, groupID string) error
	ListGroupPolicies(ctx context.Context, groupID string, params *model.PaginationParams) ([]*model.Policy, *model.Paginator, error)
}

// SharePolicy is the read-only policy of a consuming project over the datasets shared with it.
func SharePolicy(project string, datasets []string) *model.Policy {
	var st model.Statements
	for _, d := range datasets {
		for _, resource := range []string{permissions.RepoArn(d), permissions.RepoArn(d) + "/*"} {
			st = append(st, model.Statement{Effect: model.StatementEffectAllow, Action: readActions, Resource: resource})
		}
	}
	return &model.Policy{CreatedAt: time.Now(), DisplayName: sharePolicyPrefix + project, Statement: st}
}

// ReconcileShares gives each project named in a dataset's kaos.shared_with a group and a read-only
// policy over exactly the datasets shared with it, and removes them for projects no dataset is
// shared with any more. It returns a description of each change.
func ReconcileShares(ctx context.Context, c Catalog, svc AuthService) ([]string, error) {
	var changes []string
	note := func(format string, args ...any) { changes = append(changes, fmt.Sprintf(format, args...)) }
	shared := map[string][]string{}
	err := forEachRepository(ctx, c, DatasetPrefix, func(r *catalog.Repository) error {
		md, err := c.GetRepositoryMetadata(ctx, r.Name)
		if err != nil {
			return err
		}
		for _, p := range SharedWith(md[KeySharedWith]) {
			if projectName.MatchString(p) {
				shared[p] = append(shared[p], r.Name)
			}
		}
		return nil
	})
	if err != nil {
		return changes, err
	}
	for _, p := range slices.Sorted(maps.Keys(shared)) {
		if err := ensureShare(ctx, svc, p, shared[p], note); err != nil {
			return changes, fmt.Errorf("share with %s: %w", p, err)
		}
	}
	groups, _, err := svc.ListGroups(ctx, &model.PaginationParams{Prefix: ConsumerGroupPrefix, Amount: -1})
	if err != nil {
		return changes, err
	}
	for _, g := range groups {
		p := strings.TrimPrefix(g.DisplayName, ConsumerGroupPrefix)
		if _, ok := shared[p]; ok || !strings.HasPrefix(g.DisplayName, ConsumerGroupPrefix) {
			continue
		}
		if err := svc.DeletePolicy(ctx, sharePolicyPrefix+p); err != nil && !errors.Is(err, auth.ErrNotFound) {
			return changes, err
		}
		if err := svc.DeleteGroup(ctx, g.DisplayName); err != nil && !errors.Is(err, auth.ErrNotFound) {
			return changes, err
		}
		note("unshared everything with %s", p)
	}
	return changes, nil
}

func ensureShare(ctx context.Context, svc AuthService, project string, datasets []string, note func(string, ...any)) error {
	group := ConsumerGroupPrefix + project
	if _, err := svc.GetGroup(ctx, group); errors.Is(err, auth.ErrNotFound) {
		if _, err := svc.CreateGroup(ctx, &model.Group{DisplayName: group, CreatedAt: time.Now()}); err != nil {
			return err
		}
		note("created group %s", group)
	} else if err != nil {
		return err
	}
	slices.Sort(datasets)
	want := SharePolicy(project, datasets)
	have, err := svc.GetPolicy(ctx, want.DisplayName)
	switch {
	case errors.Is(err, auth.ErrNotFound):
		if err := svc.WritePolicy(ctx, want, false); err != nil {
			return err
		}
		note("shared %v with %s", datasets, project)
	case err != nil:
		return err
	case !slices.EqualFunc(have.Statement, want.Statement, sameStatement):
		if err := svc.WritePolicy(ctx, want, true); err != nil {
			return err
		}
		note("shared %v with %s", datasets, project)
	}
	attached, _, err := svc.ListGroupPolicies(ctx, group, &model.PaginationParams{Amount: -1})
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(attached, func(p *model.Policy) bool { return p.DisplayName == want.DisplayName }) {
		return svc.AttachPolicyToGroup(ctx, want.DisplayName, group)
	}
	return nil
}

func sameStatement(a, b model.Statement) bool {
	return a.Effect == b.Effect && a.Resource == b.Resource && slices.Equal(a.Action, b.Action)
}
