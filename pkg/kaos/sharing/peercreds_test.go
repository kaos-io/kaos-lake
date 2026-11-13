// KAOS (kaos-lake, PRD novelcore/kaos#1911 D-44, kaos#2089): new file, Apache-2.0.

package sharing_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/treeverse/lakefs/contrib/auth/acl"
	"github.com/treeverse/lakefs/pkg/auth"
	"github.com/treeverse/lakefs/pkg/auth/crypt"
	"github.com/treeverse/lakefs/pkg/auth/declared"
	"github.com/treeverse/lakefs/pkg/auth/model"
	"github.com/treeverse/lakefs/pkg/auth/multiuser"
	authparams "github.com/treeverse/lakefs/pkg/auth/params"
	"github.com/treeverse/lakefs/pkg/auth/setup"
	"github.com/treeverse/lakefs/pkg/kaos/repos"
	"github.com/treeverse/lakefs/pkg/kaos/sharing"
	"github.com/treeverse/lakefs/pkg/kv/kvtest"
	"github.com/treeverse/lakefs/pkg/permissions"
)

func can(t *testing.T, svc auth.Service, user, action, resource string) bool {
	t.Helper()
	resp, err := svc.Authorize(t.Context(), &auth.AuthorizationRequest{
		Username:            user,
		RequiredPermissions: permissions.Node{Permission: permissions.Permission{Action: action, Resource: resource}},
	})
	require.NoError(t, err)
	return resp.Allowed
}

// The owner project "cars" shares dataset-roads and dataset-default with vision, dataset-roads and
// dataset-signs with orbits, and dataset-private with nobody. Each reading project's machine user
// is in its consumer group, as the token identity puts it there; "cams" has nothing shared with it.
type world struct {
	svc  auth.Service
	lake *lake
	h    sharing.PeerCredentialsHandler
}

func newWorld(t *testing.T) *world {
	t.Helper()
	ctx := t.Context()
	w := &world{
		// The service lakefs run uses in multi-user mode, set up like a fresh stack.
		svc:  &multiuser.Service{AuthService: acl.NewAuthService(kvtest.GetStore(ctx, t), crypt.NewSecretStore([]byte("k")), authparams.ServiceCache{})},
		lake: ownerLake(),
	}
	_, err := setup.CreateAdminUser(ctx, w.svc, &model.SuperuserConfiguration{
		User: model.User{Username: "admin", CreatedAt: time.Now()}, AccessKeyID: "AKIAJADMIN00000001Q", SecretAccessKey: "admin-secret",
	})
	require.NoError(t, err)
	w.h = sharing.PeerCredentialsHandler{Users: w.svc, TTL: time.Hour}
	_, err = repos.ReconcileShares(ctx, w.lake, w.svc)
	require.NoError(t, err)
	for _, project := range []string{"vision", "orbits"} {
		w.machine(t, project)
	}
	return w
}

func (w *world) machine(t *testing.T, project string) string {
	t.Helper()
	user := "machine@" + project
	_, err := w.svc.CreateUser(t.Context(), &model.User{Username: user, CreatedAt: time.Now(), Source: "zitadel"})
	require.NoError(t, err)
	if _, err := w.svc.GetGroup(t.Context(), repos.ConsumerGroupPrefix+project); err == nil {
		require.NoError(t, w.svc.AddUserToGroup(t.Context(), user, repos.ConsumerGroupPrefix+project))
	}
	return user
}

type key struct {
	AccessKeyID     string    `json:"access_key_id"`
	SecretAccessKey string    `json:"secret_access_key"`
	Expiration      time.Time `json:"expiration"`
}

