// KAOS (kaos-lake, PRD novelcore/kaos#1217 F-06): new file, Apache-2.0.

// Package declared keeps the multi-user store in line with what the platform declares: one user,
// access key and policy per ML app, delivered as files, and the fixed policies of the groups that
// people join from their project role. People are not declared here: their groups come from their token.
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
	"sync"
	"time"

	"github.com/treeverse/lakefs/pkg/auth"
	"github.com/treeverse/lakefs/pkg/auth/model"
	"github.com/treeverse/lakefs/pkg/auth/setup"
	"github.com/treeverse/lakefs/pkg/kaos/repos"
	"github.com/treeverse/lakefs/pkg/logging"
	"github.com/treeverse/lakefs/pkg/permissions"
)

const (
	// UserPrefix marks the users this package owns; it never touches any other user.
	UserPrefix = "kaos-app-"
	// EditorsGroup is the group of project editors (people). App users used to join it; since
	// PRD novelcore/kaos#1283 D-04 they have their own policy and no group.
	EditorsGroup = setup.DevelopersGroup
	// RepositoryCreatePolicy lets editors create repositories, which upstream Developers cannot:
	// project members create repositories by hand (PRD novelcore/kaos#1147). It also lets them
	// label the datasets they create (dataset-*, PRD novelcore/kaos#1283). Deletion stays with Admins.
	RepositoryCreatePolicy = "KAOSRepositoryCreate"
)

var ErrInvalid = errors.New("invalid declared app user")

// App is one declared app user with its single access key.
type App struct {
	User            string `json:"user"`
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
	// Namespace is where the app's workflow pods run: their service-account tokens act as User
	// from there only (pkg/auth/tokenid, F-05). Empty: the app has no workload identity.
	Namespace string `json:"namespace,omitempty"`
	// OutputsRepository is the app's own outputs repository, kept in place by pkg/kaos/repos
	// (PRD novelcore/kaos#1283 F-01). Empty: the app has none.
	OutputsRepository string `json:"outputs_repository,omitempty"`
	// Datasets are the datasets the app's pipeline produces (PRD novelcore/kaos#1911 D-04): the
	// repositories it alone may write, created by pkg/kaos/repos. A dataset has one producer.
	Datasets []string `json:"datasets,omitempty"`
}

// Loaded holds the apps of the last successful load, for workload identity.
type Loaded struct {
	mu   sync.RWMutex
	apps map[string]App
}

// Get returns the loaded app with user name user.
func (l *Loaded) Get(user string) (App, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	a, ok := l.apps[user]
	return a, ok
}

// Outputs returns each loaded app's outputs repository, by app name (the user without UserPrefix).
func (l *Loaded) Outputs() map[string]string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := map[string]string{}
	for user, a := range l.apps {
		if a.OutputsRepository != "" {
			out[strings.TrimPrefix(user, UserPrefix)] = a.OutputsRepository
		}
	}
	return out
}

// Datasets returns each declared dataset's producer, by dataset: the app name (the user without
// UserPrefix).
func (l *Loaded) Datasets() map[string]string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := map[string]string{}
	for user, a := range l.apps {
		for _, d := range a.Datasets {
			out[d] = strings.TrimPrefix(user, UserPrefix)
		}
	}
	return out
}

// Set replaces the loaded apps.
func (l *Loaded) Set(apps []App) {
	byUser := make(map[string]App, len(apps))
	for _, a := range apps {
		byUser[a.User] = a
	}
	l.mu.Lock()
	l.apps = byUser
	l.mu.Unlock()
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
	producers := map[string]string{}
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
		case a.OutputsRepository != "" && !repos.ValidName(a.OutputsRepository, repos.OutputsPrefix):
			return nil, fmt.Errorf("%s: %w: outputs repository %q must be %s followed by 3-48 lowercase letters, digits or hyphens",
				path, ErrInvalid, a.OutputsRepository, repos.OutputsPrefix)
		case users[a.User] || keys[a.AccessKeyID]:
			return nil, fmt.Errorf("%s: %w: user %q or its access key is declared twice", path, ErrInvalid, a.User)
		}
		for _, d := range a.Datasets {
			if !repos.ValidName(d, repos.DatasetPrefix) {
				return nil, fmt.Errorf("%s: %w: dataset %q must be %s followed by 3-48 lowercase letters, digits or hyphens",
					path, ErrInvalid, d, repos.DatasetPrefix)
			}
			if by, ok := producers[d]; ok {
				return nil, fmt.Errorf("%s: %w: dataset %q is already declared by %s; a dataset has one producer", path, ErrInvalid, d, by)
			}
			producers[d] = a.User
		}
		users[a.User], keys[a.AccessKeyID] = true, true
		apps = append(apps, a)
	}
	return apps, nil
}

