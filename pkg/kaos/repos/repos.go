// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-01/F-02): new file, Apache-2.0.

// Package repos gives repositories a purpose people can see (PRD novelcore/kaos#1283): a dataset
// (`dataset-*`), an ML app's outputs (`outputs-<app>`), or other. The purpose is repository
// metadata under the `kaos.` prefix. The platform's own repositories (the project's default
// dataset, each app's outputs) are declared and kept in place by Reconcile; a repository is never
// deleted here.
package repos

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/treeverse/lakefs/pkg/catalog"
	"github.com/treeverse/lakefs/pkg/graveler"
	"github.com/treeverse/lakefs/pkg/logging"
)

const (
	DatasetPrefix = "dataset-"
	OutputsPrefix = "outputs-"

	KeyKind        = "kaos.kind"
	KeyApp         = "kaos.app"
	KeyDefault     = "kaos.default"
	KeyTemplate    = "kaos.template"
	KeyDescription = "kaos.description"
	KeyOrphaned    = "kaos.orphaned"
	// KeyLocation is where the repository's storage lives (D-10); set by the platform only.
	KeyLocation = "kaos.location"
	// KeySharedWith lists the projects that may read a dataset, comma-separated (D-09). Only the
	// owning project's admins set it: the API asks for the delete-repository right.
	KeySharedWith = "kaos.shared_with"

	KindDataset = "dataset"
	KindOutputs = "outputs"

	defaultBranch = "main"
)

var ErrInvalid = errors.New("invalid repository label")

// platformKeys are written by kaos-lake only: which repositories the platform declared.
var platformKeys = []string{KeyApp, KeyDefault, KeyOrphaned, KeyLocation}

// projectName is one KubeProject name in kaos.shared_with.
var projectName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// ValidProject reports whether name is a valid KubeProject name.
func ValidProject(name string) bool {
	return projectName.MatchString(name)
}

