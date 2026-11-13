// KAOS (kaos-lake, PRD novelcore/kaos#1911 D-44, kaos#2089): new file, Apache-2.0.

package sharing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/treeverse/lakefs/pkg/auth"
	"github.com/treeverse/lakefs/pkg/auth/declared"
	"github.com/treeverse/lakefs/pkg/auth/keys"
	"github.com/treeverse/lakefs/pkg/auth/model"
	"github.com/treeverse/lakefs/pkg/auth/tokenid"
	"github.com/treeverse/lakefs/pkg/kaos/repos"
	"github.com/treeverse/lakefs/pkg/logging"
)

// An app reads another project's shared datasets with its own identity in two calls. The app asks its
// own project's lakeFS at SharedCredentialsPath (PeerExchangeHandler), which asks the owner's at
// PeerCredentialsPath (PeerCredentialsHandler) for a key of that app.
const (
	SharedCredentialsPath = "/kaos/v1/shared-credentials"
	PeerCredentialsPath   = "/kaos/v1/peer-credentials"
)

const maxPeerBody = 4 << 10

// DefaultPeerUsersMax is how many apps of one reading project may hold a key in an owner's data store
// (kaos.sharing.peer_users_max).
const DefaultPeerUsersMax = 64

// PeerKey is the access key both endpoints answer with: an exchanged key of one app, which expires.
type PeerKey struct {
	AccessKeyID     string    `json:"access_key_id"`
	SecretAccessKey string    `json:"secret_access_key"`
	Expiration      time.Time `json:"expiration"`
}

// PeerUsers is what PeerCredentialsHandler needs of the multi-user store.
type PeerUsers interface {
	GetUser(ctx context.Context, username string) (*model.User, error)
	ListUsers(ctx context.Context, params *model.PaginationParams) ([]*model.User, *model.Paginator, error)
	ListUserCredentials(ctx context.Context, username string, params *model.PaginationParams) ([]*model.Credential, *model.Paginator, error)
	GetCredentials(ctx context.Context, accessKeyID string) (*model.Credential, error)
	CreateUser(ctx context.Context, user *model.User) (string, error)
	ListUserGroups(ctx context.Context, username string, params *model.PaginationParams) ([]*model.Group, *model.Paginator, error)
	AddUserToGroup(ctx context.Context, username, groupID string) error
	RemoveUserFromGroup(ctx context.Context, username, groupID string) error
	AddCredentials(ctx context.Context, username, accessKeyID, secretAccessKey string) (*model.Credential, error)
}

type peerRequest struct {
	Project string `json:"project"`
	App     string `json:"app"`
}

// PeerCredentialsHandler answers POST /kaos/v1/peer-credentials on the owner side, behind the API's
// authentication (kaos#2089, PRD novelcore/kaos#1911 D-44).
//
// The caller is a reading project's machine user: its token puts it in the project's
// kaos-consumer-<project> group, which exists only while a dataset is shared with the project. It
// is recognised by being a user made from an identity-provider token (Source tokenid.UserSource): a peer
// key (kaos-peer-*), a declared app (kaos-app-*), the admin and any hand-made user are refused, so a key
// cannot renew itself and only the reading project's data store makes peer users. The code has no claim
// that tells the project's machine user from its people (both carry the project's role claims), so a
// person of the project in the group is not refused; what such a person can do is bounded by MaxUsers and
// is read access they already hold.
// The body names that project and one of its apps. The handler gives the app its own user,
// kaos-peer-<project>.<app>, in that group and no other, and returns a new access key of that user
// which stops working after TTL. The user holds no policy: it reads exactly what the group does,
// the datasets shared with its project (repos.ReconcileShares), never writes, and loses every
// grant when a share is withdrawn, because the group's policy changes or the group goes. The
// owner's data store does not check that the app exists: the project's own data store names its
// apps, and the machine user of the project is already trusted for everything the project reads.
type PeerCredentialsHandler struct {
	Users PeerUsers
	// TTL is how long a key works (kaos.workload_identity.key_ttl); zero: no key is given out,
	// because workload identity is off and nothing would expire it.
	TTL time.Duration
	// MaxUsers is how many apps of one project may hold a key here; zero: DefaultPeerUsersMax.
	MaxUsers int
}

func (h PeerCredentialsHandler) maxUsers() int {
	if h.MaxUsers > 0 {
		return h.MaxUsers
	}
	return DefaultPeerUsersMax
}

// errTooMany marks a project that already has MaxUsers apps here.
var errTooMany = errors.New("too many apps")

