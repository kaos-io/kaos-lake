// KAOS (kaos-lake, PRD novelcore/kaos#1217 F-06): new file, Apache-2.0.

// Package declared keeps the multi-user store in line with what the platform declares: one user
// and access key per ML app, delivered as files, and the fixed policies of the groups that people
// join from their project role. People are not declared here: their groups come from their token.
package declared

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/treeverse/lakefs/pkg/auth"
	"github.com/treeverse/lakefs/pkg/auth/model"
	"github.com/treeverse/lakefs/pkg/auth/setup"
	"github.com/treeverse/lakefs/pkg/logging"
	"github.com/treeverse/lakefs/pkg/permissions"
)

const (
	// UserPrefix marks the users this package owns; it never touches any other user.
	UserPrefix = "kaos-app-"
	// AppGroup is the group app users join: project editors (PRD D-04).
	AppGroup = setup.DevelopersGroup
	// RepositoryCreatePolicy lets editors create repositories, which upstream Developers cannot:
	// project members create repositories by hand (PRD novelcore/kaos#1147). Deletion stays with Admins.
	RepositoryCreatePolicy = "KAOSRepositoryCreate"
)

var ErrInvalid = errors.New("invalid declared app user")

// App is one declared app user with its single access key.
type App struct {
	User            string `json:"user"`
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
}

// Load reads one App per file in dir. Names starting with "." are skipped: a mounted Kubernetes
// Secret keeps its data under "..data" with one symlink per key. Any invalid file fails the load,
// so a bad delivery changes nothing.
func Load(dir string) ([]App, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var apps []App
	users := map[string]bool{}
	keys := map[string]bool{}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path) // follows the Secret volume's symlinks
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		var a App
		if err := json.Unmarshal(data, &a); err != nil {
			return nil, fmt.Errorf("%s: %w: %w", path, ErrInvalid, err)
		}
		switch {
		case !strings.HasPrefix(a.User, UserPrefix) || len(a.User) == len(UserPrefix):
			return nil, fmt.Errorf("%s: %w: user %q must start with %q", path, ErrInvalid, a.User, UserPrefix)
		case len(a.AccessKeyID) < 3 || len(a.AccessKeyID) > 20 || a.SecretAccessKey == "":
			return nil, fmt.Errorf("%s: %w: access key", path, ErrInvalid)
		case users[a.User] || keys[a.AccessKeyID]:
			return nil, fmt.Errorf("%s: %w: user %q or its access key is declared twice", path, ErrInvalid, a.User)
		}
		users[a.User], keys[a.AccessKeyID] = true, true
		apps = append(apps, a)
	}
	return apps, nil
}

// Reconcile makes the store match apps and the fixed group policies, and returns a description of
// each change it made. Users without UserPrefix are never changed.
func Reconcile(ctx context.Context, svc auth.Service, apps []App) ([]string, error) {
	var changes []string
	note := func(format string, args ...any) { changes = append(changes, fmt.Sprintf(format, args...)) }

	if err := ensureRepositoryCreate(ctx, svc, note); err != nil {
		return changes, err
	}
	declared := map[string]bool{}
	for _, a := range apps {
		declared[a.User] = true
		if err := ensureApp(ctx, svc, a, note); err != nil {
			return changes, fmt.Errorf("app user %s: %w", a.User, err)
		}
	}
	users, err := appUsers(ctx, svc)
	if err != nil {
		return changes, err
	}
	for _, u := range users {
		if !strings.HasPrefix(u.Username, UserPrefix) || declared[u.Username] {
			continue
		}
		// DeleteUser leaves credentials behind, so remove them first.
		if err := setCredentials(ctx, svc, u.Username, nil, note); err != nil {
			return changes, fmt.Errorf("app user %s: %w", u.Username, err)
		}
		if err := svc.DeleteUser(ctx, u.Username); err != nil {
			return changes, fmt.Errorf("delete app user %s: %w", u.Username, err)
		}
		note("deleted user %s", u.Username)
	}
	return changes, nil
}

func appUsers(ctx context.Context, svc auth.Service) ([]*model.User, error) {
	var all []*model.User
	params := &model.PaginationParams{Prefix: UserPrefix, Amount: -1}
	for {
		users, page, err := svc.ListUsers(ctx, params)
		if err != nil {
			return nil, fmt.Errorf("list app users: %w", err)
		}
		all = append(all, users...)
		if page == nil || page.NextPageToken == "" {
			return all, nil
		}
		params.After = page.NextPageToken
	}
}

