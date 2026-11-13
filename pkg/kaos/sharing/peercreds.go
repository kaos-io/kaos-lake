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
	"github.com/treeverse/lakefs/pkg/kaos/repos"
)

// An app reads another project's shared datasets with its own identity in two calls. The app asks its
// own project's lakeFS at SharedCredentialsPath (PeerExchangeHandler), which asks the owner's at
// PeerCredentialsPath (PeerCredentialsHandler) for a key of that app.
const (
	SharedCredentialsPath = "/kaos/v1/shared-credentials"
	PeerCredentialsPath   = "/kaos/v1/peer-credentials"
)

const maxPeerBody = 4 << 10

// PeerKey is the access key both endpoints answer with: an exchanged key of one app, which expires.
type PeerKey struct {
	AccessKeyID     string    `json:"access_key_id"`
	SecretAccessKey string    `json:"secret_access_key"`
	Expiration      time.Time `json:"expiration"`
}

// PeerUsers is what PeerCredentialsHandler needs of the multi-user store.
type PeerUsers interface {
	GetUser(ctx context.Context, username string) (*model.User, error)
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
// kaos-consumer-<project> group, which exists only while a dataset is shared with the project.
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
}

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
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": err.Error()})
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
		k, err = h.issue(ctx, repos.PeerUser(req.Project, req.App), group)
		if !errors.Is(err, auth.ErrNotFound) {
			break
		}
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, k)
}

// issue makes sure user exists, in group and no other, and gives it a new key.
func (h PeerCredentialsHandler) issue(ctx context.Context, user, group string) (*PeerKey, error) {
	if _, err := h.Users.GetUser(ctx, user); errors.Is(err, auth.ErrNotFound) {
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
	// An access key ID is at most 20 characters (pkg/gateway/sig).
	id := declared.ExchangeKeyPrefix + keys.KeyGenerator(20-len(declared.ExchangeKeyPrefix))
	cred, err := h.Users.AddCredentials(ctx, user, id, keys.Base64StringGenerator(30))
	if err != nil {
		return nil, fmt.Errorf("add key to %s: %w", user, err)
	}
	return &PeerKey{AccessKeyID: cred.AccessKeyID, SecretAccessKey: cred.SecretAccessKey, Expiration: cred.IssuedDate.Add(h.TTL)}, nil
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
		writeJSON(w, http.StatusBadGateway, map[string]string{"message": "cannot sign in to " + req.Owner + ": " + err.Error()})
		return
	}
	body, err := json.Marshal(peerRequest{Project: h.Project, App: app})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 4*PeerTimeout)
	defer cancel()
	out, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(peers[i].Endpoint, "/")+PeerCredentialsPath, bytes.NewReader(body))
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"message": err.Error()})
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
