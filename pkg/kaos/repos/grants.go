// KAOS (kaos-lake, PRD novelcore/kaos#1911 CON-02, CON-03): new file, Apache-2.0.

package repos

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/treeverse/lakefs/pkg/catalog"
	"github.com/treeverse/lakefs/pkg/graveler"
)

// GrantValue is the value of KeyGrant: app, and since when its policy has allowed it to write the
// dataset.
func GrantValue(app string, since time.Time) string {
	return app + "@" + since.UTC().Format(time.RFC3339)
}

// Granted reports whether md says the app that produces the dataset may write it.
func Granted(md graveler.RepositoryMetadata) bool {
	app, _, ok := strings.Cut(md[KeyGrant], "@")
	return ok && app != "" && app == md[KeyProducer]
}

// ReconcileGrants keeps KeyGrant on each produced dataset (labelled kaos.producer) saying whether
// the producing app may write it now, which is what the rest of the platform waits for before it
// starts a pipeline that writes the dataset. since reports whether the app's policy allows it to
// write the dataset, and since when (the policy's own stored time, so every replica and a
// restarted lake agree). The key is set only once the allowance is older than settle, the longest
// a session keeps the policies it read: before that the app's session may still hold the policy
// without it. It is removed as soon as the allowance is not there (the dataset left the app's
// declaration, the app was renamed, protection was taken off main). waiting reports that some
// produced dataset is not marked yet, so the caller looks again soon. It returns a description of
// each change.
func ReconcileGrants(ctx context.Context, c Catalog,
	since func(ctx context.Context, dataset, app string) (time.Time, bool, error), settle time.Duration, now time.Time) (changes []string, waiting bool, err error) {
	err = forEachRepository(ctx, c, DatasetPrefix, func(r *catalog.Repository) error {
		md, err := c.GetRepositoryMetadata(ctx, r.Name)
		if err != nil || md[KeyProducer] == "" {
			return err
		}
		app := md[KeyProducer]
		drop := func(why string) error {
			if _, has := md[KeyGrant]; !has {
				return nil
			}
			if err := c.DeleteRepositoryMetadata(ctx, r.Name, []string{KeyGrant}); err != nil {
				return err
			}
			changes = append(changes, fmt.Sprintf("%s: %s", r.Name, why))
			return nil
		}
		at, ok, err := since(ctx, r.Name, app)
		if err != nil {
			return err
		}
		switch {
		case !ok:
			waiting = true
			return drop(app + " may not write it")
		case now.Sub(at) <= settle:
			// Not old enough to be sure: what is marked for this app stays, anything else goes.
			waiting = waiting || !Granted(md)
			if !Granted(md) {
				return drop("the grant is not " + app + "'s")
			}
			return nil
		}
		if want := GrantValue(app, at); md[KeyGrant] != want {
			if err := c.UpdateRepositoryMetadata(ctx, r.Name, graveler.RepositoryMetadata{KeyGrant: want}); err != nil {
				return err
			}
			changes = append(changes, fmt.Sprintf("%s: %s may write it (%s)", r.Name, app, want))
		}
		return nil
	})
	return changes, waiting, err
}