func ensureRepositoryCreate(ctx context.Context, svc auth.Service, note func(string, ...any)) error {
	policy := &model.Policy{
		CreatedAt:   time.Now(),
		DisplayName: RepositoryCreatePolicy,
		Statement: model.Statements{{
			Effect:   model.StatementEffectAllow,
			Action:   []string{permissions.CreateRepositoryAction, permissions.AttachStorageNamespaceAction},
			Resource: permissions.All,
		}},
	}
	existing, err := svc.GetPolicy(ctx, RepositoryCreatePolicy)
	switch {
	case errors.Is(err, auth.ErrNotFound):
		if err := svc.WritePolicy(ctx, policy, false); err != nil {
			return fmt.Errorf("create policy %s: %w", RepositoryCreatePolicy, err)
		}
		note("created policy %s", RepositoryCreatePolicy)
	case err != nil:
		return fmt.Errorf("get policy %s: %w", RepositoryCreatePolicy, err)
	case !slices.EqualFunc(existing.Statement, policy.Statement, sameStatement):
		if err := svc.WritePolicy(ctx, policy, true); err != nil {
			return fmt.Errorf("restore policy %s: %w", RepositoryCreatePolicy, err)
		}
		note("restored policy %s", RepositoryCreatePolicy)
	}
	attached, _, err := svc.ListGroupPolicies(ctx, AppGroup, &model.PaginationParams{Amount: -1})
	if err != nil {
		return fmt.Errorf("list %s policies: %w", AppGroup, err)
	}
	if !slices.ContainsFunc(attached, func(p *model.Policy) bool { return p.DisplayName == RepositoryCreatePolicy }) {
		if err := svc.AttachPolicyToGroup(ctx, RepositoryCreatePolicy, AppGroup); err != nil {
			return fmt.Errorf("attach %s to %s: %w", RepositoryCreatePolicy, AppGroup, err)
		}
		note("attached policy %s to %s", RepositoryCreatePolicy, AppGroup)
	}
	return nil
}

func sameStatement(a, b model.Statement) bool {
	return a.Effect == b.Effect && a.Resource == b.Resource && slices.Equal(a.Action, b.Action) && len(a.Condition) == 0 && len(b.Condition) == 0
}

func ensureApp(ctx context.Context, svc auth.Service, a App, note func(string, ...any)) error {
	_, err := svc.GetUser(ctx, a.User)
	if errors.Is(err, auth.ErrNotFound) {
		if _, err := svc.CreateUser(ctx, &model.User{Username: a.User, CreatedAt: time.Now()}); err != nil {
			return fmt.Errorf("create: %w", err)
		}
		note("created user %s", a.User)
	} else if err != nil {
		return fmt.Errorf("get: %w", err)
	}
	groups, _, err := svc.ListUserGroups(ctx, a.User, &model.PaginationParams{Amount: -1})
	if err != nil {
		return fmt.Errorf("list groups: %w", err)
	}
	inGroup := false
	for _, g := range groups {
		if g.DisplayName == AppGroup {
			inGroup = true
			continue
		}
		if err := svc.RemoveUserFromGroup(ctx, a.User, g.DisplayName); err != nil {
			return fmt.Errorf("remove from %s: %w", g.DisplayName, err)
		}
		note("removed user %s from %s", a.User, g.DisplayName)
	}
	if !inGroup {
		if err := svc.AddUserToGroup(ctx, a.User, AppGroup); err != nil {
			return fmt.Errorf("add to %s: %w", AppGroup, err)
		}
		note("added user %s to %s", a.User, AppGroup)
	}
	return setCredentials(ctx, svc, a.User, &a, note)
}

// setCredentials leaves user with exactly want's access key, or with none when want is nil.
func setCredentials(ctx context.Context, svc auth.Service, user string, want *App, note func(string, ...any)) error {
	creds, _, err := svc.ListUserCredentials(ctx, user, &model.PaginationParams{Amount: -1})
	if err != nil {
		return fmt.Errorf("list credentials: %w", err)
	}
	have := false
	for _, c := range creds {
		if want != nil && c.AccessKeyID == want.AccessKeyID {
			// GetCredentialsForUser blanks the secret; GetCredentials returns it (through the auth
			// cache, whose TTL is shorter than the reconcile interval).
			full, err := svc.GetCredentials(ctx, c.AccessKeyID)
			if err != nil {
				return fmt.Errorf("read credentials %s: %w", c.AccessKeyID, err)
			}
			if full.Username == user && full.SecretAccessKey == want.SecretAccessKey {
				have = true
				continue
			}
		}
		if err := svc.DeleteCredentials(ctx, user, c.AccessKeyID); err != nil {
			return fmt.Errorf("delete credentials %s: %w", c.AccessKeyID, err)
		}
		note("deleted access key %s of %s", c.AccessKeyID, user)
	}
	if want != nil && !have {
		if _, err := svc.AddCredentials(ctx, user, want.AccessKeyID, want.SecretAccessKey); err != nil {
			return fmt.Errorf("add credentials %s: %w", want.AccessKeyID, err)
		}
		note("added access key %s to %s", want.AccessKeyID, user)
	}
	return nil
}

// Run loads dir and reconciles now and then every interval until ctx ends. A failed round is
// logged and changes nothing more; the next round tries again.
func Run(ctx context.Context, svc auth.Service, dir string, interval time.Duration, logger logging.Logger) {
	log := logger.WithField("service", "kaos_declared_app_users").WithField("path", dir)
	round := func() {
		apps, err := Load(dir)
		if err != nil {
			log.WithError(err).Error("Declared app users not applied")
			return
		}
		changes, err := Reconcile(ctx, svc, apps)
		for _, c := range changes {
			log.Info(c)
		}
		if err != nil {
			log.WithError(err).Error("Declared app users partly applied")
		}
	}
	round()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			round()
		}
	}
}