// Reconcile makes the store match apps and the fixed group policies, and returns a description of
// each change it made. Users without UserPrefix are never changed. A declared app user also keeps
// its exchanged keys younger than exchangeTTL (F-05).
func Reconcile(ctx context.Context, svc auth.Service, apps []App, exchangeTTL time.Duration, opts ...Option) ([]string, error) {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	var changes []string
	note := func(format string, args ...any) { changes = append(changes, fmt.Sprintf(format, args...)) }

	if err := ensureRepositoryCreate(ctx, svc, note); err != nil {
		return changes, err
	}
	declared := map[string]bool{}
	for _, a := range apps {
		declared[a.User] = true
		if err := ensureApp(ctx, svc, a, exchangeTTL, o, note); err != nil {
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
		if err := setCredentials(ctx, svc, u.Username, nil, 0, note); err != nil {
			return changes, fmt.Errorf("app user %s: %w", u.Username, err)
		}
		if err := svc.DeleteUser(ctx, u.Username); err != nil {
			return changes, fmt.Errorf("delete app user %s: %w", u.Username, err)
		}
		note("deleted user %s", u.Username)
	}
	if err := removeStaleAppPolicies(ctx, svc, declared, note); err != nil {
		return changes, err
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
		}, {
			Effect:   model.StatementEffectAllow,
			Action:   []string{permissions.UpdateRepositoryAction},
			Resource: permissions.RepoArn(repos.DatasetPrefix + "*"),
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
	attached, _, err := svc.ListGroupPolicies(ctx, EditorsGroup, &model.PaginationParams{Amount: -1})
	if err != nil {
		return fmt.Errorf("list %s policies: %w", EditorsGroup, err)
	}
	if !slices.ContainsFunc(attached, func(p *model.Policy) bool { return p.DisplayName == RepositoryCreatePolicy }) {
		if err := svc.AttachPolicyToGroup(ctx, RepositoryCreatePolicy, EditorsGroup); err != nil {
			return fmt.Errorf("attach %s to %s: %w", RepositoryCreatePolicy, EditorsGroup, err)
		}
		note("attached policy %s to %s", RepositoryCreatePolicy, EditorsGroup)
	}
	return nil
}

func sameStatement(a, b model.Statement) bool {
	return a.Effect == b.Effect && a.Resource == b.Resource && slices.Equal(a.Action, b.Action) && len(a.Condition) == 0 && len(b.Condition) == 0
}

func ensureApp(ctx context.Context, svc auth.Service, a App, exchangeTTL time.Duration, o options, note func(string, ...any)) error {
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
	// Apps are in no group: their rights are exactly their own policy (PRD #1283 D-04).
	for _, g := range groups {
		if err := svc.RemoveUserFromGroup(ctx, a.User, g.DisplayName); err != nil {
			return fmt.Errorf("remove from %s: %w", g.DisplayName, err)
		}
		note("removed user %s from %s", a.User, g.DisplayName)
	}
	if err := ensureAppPolicy(ctx, svc, a, o.legacyRepository, o.writable(ctx, a), note); err != nil {
		return err
	}
	return setCredentials(ctx, svc, a.User, &a, exchangeTTL, note)
}

// setCredentials leaves user with exactly want's access key, or none when want is nil, and its
// exchanged keys younger than exchangeTTL.
func setCredentials(ctx context.Context, svc auth.Service, user string, want *App, exchangeTTL time.Duration, note func(string, ...any)) error {
	creds, _, err := svc.ListUserCredentials(ctx, user, &model.PaginationParams{Amount: -1})
	if err != nil {
		return fmt.Errorf("list credentials: %w", err)
	}
	have := false
	now := time.Now()
	for _, c := range creds {
		if strings.HasPrefix(c.AccessKeyID, ExchangeKeyPrefix) && !Expired(c, exchangeTTL, now) {
			continue
		}
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

// Option tunes Reconcile and Run.
type Option func(*options)

type options struct {
	legacyRepository string
	produced         func(ctx context.Context, dataset, app string) bool
	wake             <-chan struct{}
	afterRound       func(ctx context.Context) (waiting bool)
	retryEvery       time.Duration
	retryFor         time.Duration
}

// How soon, and for how long after a wake-up, Run looks again while something it waits on is not
// there yet: a dataset's main still being protected, or a grant not yet old enough to rely on.
const (
	defaultRetryEvery = 2 * time.Second
	defaultRetryFor   = 3 * time.Minute
)

// writable returns the datasets a declares that it may write now: those produced reports ready.
// Without WithProducedDatasets, none.
func (o options) writable(ctx context.Context, a App) []string {
	var out []string
	for _, d := range a.Datasets {
		if o.produced != nil && o.produced(ctx, d, strings.TrimPrefix(a.User, UserPrefix)) {
			out = append(out, d)
		}
	}
	return out
}

// WithProducedDatasets tells Reconcile which declared datasets are ready to be written by their
// app (PRD novelcore/kaos#1911 CON-02, CON-03): ready reports whether dataset exists as app's own
// with its main protected (repos.ProducedBy). An app gets its write rights on a declared dataset
// only while ready says so, and loses them on the next pass after it stops; until then, and
// without this option, it only reads it. So the app never holds the commit right on a main that
// is someone else's or unprotected, whichever of the two reconciles runs first.
func WithProducedDatasets(ready func(ctx context.Context, dataset, app string) bool) Option {
	return func(o *options) { o.produced = ready }
}

// WithWake makes Run reconcile whenever wake signals, not only at its interval, and then look again
// every few seconds for a few minutes while a produced dataset is not ready for its app: the
// repositories reconcile (repos.Changed) wakes it as soon as it has made a dataset, so the app is
// granted the right to write it right then, not up to an interval later (PRD novelcore/kaos#1911
// CON-02). Only Run reconciles: a wake-up never starts a second, concurrent pass.
func WithWake(wake <-chan struct{}) Option { return func(o *options) { o.wake = wake } }

// WithAfterRound runs fn after each pass of Run, once the apps' policies are applied. waiting
// says something is not settled yet (see WithWake). Run's own goroutine calls it, so it never
// overlaps a pass.
func WithAfterRound(fn func(ctx context.Context) (waiting bool)) Option {
	return func(o *options) { o.afterRound = fn }
}

// WithRetry sets how often and for how long after a wake-up Run looks again while waiting.
func WithRetry(every, window time.Duration) Option {
	return func(o *options) { o.retryEvery, o.retryFor = every, window }
}

// WithLegacyRepository lets every app read and write the named repository too: the project
// repository that apps created before PRD novelcore/kaos#1283 still write their outputs to.
// Empty: no such access.
func WithLegacyRepository(name string) Option { return func(o *options) { o.legacyRepository = name } }

// Run loads dir and reconciles now and then every interval until ctx ends, keeping each load in
// loaded. A failed round is logged and changes nothing more; the next round tries again.
func Run(ctx context.Context, svc auth.Service, dir string, interval, exchangeTTL time.Duration, loaded *Loaded, logger logging.Logger, opts ...Option) {
	o := options{retryEvery: defaultRetryEvery, retryFor: defaultRetryFor}
	for _, opt := range opts {
		opt(&o)
	}
	log := logger.WithField("service", "kaos_declared_app_users").WithField("path", dir)
	// round reports whether something is not settled: a declared dataset its app may not write
	// yet, or what afterRound waits on.
	round := func() (waiting bool) {
		apps, err := Load(dir)
		if err != nil {
			log.WithError(err).Error("Declared app users not applied")
			return false
		}
		loaded.Set(apps)
		changes, err := Reconcile(ctx, svc, apps, exchangeTTL, opts...)
		for _, c := range changes {
			log.Info(c)
		}
		if err != nil {
			log.WithError(err).Error("Declared app users partly applied")
		}
		if o.afterRound != nil {
			return o.afterRound(ctx)
		}
		return false
	}
	round()
	t := time.NewTicker(interval)
	defer t.Stop()
	var retry <-chan time.Time
	var until time.Time
	look := func(waiting bool) {
		retry = nil
		if waiting && time.Now().Before(until) {
			retry = time.After(o.retryEvery)
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			look(round())
		case <-o.wake:
			until = time.Now().Add(o.retryFor)
			look(round())
		case <-retry:
			look(round())
		}
	}
}

// GrantedSince reports whether the policy of app (the name of its user without UserPrefix) holds
// the allowances for producing dataset, and since when: the policy's own stored time, which is
// when it last changed. ReconcileGrants marks the dataset from it.
func GrantedSince(ctx context.Context, svc auth.Service, dataset, app string) (time.Time, bool, error) {
	a := App{User: UserPrefix + app}
	have, err := svc.GetPolicy(ctx, AppPolicyName(a.User))
	if errors.Is(err, auth.ErrNotFound) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	base := AppPolicy(a, "", nil).Statement
	for _, st := range AppPolicy(a, "", []string{dataset}).Statement {
		if slices.ContainsFunc(base, func(b model.Statement) bool { return sameStatement(st, b) }) {
			continue
		}
		if !slices.ContainsFunc(have.Statement, func(h model.Statement) bool { return sameStatement(st, h) }) {
			return time.Time{}, false, nil
		}
	}
	return have.CreatedAt, true, nil
}