// exchange asks the owner for the access key of app of project, as caller.
func (w *world) exchange(t *testing.T, caller, project, app string) (int, key) {
	t.Helper()
	body, err := json.Marshal(map[string]string{"project": project, "app": app})
	require.NoError(t, err)
	r := httptest.NewRequest(http.MethodPost, sharing.PeerCredentialsPath, bytes.NewReader(body))
	if caller != "" {
		u, err := w.svc.GetUser(t.Context(), caller) // as the middleware gives it, with its Source
		if err != nil {
			u = &model.User{Username: caller}
		}
		r = r.WithContext(auth.WithUser(r.Context(), u))
	}
	rec := httptest.NewRecorder()
	w.h.ServeHTTP(rec, r)
	var k key
	if rec.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &k))
	}
	return rec.Code, k
}

func (w *world) reads(t *testing.T, user, repo string) bool {
	t.Helper()
	a := can(t, w.svc, user, permissions.ReadObjectAction, permissions.ObjectArn(repo, "data.yaml"))
	b := can(t, w.svc, user, permissions.ListObjectsAction, permissions.RepoArn(repo))
	require.Equal(t, a, b, "read and list go together")
	return a
}

func (w *world) writes(t *testing.T, user, repo string) bool {
	t.Helper()
	for _, c := range []permissions.Permission{
		{Action: permissions.WriteObjectAction, Resource: permissions.ObjectArn(repo, "x")},
		{Action: permissions.DeleteObjectAction, Resource: permissions.ObjectArn(repo, "x")},
		{Action: permissions.CreateCommitAction, Resource: permissions.BranchArn(repo, "main")},
		{Action: permissions.CreateBranchAction, Resource: permissions.BranchArn(repo, "run-1")},
		{Action: permissions.CreateTagAction, Resource: permissions.TagArn(repo, "v1")},
		{Action: permissions.DeleteRepositoryAction, Resource: permissions.RepoArn(repo)},
		{Action: permissions.UpdateRepositoryAction, Resource: permissions.RepoArn(repo)},
	} {
		if can(t, w.svc, user, c.Action, c.Resource) {
			return true
		}
	}
	return false
}

// An app of the reading project is its own user in the owner's data store: it reads the datasets
// shared with its project, never writes one, never reads another of the owner's, and its key
// expires.
func TestPeerCredentials_ReaderAppReadsOnlyWhatIsShared(t *testing.T) {
	w := newWorld(t)
	code, k := w.exchange(t, "machine@vision", "vision", "yolo")
	require.Equal(t, http.StatusOK, code)

	user := repos.PeerUser("vision", "yolo")
	require.Equal(t, "kaos-peer-vision.yolo", user)
	require.True(t, strings.HasPrefix(k.AccessKeyID, declared.ExchangeKeyPrefix), "an exchanged key: it expires")
	require.LessOrEqual(t, len(k.AccessKeyID), 20)
	require.NotEmpty(t, k.SecretAccessKey)
	require.WithinDuration(t, time.Now().Add(time.Hour), k.Expiration, time.Minute)
	c, err := w.svc.GetCredentials(t.Context(), k.AccessKeyID)
	require.NoError(t, err)
	require.Equal(t, user, c.Username, "the key is the app's, not the machine user's")

	require.True(t, w.reads(t, user, "dataset-roads"))
	require.True(t, w.reads(t, user, "dataset-default"))
	require.False(t, w.reads(t, user, "dataset-signs"), "shared with orbits, not with vision")
	require.False(t, w.reads(t, user, "dataset-private"), "not shared with anyone")
	require.False(t, w.reads(t, user, "dataset-draft"), "its label names vision, but it is not labelled a dataset")
	require.False(t, w.reads(t, user, "outputs-yolo"))
	for _, repo := range []string{"dataset-roads", "dataset-default", "dataset-signs", "dataset-private"} {
		require.False(t, w.writes(t, user, repo), repo)
	}
	require.False(t, can(t, w.svc, user, permissions.CreateRepositoryAction, permissions.RepoArn("dataset-new")))

	groups, _, err := w.svc.ListUserGroups(t.Context(), user, &model.PaginationParams{Amount: -1})
	require.NoError(t, err)
	require.Len(t, groups, 1)
	require.Equal(t, repos.ConsumerGroupPrefix+"vision", groups[0].DisplayName)
	policies, _, err := w.svc.ListUserPolicies(t.Context(), user, &model.PaginationParams{Amount: -1})
	require.NoError(t, err)
	require.Empty(t, policies, "the user holds no policy of its own")
}