func (h PeerCredentialsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"message": "POST"})
		return
	}
	caller, err := auth.GetUser(ctx)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"message": "sign in to read shared datasets"})
		return
	}
	if caller.Source != tokenid.UserSource {
		writeJSON(w, http.StatusForbidden, map[string]string{"message": "only the reading project's data store asks for a key here"})
		return
	}
	var req peerRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxPeerBody)).Decode(&req); err != nil || !repos.ValidProject(req.Project) || !repos.ValidProject(req.App) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": `body: {"project": "<your project>", "app": "<your app>"}`})
		return
	}
	if h.TTL <= 0 {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"message": "this data store gives apps of other projects no keys"})
		return
	}
	group := repos.ConsumerGroupPrefix + req.Project
	groups, _, err := h.Users.ListUserGroups(ctx, caller.Username, &model.PaginationParams{Amount: -1})
	if err != nil && !errors.Is(err, auth.ErrNotFound) {
		logging.FromContext(ctx).WithError(err).Error("Peer key: groups of the caller not read")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": peerKeyFailed})
		return
	}
	if !slices.ContainsFunc(groups, func(g *model.Group) bool { return g.DisplayName == group }) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"message": "nothing is shared with your project here"})
		return
	}
	var k *PeerKey
	// A pass of the reconcile may delete a peer user whose keys have all expired between the two
	// steps: then make it again.
	for range 2 {
		k, err = h.issue(ctx, req.Project, repos.PeerUser(req.Project, req.App), group)
		if !errors.Is(err, auth.ErrNotFound) {
			break
		}
	}
	if errors.Is(err, errTooMany) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"message": fmt.Sprintf(
			"project %s already has %d apps here: at most %d apps of a project read this data store; ask the platform team", req.Project, h.maxUsers(), h.maxUsers())})
		return
	}
	if err != nil {
		logging.FromContext(ctx).WithError(err).WithField("user", repos.PeerUser(req.Project, req.App)).Error("Peer key not made")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": peerKeyFailed})
		return
	}
	writeJSON(w, http.StatusOK, k)
}

const peerKeyFailed = "the key could not be made; try again, and ask the platform team if it stays"

