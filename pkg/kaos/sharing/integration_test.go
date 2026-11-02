// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-12): new file, Apache-2.0.

package sharing_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/treeverse/lakefs/contrib/auth/acl"
	"github.com/treeverse/lakefs/pkg/auth"
	"github.com/treeverse/lakefs/pkg/auth/crypt"
	authparams "github.com/treeverse/lakefs/pkg/auth/params"
	"github.com/treeverse/lakefs/pkg/auth/tokenid"
	"github.com/treeverse/lakefs/pkg/config"
	"github.com/treeverse/lakefs/pkg/graveler"
	"github.com/treeverse/lakefs/pkg/kaos/datacatalog"
	"github.com/treeverse/lakefs/pkg/kaos/repos"
	"github.com/treeverse/lakefs/pkg/kaos/sharing"
	"github.com/treeverse/lakefs/pkg/kv/kvtest"
)

// Two projects of one organisation, each with its own "lakeFS": cars owns dataset-roads, vision's
// pipelines list it in their catalog once cars shares it, and stop seeing it after the unshare
// and the cache TTL. The owner side runs the real token verification (pkg/auth/tokenid), the
// multi-user store and ReconcileShares; the consumer side the real peers file, Zitadel mint and
// aggregation, against a fake Zitadel.
func TestTwoProjectsShareADataset(t *testing.T) {
	ctx := t.Context()
	z := newZitadel(t) // vision's machine user: groups [kubeproject:vision:admin]

	// cars: its lakeFS, with token identity as the operator configures it.
	svc := acl.NewAuthService(kvtest.GetStore(ctx, t), crypt.NewSecretStore([]byte("k")), authparams.ServiceCache{})
	carsLake := newLake(map[string]map[string]string{
		"dataset-roads":   {repos.KeyKind: "dataset", repos.KeyDescription: "roads"},
		"dataset-private": {repos.KeyKind: "dataset"},
	})
	id, err := tokenid.New(config.KAOSTokenIdentity{
		Issuer: z.URL, Audiences: []string{orgProjectID}, Project: "cars", ConsumerClaimPrefixes: []string{"kubeproject:"},
		Groups: []struct {
			Claim string `mapstructure:"claim"`
			Group string `mapstructure:"group"`
		}{{Claim: "kubeproject:cars:admin", Group: "Admins"}},
	}, svc)
	require.NoError(t, err)
	shared := sharing.SharedHandler{Catalog: carsLake, Groups: svc, Owner: "cars"}
	cars := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// as GenericAuthMiddleware does for an identity-provider bearer token
		token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if r.URL.Path != sharing.SharedPath || !id.Owns(token) {
			http.Error(w, "not here", http.StatusNotFound)
			return
		}
		user, err := id.Resolve(r.Context(), token)
		if err != nil {
			http.Error(w, "denied", http.StatusUnauthorized)
			return
		}
		shared.ServeHTTP(w, r.WithContext(auth.WithUser(r.Context(), user)))
	}))
	t.Cleanup(cars.Close)
	share := func(with string) {
		if with == "" {
			require.NoError(t, carsLake.DeleteRepositoryMetadata(ctx, "dataset-roads", []string{repos.KeySharedWith}))
		} else {
			require.NoError(t, carsLake.UpdateRepositoryMetadata(ctx, "dataset-roads", graveler.RepositoryMetadata{repos.KeySharedWith: with}))
		}
		_, err := repos.ReconcileShares(ctx, carsLake, svc) // what repos.Kick starts
		require.NoError(t, err)
	}

	// vision: its peers file names cars; its catalog adds what peers share.
	dir := t.TempDir()
	peersPath := filepath.Join(dir, "peers.json")
	write(t, peersPath, `{"project":"vision","peers":[{"project":"cars","endpoint":"`+cars.URL+`","url":"https://lakefs-cars.acme.kaos.eu"}]}`)
	visionSharing, err := sharing.New(config.KAOSSharing{PeersPath: peersPath, MachineKeyPath: z.machineKey(t, dir, "key-1")},
		config.KAOSTokenIdentity{Issuer: z.URL, Audiences: []string{orgProjectID}, Project: "vision"}, nil)
	require.NoError(t, err)
	_, err = visionSharing.Peers.Load()
	require.NoError(t, err)
	c := newClock()
	visionSharing.Aggregator.Now = c.Now
	catalogHandler := datacatalog.Handler{
		Catalog: newLake(map[string]map[string]string{"dataset-own": {repos.KeyKind: "dataset", repos.KeyDefault: "true"}}),
		Verify: func(_ context.Context, token string, _ []string) (string, error) {
			if token != "ci-token" {
				return "", errors.New("denied")
			}
			return "vision-ci", nil
		},
		Namespaces: []string{"vision-ci"},
		Shared:     visionSharing.Aggregator.Shared,
	}
	list := func() (string, []datacatalog.Dataset) {
		r := httptest.NewRequest(http.MethodGet, "/kaos/v1/catalog", nil)
		r.Header.Set("Authorization", "Bearer ci-token")
		rec := httptest.NewRecorder()
		catalogHandler.ServeHTTP(rec, r)
		require.Equal(t, http.StatusOK, rec.Code)
		var body struct{ Datasets []datacatalog.Dataset }
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		return rec.Body.String(), body.Datasets
	}
	own := datacatalog.Dataset{Name: "dataset-own", Default: true, Latest: "head-dataset-own",
		Versions: []datacatalog.Version{{Tag: "v1", Commit: "c1-dataset-own"}}}
	roads := datacatalog.Dataset{Name: "cars/dataset-roads", Owner: "cars", Description: "roads", Latest: "head-dataset-roads",
		Versions: []datacatalog.Version{{Tag: "v1", Commit: "c1-dataset-roads"}}}

	// Nothing shared yet: cars answers 401 (vision has no consumer group there), read as empty.
	_, got := list()
	require.Equal(t, []datacatalog.Dataset{own}, got)

	share("vision,orbits")
	_, got = list()
	require.Equal(t, []datacatalog.Dataset{own}, got, "the cache answers until its TTL")
	c.Add(61 * time.Second)
	raw, got := list()
	require.Equal(t, []datacatalog.Dataset{own, roads}, got)
	require.NotContains(t, raw, "orbits", "another consumer is never revealed")
	require.NotContains(t, raw, "dataset-private")
	require.EqualValues(t, 1, z.mints.Load(), "the machine token is minted once and reused")

	share("")
	_, got = list()
	require.Equal(t, []datacatalog.Dataset{own, roads}, got, "within the TTL the cache answers")
	c.Add(61 * time.Second)
	_, got = list()
	require.Equal(t, []datacatalog.Dataset{own}, got, "unshared: gone after the TTL")
	_, err = svc.GetGroup(ctx, repos.ConsumerGroupPrefix+"vision")
	require.ErrorIs(t, err, auth.ErrNotFound)
}

// sharing.New is off without a peers file and refuses to start without token identity.
func TestNew(t *testing.T) {
	s, err := sharing.New(config.KAOSSharing{}, config.KAOSTokenIdentity{}, nil)
	require.NoError(t, err)
	require.Nil(t, s, "no kaos.sharing: off")
	_, err = sharing.New(config.KAOSSharing{PeersPath: "/p"}, config.KAOSTokenIdentity{}, nil)
	require.ErrorContains(t, err, "kaos.sharing needs kaos.token_identity")
	s, err = sharing.New(config.KAOSSharing{PeersPath: "/p"}, config.KAOSTokenIdentity{Issuer: "https://z", Audiences: []string{"1"}}, nil)
	require.NoError(t, err)
	require.Equal(t, sharing.DefaultCacheTTL, s.Aggregator.TTL)
}