// A second exchange gives the same user the same key (see ReusesTheLiveKey); apps of one project are separate users.
func TestPeerCredentials_OneUserPerApp(t *testing.T) {
	w := newWorld(t)
	_, first := w.exchange(t, "machine@vision", "vision", "yolo")
	_, second := w.exchange(t, "machine@vision", "vision", "yolo")
	require.Equal(t, first.AccessKeyID, second.AccessKeyID)
	require.NotEqual(t, first.AccessKeyID, other0(t, w).AccessKeyID)
	_, other := w.exchange(t, "machine@vision", "vision", "segment")
	for _, k := range []key{first, second} {
		c, err := w.svc.GetCredentials(t.Context(), k.AccessKeyID)
		require.NoError(t, err)
		require.Equal(t, "kaos-peer-vision.yolo", c.Username)
	}
	c, err := w.svc.GetCredentials(t.Context(), other.AccessKeyID)
	require.NoError(t, err)
	require.Equal(t, "kaos-peer-vision.segment", c.Username)
	require.True(t, w.reads(t, "kaos-peer-vision.segment", "dataset-roads"))
}

// Apps read what is shared with their own project, and no project can ask for another's.
func TestPeerCredentials_ThirdProjectCannotRead(t *testing.T) {
	w := newWorld(t)
	_, v := w.exchange(t, "machine@vision", "vision", "yolo")
	require.NotEmpty(t, v.AccessKeyID)
	code, _ := w.exchange(t, "machine@orbits", "orbits", "yolo")
	require.Equal(t, http.StatusOK, code)
	require.True(t, w.reads(t, "kaos-peer-orbits.yolo", "dataset-signs"))
	require.False(t, w.reads(t, "kaos-peer-vision.yolo", "dataset-signs"))

	// A project nothing is shared with has no consumer group, so its machine user has none.
	cams := w.machine(t, "cams")
	code, _ = w.exchange(t, cams, "cams", "yolo")
	require.Equal(t, http.StatusUnauthorized, code)
	_, err := w.svc.GetUser(t.Context(), repos.PeerUser("cams", "yolo"))
	require.ErrorIs(t, err, auth.ErrNotFound, "no user is made for a refused request")

	// Vision's machine key cannot make an app of orbits, nor of cams.
	for _, project := range []string{"orbits", "cams"} {
		code, _ = w.exchange(t, "machine@vision", project, "yolo")
		require.Equal(t, http.StatusUnauthorized, code, project)
	}
	// Nor can a caller of the owner's own project: no consumer group, no peer user.
	_, err = w.svc.CreateUser(t.Context(), &model.User{Username: "kaos-app-yolo", CreatedAt: time.Now()})
	require.NoError(t, err)
	code, _ = w.exchange(t, "kaos-app-yolo", "vision", "yolo")
	require.Equal(t, http.StatusForbidden, code)
	code, _ = w.exchange(t, "", "vision", "yolo")
	require.Equal(t, http.StatusUnauthorized, code, "not signed in")
}