// issue makes sure user exists, in group and no other, and gives it a new key.
func (h PeerCredentialsHandler) issue(ctx context.Context, project, user, group string) (*PeerKey, error) {
	if _, err := h.Users.GetUser(ctx, user); errors.Is(err, auth.ErrNotFound) {
		if n, err := h.countUsers(ctx, project); err != nil {
			return nil, err
		} else if n >= h.maxUsers() {
			return nil, errTooMany
		}
		if _, err := h.Users.CreateUser(ctx, &model.User{Username: user, CreatedAt: time.Now()}); err != nil && !errors.Is(err, auth.ErrAlreadyExists) {
			return nil, fmt.Errorf("create %s: %w", user, err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("get %s: %w", user, err)
	}
	have, _, err := h.Users.ListUserGroups(ctx, user, &model.PaginationParams{Amount: -1})
	if err != nil {
		return nil, fmt.Errorf("list groups of %s: %w", user, err)
	}
	inGroup := false
	for _, g := range have {
		if g.DisplayName == group {
			inGroup = true
			continue
		}
		if err := h.Users.RemoveUserFromGroup(ctx, user, g.DisplayName); err != nil {
			return nil, fmt.Errorf("remove %s from %s: %w", user, g.DisplayName, err)
		}
	}
	if !inGroup {
		if err := h.Users.AddUserToGroup(ctx, user, group); err != nil {
			return nil, fmt.Errorf("add %s to %s: %w", user, group, err)
		}
	}
	if k, err := h.liveKey(ctx, user); err != nil || k != nil {
		return k, err
	}
	// An access key ID is at most 20 characters (pkg/gateway/sig).
	id := declared.ExchangeKeyPrefix + keys.KeyGenerator(20-len(declared.ExchangeKeyPrefix))
	cred, err := h.Users.AddCredentials(ctx, user, id, keys.Base64StringGenerator(30))
	if err != nil {
		return nil, fmt.Errorf("add key to %s: %w", user, err)
	}
	return &PeerKey{AccessKeyID: cred.AccessKeyID, SecretAccessKey: cred.SecretAccessKey, Expiration: cred.IssuedDate.Add(h.TTL)}, nil
}

// liveKey returns the newest exchanged key of user that has more than half of TTL left, or nil. Every
// call of one app, from any number of parallel steps, gets that same key (it reads the same datasets
// whoever holds it), so the user has one key until it is half spent and at most two while both live;
// the declared reconcile deletes an expired one. A caller thus always holds a key with at least half of
// TTL left.
func (h PeerCredentialsHandler) liveKey(ctx context.Context, user string) (*PeerKey, error) {
	creds, _, err := h.Users.ListUserCredentials(ctx, user, &model.PaginationParams{Amount: -1})
	if err != nil {
		return nil, fmt.Errorf("list keys of %s: %w", user, err)
	}
	var best *model.Credential
	for _, c := range creds {
		if !strings.HasPrefix(c.AccessKeyID, declared.ExchangeKeyPrefix) || c.IssuedDate.Add(h.TTL/2).Before(time.Now()) {
			continue
		}
		if best == nil || c.IssuedDate.After(best.IssuedDate) {
			best = c
		}
	}
	if best == nil {
		return nil, nil
	}
	full, err := h.Users.GetCredentials(ctx, best.AccessKeyID) // the list leaves the secret out
	if err != nil {
		return nil, fmt.Errorf("read key of %s: %w", user, err)
	}
	return &PeerKey{AccessKeyID: full.AccessKeyID, SecretAccessKey: full.SecretAccessKey, Expiration: full.IssuedDate.Add(h.TTL)}, nil
}

// countUsers returns how many apps of project hold a user here.
func (h PeerCredentialsHandler) countUsers(ctx context.Context, project string) (int, error) {
	n := 0
	params := &model.PaginationParams{Prefix: repos.PeerUserPrefix + project + ".", Amount: -1}
	for {
		users, page, err := h.Users.ListUsers(ctx, params)
		if err != nil {
			return 0, fmt.Errorf("list peer users of %s: %w", project, err)
		}
		n += len(users)
		if page == nil || page.NextPageToken == "" {
			return n, nil
		}
		params.After = page.NextPageToken
	}
}

// PeerExchangeHandler answers POST /kaos/v1/shared-credentials on the reading project's side, behind
// the API's authentication: an app asks for a key to read the datasets project "owner" shares with
// this project. The caller must be one of this project's declared apps (IsApp), whichever way it
// signed in (its workload token, its key). The handler asks the owner's lakeFS with this project's
// machine token, naming the app, and answers with the owner's key. The app never holds the
// project's machine key, and the key it is given works as that app only and on what is shared.
type PeerExchangeHandler struct {
	// Project is this project (kaos.token_identity.project).
	Project string
	// Peers returns the projects this one reads from (the peers file).
	Peers func() []Peer
	// Token returns the machine token sent to the owner (Minter.Token).
	Token func(ctx context.Context) (string, error)
	// IsApp reports whether user is a declared app user of this project.
	IsApp  func(user string) bool
	Client *http.Client
}

func (h PeerExchangeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"message": "POST"})
		return
	}
	caller, err := auth.GetUser(ctx)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"message": "sign in to read shared datasets"})
		return
	}
	app, ok := strings.CutPrefix(caller.Username, declared.UserPrefix)
	if !ok || !h.IsApp(caller.Username) {
		writeJSON(w, http.StatusForbidden, map[string]string{"message": "only an app of this project reads another project's datasets this way"})
		return
	}
	var req struct {
		Owner string `json:"owner"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxPeerBody)).Decode(&req); err != nil || !repos.ValidProject(req.Owner) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": `body: {"owner": "<the project that shares>"}`})
		return
	}
	peers := h.Peers()
	i := slices.IndexFunc(peers, func(p Peer) bool { return p.Project == req.Owner })
	if i < 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"message": req.Owner + " is not a project of your organisation that this data store knows"})
		return
	}
	token, err := h.Token(ctx)
	if err != nil {
		logging.FromContext(ctx).WithError(err).WithField("owner", req.Owner).Error("Peer key: machine token not minted")
		writeJSON(w, http.StatusBadGateway, map[string]string{"message": "cannot sign in to " + req.Owner + "'s data store; ask the platform team"})
		return
	}
	body, err := json.Marshal(peerRequest{Project: h.Project, App: app})
	if err != nil {
		logging.FromContext(ctx).WithError(err).Error("Peer key: request not encoded")
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": peerKeyFailed})
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 4*PeerTimeout)
	defer cancel()
	out, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(peers[i].Endpoint, "/")+PeerCredentialsPath, bytes.NewReader(body))
	if err != nil {
		logging.FromContext(ctx).WithError(err).Error("Peer key: request not built")
		writeJSON(w, http.StatusBadGateway, map[string]string{"message": peerKeyFailed})
		return
	}
	out.Header.Set("Authorization", "Bearer "+token)
	out.Header.Set("Content-Type", "application/json")
	client := h.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(out)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"message": req.Owner + "'s data store did not answer"})
		return
	}
	defer resp.Body.Close()
	var k PeerKey
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		writeJSON(w, http.StatusForbidden, map[string]string{"message": req.Owner + " shares no dataset with your project"})
	case resp.StatusCode == http.StatusOK && json.NewDecoder(io.LimitReader(resp.Body, maxPeerBody)).Decode(&k) == nil && k.AccessKeyID != "":
		writeJSON(w, http.StatusOK, k)
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed || resp.StatusCode == http.StatusOK:
		writeJSON(w, http.StatusBadGateway, map[string]string{"message": req.Owner + "'s data store is too old to share datasets with apps"})
	default:
		writeJSON(w, http.StatusBadGateway, map[string]string{"message": fmt.Sprintf("%s's data store answered HTTP %d", req.Owner, resp.StatusCode)})
	}
}