// SharedWith parses kaos.shared_with into project names; empty items are dropped.
func SharedWith(value string) []string {
	var out []string
	for _, p := range strings.Split(value, ",") {
		if p = strings.TrimSpace(p); p != "" && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

// nameRest is what follows a purpose prefix (CON-02): 3 to 48 lowercase letters, digits, hyphens.
var nameRest = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{2,47}$`)

// ValidName reports whether name is prefix followed by a valid rest.
func ValidName(name, prefix string) bool {
	rest, ok := strings.CutPrefix(name, prefix)
	return ok && nameRest.MatchString(rest)
}

// ValidateMetadata checks the labels a caller writes on repository name. People may label a
// `dataset-*` repository a dataset and describe it; the outputs kind and the platform keys are
// kaos-lake's.
func ValidateMetadata(name string, md map[string]string) error {
	for k := range md {
		if slices.Contains(platformKeys, k) {
			return fmt.Errorf("%w: %s is set by the platform only", ErrInvalid, k)
		}
	}
	if v, ok := md[KeySharedWith]; ok {
		if !ValidName(name, DatasetPrefix) {
			return fmt.Errorf("%w: only datasets (%s…) are shared", ErrInvalid, DatasetPrefix)
		}
		for _, p := range SharedWith(v) {
			if !projectName.MatchString(p) {
				return fmt.Errorf("%w: %s: %q is not a project name", ErrInvalid, KeySharedWith, p)
			}
		}
	}
	kind, ok := md[KeyKind]
	switch {
	case !ok:
		return nil
	case kind == KindDataset && !ValidName(name, DatasetPrefix):
		return fmt.Errorf("%w: a dataset's name is %s followed by 3-48 lowercase letters, digits or hyphens; %q is not",
			ErrInvalid, DatasetPrefix, name)
	case kind == KindDataset:
		return nil
	case kind == KindOutputs:
		return fmt.Errorf("%w: outputs repositories are created by the platform, one per ML app", ErrInvalid)
	default:
		return fmt.Errorf("%w: %s is %q; want %q", ErrInvalid, KeyKind, kind, KindDataset)
	}
}

// ValidateDelete checks the label keys a caller removes from a repository.
func ValidateDelete(keys []string) error {
	for _, k := range keys {
		if k == KeyKind || slices.Contains(platformKeys, k) {
			return fmt.Errorf("%w: %s is kept by the platform", ErrInvalid, k)
		}
	}
	return nil
}

// Declared is one repository the platform keeps in place.
type Declared struct {
	Name    string
	Kind    string
	App     string // the outputs' app; empty for a dataset
	Default bool   // the project's default dataset
}

// Labels are the declared repository's `kaos.` metadata.
func (d Declared) Labels() map[string]string {
	md := map[string]string{KeyKind: d.Kind}
	if d.App != "" {
		md[KeyApp] = d.App
	}
	if d.Default {
		md[KeyDefault] = "true"
	}
	return md
}

// Desired is the project's default dataset, when named, and one outputs repository per app.
func Desired(defaultDataset string, outputs map[string]string) []Declared {
	var want []Declared
	if defaultDataset != "" {
		want = append(want, Declared{Name: defaultDataset, Kind: KindDataset, Default: true})
	}
	apps := make([]string, 0, len(outputs))
	for app := range outputs {
		apps = append(apps, app)
	}
	slices.Sort(apps)
	for _, app := range apps {
		want = append(want, Declared{Name: outputs[app], Kind: KindOutputs, App: app})
	}
	return want
}

// Catalog is what Reconcile needs of the lakeFS catalog.
type Catalog interface {
	GetRepository(ctx context.Context, repository string) (*catalog.Repository, error)
	CreateRepository(ctx context.Context, repository, storageID, storageNamespace, branch string, readOnly bool) (*catalog.Repository, error)
	GetRepositoryMetadata(ctx context.Context, repository string) (graveler.RepositoryMetadata, error)
	UpdateRepositoryMetadata(ctx context.Context, repository string, metadata graveler.RepositoryMetadata) error
	DeleteRepositoryMetadata(ctx context.Context, repository string, keys []string) error
	ListRepositories(ctx context.Context, limit int, prefix, searchString, after string, opts ...catalog.ListRepositoriesOptionsFunc) ([]*catalog.Repository, bool, error)
	GetCommit(ctx context.Context, repository, reference string) (*catalog.CommitLog, error)
}

// Reconcile creates each declared repository that is missing, under storagePrefix, and puts its
// labels back. An outputs repository no app declares any longer is labelled orphaned and kept: it
// holds results. It returns a description of each change.
func Reconcile(ctx context.Context, c Catalog, storageID, storagePrefix, location string, want []Declared) ([]string, error) {
	var changes []string
	note := func(format string, args ...any) { changes = append(changes, fmt.Sprintf(format, args...)) }
	declared := map[string]bool{}
	for _, d := range want {
		declared[d.Name] = true
		if err := ensure(ctx, c, storageID, storagePrefix, d, note); err != nil {
			return changes, fmt.Errorf("repository %s: %w", d.Name, err)
		}
	}
	if location != "" {
		if err := labelLocation(ctx, c, storagePrefix, location, note); err != nil {
			return changes, err
		}
	}
	// A dataset-* repository someone made by hand, without a template (lakeFS's own Create
	// repository, the API, the kubecore package), is a dataset too: labelled, it shows on the
	// Datasets page and in the pipeline dropdown and gets main's protection. Like the New dataset
	// flow, which labels after its first commit, it is labelled only once its default branch has
	// a commit of its own, so the first upload straight to main is not refused.
	if err := forEachRepository(ctx, c, DatasetPrefix, func(r *catalog.Repository) error {
		if !ValidName(r.Name, DatasetPrefix) {
			return nil
		}
		md, err := c.GetRepositoryMetadata(ctx, r.Name)
		if err != nil || md[KeyKind] != "" {
			return err
		}
		if head, err := c.GetCommit(ctx, r.Name, r.DefaultBranch); err != nil || len(head.Parents) == 0 {
			return nil // empty (only lakeFS's initial commit) or unreadable: next pass
		}
		if err := c.UpdateRepositoryMetadata(ctx, r.Name, graveler.RepositoryMetadata{KeyKind: KindDataset}); err != nil {
			return err
		}
		note("labelled %s a dataset: made without a template", r.Name)
		return nil
	}); err != nil {
		return changes, err
	}
	err := forEachRepository(ctx, c, OutputsPrefix, func(r *catalog.Repository) error {
		name := r.Name
		if declared[name] {
			return nil
		}
		md, err := c.GetRepositoryMetadata(ctx, name)
		if err != nil || md[KeyKind] != KindOutputs || md[KeyOrphaned] == "true" {
			return err
		}
		if err := c.UpdateRepositoryMetadata(ctx, name, graveler.RepositoryMetadata{KeyOrphaned: "true"}); err != nil {
			return err
		}
		note("labelled %s orphaned: no app declares it", name)
		return nil
	})
	return changes, err
}

func ensure(ctx context.Context, c Catalog, storageID, storagePrefix string, d Declared, note func(string, ...any)) error {
	_, err := c.GetRepository(ctx, d.Name)
	if errors.Is(err, graveler.ErrNotFound) {
		ns := strings.TrimSuffix(storagePrefix, "/") + "/" + d.Name
		if _, err := c.CreateRepository(ctx, d.Name, storageID, ns, defaultBranch, false); err != nil {
			return fmt.Errorf("create in %s: %w", ns, err)
		}
		note("created %s in %s", d.Name, ns)
	} else if err != nil {
		return err
	}
	md, err := c.GetRepositoryMetadata(ctx, d.Name)
	if err != nil {
		return err
	}
	update := graveler.RepositoryMetadata{}
	for k, v := range d.Labels() {
		if md[k] != v {
			update[k] = v
		}
	}
	if len(update) > 0 {
		if err := c.UpdateRepositoryMetadata(ctx, d.Name, update); err != nil {
			return err
		}
		note("labelled %s %v", d.Name, update)
	}
	if md[KeyOrphaned] != "" {
		if err := c.DeleteRepositoryMetadata(ctx, d.Name, []string{KeyOrphaned}); err != nil {
			return err
		}
		note("%s is declared again", d.Name)
	}
	return nil
}

// labelLocation labels every repository whose storage is under storagePrefix with location.
func labelLocation(ctx context.Context, c Catalog, storagePrefix, location string, note func(string, ...any)) error {
	under := strings.TrimSuffix(storagePrefix, "/") + "/"
	return forEachRepository(ctx, c, "", func(r *catalog.Repository) error {
		if !strings.HasPrefix(r.StorageNamespace, under) {
			return nil
		}
		md, err := c.GetRepositoryMetadata(ctx, r.Name)
		if err != nil || md[KeyLocation] == location {
			return err
		}
		if err := c.UpdateRepositoryMetadata(ctx, r.Name, graveler.RepositoryMetadata{KeyLocation: location}); err != nil {
			return err
		}
		note("labelled %s at %s", r.Name, location)
		return nil
	})
}

func forEachRepository(ctx context.Context, c Catalog, prefix string, fn func(r *catalog.Repository) error) error {
	after := ""
	for {
		page, more, err := c.ListRepositories(ctx, 100, prefix, "", after)
		if err != nil {
			return err
		}
		for _, r := range page {
			if err := fn(r); err != nil {
				return fmt.Errorf("repository %s: %w", r.Name, err)
			}
			after = r.Name
		}
		if !more || len(page) == 0 {
			return nil
		}
	}
}

// Run reconciles the declared repositories now and every interval until ctx ends. outputs returns
// each declared app's outputs repository, by app name.
// svc, when not nil, also gets the read-only grants of shared datasets (ReconcileShares).
func Run(ctx context.Context, c Catalog, svc AuthService, storageID, storagePrefix, location, defaultDataset string,
	outputs func() map[string]string, interval time.Duration, log logging.Logger) {
	log = log.WithField("service", "kaos_repositories")
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	shares := func() {
		if svc == nil {
			return
		}
		changes, err := ReconcileShares(ctx, c, svc)
		for _, ch := range changes {
			log.Info(ch)
		}
		if err != nil {
			log.WithError(err).Error("Dataset shares not applied")
		}
	}
	for {
		changes, err := Reconcile(ctx, c, storageID, storagePrefix, location, Desired(defaultDataset, outputs()))
		for _, ch := range changes {
			log.Info(ch)
		}
		if err != nil {
			log.WithError(err).Error("Declared repositories not applied")
		}
		// A dataset's main changes only through a merge. Done after labelling, so the label that
		// the New dataset flow sets last (after its first commit to main) is what starts it.
		if p, ok := c.(ProtectionCatalog); ok {
			changes, err := ReconcileProtection(ctx, c, p)
			for _, ch := range changes {
				log.Info(ch)
			}
			if err != nil {
				log.WithError(err).Error("Dataset branch protection not applied")
			}
		}
		shares()
	wait:
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				break wait
			case <-kicks:
				// A dataset was just shared or unshared (Kick): apply it now, not at the next tick.
				shares()
			}
		}
	}
}