// Withdrawing a share removes the grant at once, for a key issued before; withdrawing the last one
// removes the group, and a new exchange is refused.
func TestPeerCredentials_WithdrawalRemovesAccess(t *testing.T) {
	ctx := t.Context()
	w := newWorld(t)
	_, k := w.exchange(t, "machine@vision", "vision", "yolo")
	user := repos.PeerUser("vision", "yolo")
	require.True(t, w.reads(t, user, "dataset-roads"))
	require.True(t, w.reads(t, user, "dataset-default"))

	w.lake.md["dataset-roads"][repos.KeySharedWith] = "orbits"
	_, err := repos.ReconcileShares(ctx, w.lake, w.svc)
	require.NoError(t, err)
	require.False(t, w.reads(t, user, "dataset-roads"), "that one dataset is gone from the app's reach")
	require.True(t, w.reads(t, user, "dataset-default"), "the other is not")

	w.lake.md["dataset-default"][repos.KeySharedWith] = ""
	_, err = repos.ReconcileShares(ctx, w.lake, w.svc)
	require.NoError(t, err)
	require.False(t, w.reads(t, user, "dataset-default"))
	_, err = w.svc.GetGroup(ctx, repos.ConsumerGroupPrefix+"vision")
	require.ErrorIs(t, err, auth.ErrNotFound)
	// The key itself still exists (it expires), but is worth nothing.
	_, err = w.svc.GetCredentials(ctx, k.AccessKeyID)
	require.NoError(t, err)
	code, _ := w.exchange(t, "machine@vision", "vision", "yolo")
	require.Equal(t, http.StatusUnauthorized, code)

	// Shared again: the app asks again and reads again.
	w.lake.md["dataset-default"][repos.KeySharedWith] = "vision"
	_, err = repos.ReconcileShares(ctx, w.lake, w.svc)
	require.NoError(t, err)
	vision := w.machine2(t, "machine2@vision", "vision")
	code, _ = w.exchange(t, vision, "vision", "yolo")
	require.Equal(t, http.StatusOK, code)
	require.True(t, w.reads(t, user, "dataset-default"))
	require.False(t, w.reads(t, user, "dataset-roads"))
}

// machine2 is a machine user made after its group was (re)created.
func (w *world) machine2(t *testing.T, user, project string) string {
	t.Helper()
	_, err := w.svc.CreateUser(t.Context(), &model.User{Username: user, CreatedAt: time.Now(), Source: "zitadel"})
	require.NoError(t, err)
	require.NoError(t, w.svc.AddUserToGroup(t.Context(), user, repos.ConsumerGroupPrefix+project))
	return user
}

// The owner's own apps, which declare no peer, keep what they had: nothing in a peer exchange
// touches them, and they still read what nobody shares.
func TestPeerCredentials_OwnersAppsAreUnaffected(t *testing.T) {
	ctx := t.Context()
	w := newWorld(t)
	own := declared.App{User: "kaos-app-yolo", AccessKeyID: "AKIAJYOLO000000001Q", SecretAccessKey: "s", OutputsRepository: "outputs-yolo"}
	_, err := declared.Reconcile(ctx, w.svc, []declared.App{own}, time.Hour)
	require.NoError(t, err)
	before, err := w.svc.GetPolicy(ctx, declared.AppPolicyName(own.User))
	require.NoError(t, err)

	_, _ = w.exchange(t, "machine@vision", "vision", "yolo") // same app name, other project
	require.True(t, w.reads(t, own.User, "dataset-private"))
	require.True(t, w.reads(t, own.User, "dataset-signs"))
	after, err := w.svc.GetPolicy(ctx, declared.AppPolicyName(own.User))
	require.NoError(t, err)
	require.Equal(t, before.Statement, after.Statement)
	c, err := w.svc.GetCredentials(ctx, own.AccessKeyID)
	require.NoError(t, err)
	require.Equal(t, own.User, c.Username)
	groups, _, err := w.svc.ListUserGroups(ctx, own.User, &model.PaginationParams{Amount: -1})
	require.NoError(t, err)
	require.Empty(t, groups, "an app of the owner joins no consumer group")

	// And a declared pass leaves the peer user alone while its key is young.
	changes, err := declared.Reconcile(ctx, w.svc, []declared.App{own}, time.Hour)
	require.NoError(t, err)
	require.Empty(t, changes)
	_, err = w.svc.GetUser(ctx, repos.PeerUser("vision", "yolo"))
	require.NoError(t, err)
}

