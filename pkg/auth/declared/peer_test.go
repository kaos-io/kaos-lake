// KAOS (kaos-lake, PRD novelcore/kaos#1911 D-44, kaos#2089): new file, Apache-2.0.

package declared_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/treeverse/lakefs/pkg/auth"
	"github.com/treeverse/lakefs/pkg/auth/declared"
	"github.com/treeverse/lakefs/pkg/auth/model"
	"github.com/treeverse/lakefs/pkg/kaos/repos"
)

// peer makes the user an app of project reads as in this store, with one exchanged key, and puts it
// in the project's consumer group when inGroup.
func peer(t *testing.T, svc auth.Service, project, app, keyID string, inGroup bool) string {
	t.Helper()
	ctx := t.Context()
	user := repos.PeerUser(project, app)
	_, err := svc.CreateUser(ctx, &model.User{Username: user, CreatedAt: time.Now()})
	require.NoError(t, err)
	_, err = svc.AddCredentials(ctx, user, keyID, "secret")
	require.NoError(t, err)
	if inGroup {
		group := repos.ConsumerGroupPrefix + project
		if _, err := svc.GetGroup(ctx, group); err != nil {
			_, err = svc.CreateGroup(ctx, &model.Group{DisplayName: group, CreatedAt: time.Now()})
			require.NoError(t, err)
		}
		require.NoError(t, svc.AddUserToGroup(ctx, user, group))
	}
	return user
}

// A peer user of the owner's store (an app of a reading project) keeps its young key and its group;
// once the key has expired the user goes; one whose project is no longer shared with goes at once.
// Declared apps are untouched throughout.
func TestReconcile_PeerUsers(t *testing.T) {
	ctx := t.Context()
	svc := newStore(t)
	_, err := declared.Reconcile(ctx, svc, []declared.App{yolo}, time.Hour)
	require.NoError(t, err)
	young := peer(t, svc, "vision", "yolo", "KAOSXAAAAAAAAAAAAAAA", true)
	orphan := peer(t, svc, "orbits", "yolo", "KAOSXBBBBBBBBBBBBBBB", false) // its group is gone: withdrawn

	changes, err := declared.Reconcile(ctx, svc, []declared.App{yolo}, time.Hour)
	require.NoError(t, err)
	require.NotEmpty(t, changes)
	_, err = svc.GetUser(ctx, young)
	require.NoError(t, err, "a young key keeps its user")
	_, err = svc.GetUser(ctx, orphan)
	require.ErrorIs(t, err, auth.ErrNotFound, "no consumer group, no peer user")
	requireNoKey(t, svc, "KAOSXBBBBBBBBBBBBBBB")
	requireKey(t, svc, yolo)

	changes, err = declared.Reconcile(ctx, svc, []declared.App{yolo}, time.Hour)
	require.NoError(t, err)
	require.Empty(t, changes, "settled")

	_, err = declared.Reconcile(ctx, svc, []declared.App{yolo}, 0) // every exchanged key is old now
	require.NoError(t, err)
	_, err = svc.GetUser(ctx, young)
	require.ErrorIs(t, err, auth.ErrNotFound)
	requireNoKey(t, svc, "KAOSXAAAAAAAAAAAAAAA")
	_, err = svc.GetUser(ctx, yolo.User)
	require.NoError(t, err)
	requireKey(t, svc, yolo)
}
