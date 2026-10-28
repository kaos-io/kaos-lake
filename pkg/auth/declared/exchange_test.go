// KAOS (kaos-lake, PRD novelcore/kaos#1217 F-05): new file, Apache-2.0.

package declared_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/treeverse/lakefs/pkg/auth"
	"github.com/treeverse/lakefs/pkg/auth/declared"
)

const exchanged = "KAOSXAAAAAAAAAAAAAAA"

// An exchanged key works until its TTL, stays with its declared app until then, and is deleted
// once expired; the declared key never expires.
func TestExchangedKeys(t *testing.T) {
	ctx := t.Context()
	svc := newStore(t)
	_, err := declared.Reconcile(ctx, svc, []declared.App{yolo}, time.Hour)
	require.NoError(t, err)
	_, err = svc.AddCredentials(ctx, yolo.User, exchanged, "exchanged-secret")
	require.NoError(t, err)

	changes, err := declared.Reconcile(ctx, svc, []declared.App{yolo}, time.Hour)
	require.NoError(t, err)
	require.Empty(t, changes, "a fresh exchanged key is kept")
	c, err := declared.ExpiringKeys(svc, time.Hour).GetCredentials(ctx, exchanged)
	require.NoError(t, err)
	require.Equal(t, yolo.User, c.Username)

	expired := declared.ExpiringKeys(svc, 0)
	_, err = expired.GetCredentials(ctx, exchanged)
	require.ErrorIs(t, err, auth.ErrNotFound)
	_, err = expired.GetCredentials(ctx, yolo.AccessKeyID)
	require.NoError(t, err, "declared keys do not expire")

	_, err = declared.Reconcile(ctx, svc, []declared.App{yolo}, 0)
	require.NoError(t, err)
	requireNoKey(t, svc, exchanged)
	requireKey(t, svc, yolo)
}

// An app no longer declared loses its exchanged keys with the user, however fresh.
func TestExchangedKeys_GoWithTheApp(t *testing.T) {
	ctx := t.Context()
	svc := newStore(t)
	_, err := declared.Reconcile(ctx, svc, []declared.App{yolo}, time.Hour)
	require.NoError(t, err)
	_, err = svc.AddCredentials(ctx, yolo.User, exchanged, "exchanged-secret")
	require.NoError(t, err)

	_, err = declared.Reconcile(ctx, svc, nil, time.Hour)
	require.NoError(t, err)
	requireNoKey(t, svc, exchanged)
	_, err = svc.GetUser(ctx, yolo.User)
	require.ErrorIs(t, err, auth.ErrNotFound)
	// Nor does the key come back with an app declared again under the same name.
	_, err = declared.Reconcile(ctx, svc, []declared.App{yolo}, time.Hour)
	require.NoError(t, err)
	_, err = declared.ExpiringKeys(svc, time.Hour).GetCredentials(ctx, exchanged)
	require.ErrorIs(t, err, auth.ErrNotFound)
}