func TestPeerCredentials_Refuses(t *testing.T) {
	w := newWorld(t)
	post := func(body string, h http.Handler) int {
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		r = r.WithContext(auth.WithUser(r.Context(), &model.User{Username: "machine@vision", Source: "zitadel"}))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec.Code
	}
	for _, body := range []string{``, `not json`, `{}`, `{"project":"vision"}`, `{"app":"yolo"}`,
		`{"project":"vision","app":"Yolo"}`, `{"project":"vision","app":"a.b"}`, `{"project":"vision","app":"-x"}`,
		`{"project":"Vision","app":"yolo"}`} {
		require.Equal(t, http.StatusBadRequest, post(body, w.h), body)
	}
	// Without a key lifetime (workload identity off) no key can be given out.
	noTTL := sharing.PeerCredentialsHandler{Users: w.svc}
	require.Equal(t, http.StatusServiceUnavailable, post(`{"project":"vision","app":"yolo"}`, noTTL))
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	w.h.ServeHTTP(rec, r)
	require.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

// The reading project's data store asks for the key on behalf of one of its own apps, with its
// machine key, and gives the app the owner's answer: through the real owner handler.
func TestPeerExchange_EndToEnd(t *testing.T) {
	w := newWorld(t)
	var seen struct {
		auth string
		body map[string]string
	}
	owner := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		seen.auth = r.Header.Get("Authorization")
		raw, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(raw, &seen.body))
		r.Body = io.NopCloser(bytes.NewReader(raw))
		user := map[string]string{"Bearer tok-vision": "machine@vision", "Bearer tok-cams": "machine@cams"}[seen.auth]
		if user != "" {
			r = r.WithContext(auth.WithUser(r.Context(), &model.User{Username: user, Source: "zitadel"}))
		}
		w.h.ServeHTTP(rw, r)
	}))
	defer owner.Close()

	apps := map[string]bool{"kaos-app-yolo": true, "kaos-app-segment": true}
	token := "tok-vision"
	h := sharing.PeerExchangeHandler{
		Project: "vision",
		Peers:   func() []sharing.Peer { return []sharing.Peer{{Project: "cars", Endpoint: owner.URL}} },
		Token:   func(context.Context) (string, error) { return token, nil },
		IsApp:   func(user string) bool { return apps[user] },
		Client:  owner.Client(),
	}
	ask := func(caller, ownerProject string) (int, key) {
		body, _ := json.Marshal(map[string]string{"owner": ownerProject})
		r := httptest.NewRequest(http.MethodPost, sharing.SharedCredentialsPath, bytes.NewReader(body))
		if caller != "" {
			r = r.WithContext(auth.WithUser(r.Context(), &model.User{Username: caller}))
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		var k key
		if rec.Code == http.StatusOK {
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &k))
		}
		return rec.Code, k
	}

	code, k := ask("kaos-app-yolo", "cars")
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, "Bearer tok-vision", seen.auth, "the owner is asked with the project's machine token")
	require.Equal(t, map[string]string{"project": "vision", "app": "yolo"}, seen.body, "the app is named by the user, not by the caller")
	c, err := w.svc.GetCredentials(t.Context(), k.AccessKeyID)
	require.NoError(t, err)
	require.Equal(t, "kaos-peer-vision.yolo", c.Username)
	require.True(t, w.reads(t, c.Username, "dataset-roads"))
	require.False(t, w.writes(t, c.Username, "dataset-roads"))

	// Only a declared app of this project may ask: a person, the admin, nobody.
	for _, caller := range []string{"admin", "alice@vision", ""} {
		code, _ = ask(caller, "cars")
		require.Contains(t, []int{http.StatusForbidden, http.StatusUnauthorized}, code, caller)
	}
	// Not a project of the organisation that this one knows.
	code, _ = ask("kaos-app-yolo", "nowhere")
	require.Equal(t, http.StatusNotFound, code)
	// The owner shares nothing with this project (401 there) is a refusal here, not a crash.
	token = "tok-cams"
	code, _ = ask("kaos-app-yolo", "cars")
	require.Equal(t, http.StatusForbidden, code)
	// A token that cannot be minted, an owner that is down.
	h.Token = func(context.Context) (string, error) { return "", errors.New("no key") }
	code, _ = ask("kaos-app-yolo", "cars")
	require.Equal(t, http.StatusBadGateway, code)
	h.Token = func(context.Context) (string, error) { return "tok-vision", nil }
	owner.Close()
	code, _ = ask("kaos-app-yolo", "cars")
	require.Equal(t, http.StatusBadGateway, code)
}

// Only the reading project's machine identity (a token-identity user, kaos-lake's Source "zitadel") asks
// for a key. A peer key, a declared app, the admin and a hand-made user are refused even when they are in the
// project's consumer group: a peer key cannot renew itself, and nobody else makes users here.
func TestPeerCredentials_OnlyTheMachineIdentityAsks(t *testing.T) {
	ctx := t.Context()
	w := newWorld(t)
	code, _ := w.exchange(t, "machine@vision", "vision", "yolo")
	require.Equal(t, http.StatusOK, code)
	for _, caller := range []string{repos.PeerUser("vision", "yolo"), "kaos-app-yolo", "admin", "hand-made@vision"} {
		if _, err := w.svc.GetUser(ctx, caller); errors.Is(err, auth.ErrNotFound) {
			_, err = w.svc.CreateUser(ctx, &model.User{Username: caller, CreatedAt: time.Now()})
			require.NoError(t, err)
		}
		if err := w.svc.AddUserToGroup(ctx, caller, repos.ConsumerGroupPrefix+"vision"); !errors.Is(err, auth.ErrAlreadyExists) {
			require.NoError(t, err)
		}
		code, _ = w.exchange(t, caller, "vision", "segment")
		require.Equal(t, http.StatusForbidden, code, caller)
	}
	_, err := w.svc.GetUser(ctx, repos.PeerUser("vision", "segment"))
	require.ErrorIs(t, err, auth.ErrNotFound, "no user was made for a refused caller")
	code, _ = w.exchange(t, "machine@vision", "vision", "segment")
	require.Equal(t, http.StatusOK, code)
}

func (w *world) keysOf(t *testing.T, user string) []string {
	t.Helper()
	creds, _, err := w.svc.ListUserCredentials(t.Context(), user, &model.PaginationParams{Amount: -1})
	require.NoError(t, err)
	var ids []string
	for _, c := range creds {
		ids = append(ids, c.AccessKeyID)
	}
	return ids
}

// A live key is handed out again, not a new one for each call: parallel steps of one app share it. A key
// with less than half its lifetime left is not: a new one is made, so every caller gets a key that works for
// at least half the lifetime. So a user holds at most two live keys.
func TestPeerCredentials_ReusesTheLiveKey(t *testing.T) {
	w := newWorld(t)
	w.h.TTL = 400 * time.Millisecond
	_, first := w.exchange(t, "machine@vision", "vision", "yolo")
	user := repos.PeerUser("vision", "yolo")
	for range 5 {
		_, again := w.exchange(t, "machine@vision", "vision", "yolo")
		requireSameKey(t, first, again, "same key, same secret, same expiry")
	}
	require.Len(t, w.keysOf(t, user), 1)

	time.Sleep(260 * time.Millisecond) // more than half of the lifetime is spent
	_, next := w.exchange(t, "machine@vision", "vision", "yolo")
	require.NotEqual(t, first.AccessKeyID, next.AccessKeyID)
	require.True(t, next.Expiration.After(first.Expiration))
	_, same := w.exchange(t, "machine@vision", "vision", "yolo")
	requireSameKey(t, next, same, "the newer key is the one handed out")
	require.Len(t, w.keysOf(t, user), 2)

	// Another app, another user, another key.
	_, other := w.exchange(t, "machine@vision", "vision", "segment")
	require.NotEqual(t, next.AccessKeyID, other.AccessKeyID)
}

// A reading project has at most MaxUsers apps here; the apps it has keep working, other projects count apart.
func TestPeerCredentials_CapsTheUsersOfAProject(t *testing.T) {
	w := newWorld(t)
	w.h.MaxUsers = 2
	for _, app := range []string{"a1", "a2"} {
		code, _ := w.exchange(t, "machine@vision", "vision", app)
		require.Equal(t, http.StatusOK, code, app)
	}
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"project":"vision","app":"a3"}`))
	r = r.WithContext(auth.WithUser(r.Context(), &model.User{Username: "machine@vision", Source: "zitadel"}))
	rec := httptest.NewRecorder()
	w.h.ServeHTTP(rec, r)
	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	require.Contains(t, rec.Body.String(), "at most 2 apps")
	_, err := w.svc.GetUser(t.Context(), repos.PeerUser("vision", "a3"))
	require.ErrorIs(t, err, auth.ErrNotFound)
	code, _ := w.exchange(t, "machine@vision", "vision", "a1")
	require.Equal(t, http.StatusOK, code, "an app that is already here still gets its key")
	code, _ = w.exchange(t, "machine@orbits", "orbits", "a1")
	require.Equal(t, http.StatusOK, code, "another project counts apart")
	require.Equal(t, 64, sharing.DefaultPeerUsersMax)
}

type failingStore struct {
	sharing.PeerUsers
}

func (failingStore) AddCredentials(context.Context, string, string, string) (*model.Credential, error) {
	return nil, errors.New("kv /secret/path/to/store: connection refused")
}

// A store error is logged, never answered.
func TestPeerCredentials_DoesNotAnswerStoreErrors(t *testing.T) {
	w := newWorld(t)
	h := sharing.PeerCredentialsHandler{Users: failingStore{w.svc}, TTL: time.Hour}
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"project":"vision","app":"yolo"}`))
	r = r.WithContext(auth.WithUser(r.Context(), &model.User{Username: "machine@vision", Source: "zitadel"}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.NotContains(t, rec.Body.String(), "secret")
	require.NotContains(t, rec.Body.String(), "refused")
	require.Contains(t, rec.Body.String(), "key could not be made")
}

// The reading side does not answer why its machine token could not be had.
func TestPeerExchange_DoesNotAnswerMintErrors(t *testing.T) {
	h := sharing.PeerExchangeHandler{
		Project: "vision",
		Peers:   func() []sharing.Peer { return []sharing.Peer{{Project: "cars", Endpoint: "http://127.0.0.1:1"}} },
		Token: func(context.Context) (string, error) {
			return "", errors.New("machine key /etc/kaos-lake/machine-key unreadable")
		},
		IsApp: func(string) bool { return true },
	}
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"owner":"cars"}`))
	r = r.WithContext(auth.WithUser(r.Context(), &model.User{Username: "kaos-app-yolo"}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	require.Equal(t, http.StatusBadGateway, rec.Code)
	require.NotContains(t, rec.Body.String(), "machine-key")
}

func requireSameKey(t *testing.T, a, b key, msg string) {
	t.Helper()
	require.Equal(t, a.AccessKeyID, b.AccessKeyID, msg)
	require.Equal(t, a.SecretAccessKey, b.SecretAccessKey, msg)
	require.True(t, a.Expiration.Equal(b.Expiration), msg)
}

func other0(t *testing.T, w *world) key {
	t.Helper()
	_, k := w.exchange(t, "machine@vision", "vision", "other")
	return k
}
