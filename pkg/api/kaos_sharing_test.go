// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-12): new file, Apache-2.0.

package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/treeverse/lakefs/pkg/api/apigen"
	"github.com/treeverse/lakefs/pkg/api/apiutil"
	"github.com/treeverse/lakefs/pkg/kaos/repos"
)

func drainKicks() {
	for len(repos.Kicks()) > 0 {
		<-repos.Kicks()
	}
}

func kicked() bool {
	select {
	case <-repos.Kicks():
		return true
	case <-time.After(time.Second):
		return false
	}
}

// A metadata write or delete touching kaos.shared_with applies the shares at once (repos.Kick);
// other labels, and refused writes, do not.
func TestController_SharingKicksReconcile(t *testing.T) {
	clt, deps := setupClientWithAdmin(t)
	ctx := t.Context()
	resp, err := clt.CreateRepositoryWithResponse(ctx, &apigen.CreateRepositoryParams{}, apigen.CreateRepositoryJSONRequestBody{
		DefaultBranch: apiutil.Ptr("main"), Name: "dataset-kick", StorageNamespace: onBlock(deps, "dataset-kick"),
	})
	verifyResponseOK(t, resp, err)
	set := func(md map[string]string) int {
		resp, err := clt.SetRepositoryMetadataWithResponse(ctx, "dataset-kick", apigen.SetRepositoryMetadataJSONRequestBody{
			Metadata: apigen.RepositoryMetadataSet_Metadata{AdditionalProperties: md},
		})
		require.NoError(t, err)
		return resp.StatusCode()
	}
	del := func(keys ...string) int {
		resp, err := clt.DeleteRepositoryMetadataWithResponse(ctx, "dataset-kick", apigen.DeleteRepositoryMetadataJSONRequestBody{Keys: keys})
		require.NoError(t, err)
		return resp.StatusCode()
	}

	drainKicks()
	require.Equal(t, http.StatusNoContent, set(map[string]string{"kaos.description": "no sharing"}))
	require.Empty(t, repos.Kicks(), "a label other than kaos.shared_with")
	require.Equal(t, http.StatusNoContent, set(map[string]string{"kaos.shared_with": "orbits"}))
	require.True(t, kicked(), "share")
	require.Equal(t, http.StatusBadRequest, set(map[string]string{"kaos.shared_with": "Not A Project"}))
	require.Empty(t, repos.Kicks(), "a refused share")
	require.Equal(t, http.StatusNoContent, del("kaos.description"))
	require.Empty(t, repos.Kicks())
	require.Equal(t, http.StatusNoContent, del("kaos.shared_with"))
	require.True(t, kicked(), "unshare")
}

// /kaos/v1/peers answers any signed-in caller, by access key (an app) or by login token (a
// person): without kaos.sharing, no peers.
func TestKaosPeers(t *testing.T) {
	handler, _ := setupHandler(t)
	server := setupServer(t, handler)
	cred := createDefaultAdminUser(t, setupClientByEndpoint(t, server.URL, "", ""))
	get := func(set func(r *http.Request)) (int, string) {
		r, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/kaos/v1/peers", nil)
		require.NoError(t, err)
		set(r)
		resp, err := http.DefaultClient.Do(r)
		require.NoError(t, err)
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		return resp.StatusCode, string(body)
	}

	code, body := get(func(r *http.Request) { r.SetBasicAuth(cred.AccessKeyID, cred.SecretAccessKey) })
	require.Equal(t, http.StatusOK, code)
	require.JSONEq(t, `{"project":"","peers":[]}`, body)

	login, err := setupClientByEndpoint(t, server.URL, "", "").LoginWithResponse(t.Context(), apigen.LoginJSONRequestBody{
		AccessKeyId: cred.AccessKeyID, SecretAccessKey: cred.SecretAccessKey,
	})
	require.NoError(t, err)
	require.NotNil(t, login.JSON200)
	code, body = get(func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+login.JSON200.Token) })
	require.Equal(t, http.StatusOK, code)
	var peers struct {
		Peers []any `json:"peers"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &peers))
	require.NotNil(t, peers.Peers)

	code, _ = get(func(*http.Request) {})
	require.Equal(t, http.StatusUnauthorized, code)
	code, _ = get(func(r *http.Request) { r.SetBasicAuth(cred.AccessKeyID, "wrong") })
	require.Equal(t, http.StatusUnauthorized, code)
}

// /kaos/v1/shared-with-us is behind the API's authentication: any signed-in caller, by access key
// or login token; without kaos.sharing, nothing shared.
func TestKaosSharedWithUs(t *testing.T) {
	handler, _ := setupHandler(t)
	server := setupServer(t, handler)
	cred := createDefaultAdminUser(t, setupClientByEndpoint(t, server.URL, "", ""))
	get := func(set func(r *http.Request)) (int, string) {
		r, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/kaos/v1/shared-with-us", nil)
		require.NoError(t, err)
		set(r)
		resp, err := http.DefaultClient.Do(r)
		require.NoError(t, err)
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		return resp.StatusCode, string(body)
	}

	code, body := get(func(r *http.Request) { r.SetBasicAuth(cred.AccessKeyID, cred.SecretAccessKey) })
	require.Equal(t, http.StatusOK, code)
	require.JSONEq(t, `{"project":"","datasets":[],"notes":[]}`, body)

	login, err := setupClientByEndpoint(t, server.URL, "", "").LoginWithResponse(t.Context(), apigen.LoginJSONRequestBody{
		AccessKeyId: cred.AccessKeyID, SecretAccessKey: cred.SecretAccessKey,
	})
	require.NoError(t, err)
	require.NotNil(t, login.JSON200)
	code, _ = get(func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+login.JSON200.Token) })
	require.Equal(t, http.StatusOK, code)

	code, _ = get(func(*http.Request) {})
	require.Equal(t, http.StatusUnauthorized, code)
	code, _ = get(func(r *http.Request) { r.SetBasicAuth(cred.AccessKeyID, "wrong") })
	require.Equal(t, http.StatusUnauthorized, code)
}

// Without workload identity and token identity the key endpoints between projects (PRD novelcore/kaos#1911
// D-44, kaos#2089) are not served: no key comes back, to an admin either. (The route being served with
// the flags on needs a configured identity provider, which this harness does not have; pkg/kaos/sharing
// tests the handlers.)
func TestKaosPeerCredentialsAreOffWithoutIdentity(t *testing.T) {
	handler, _ := setupHandler(t)
	server := setupServer(t, handler)
	cred := createDefaultAdminUser(t, setupClientByEndpoint(t, server.URL, "", ""))
	for path, body := range map[string]string{
		"/kaos/v1/peer-credentials":   `{"project":"vision","app":"yolo"}`,
		"/kaos/v1/shared-credentials": `{"owner":"cars"}`,
	} {
		r, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+path, strings.NewReader(body))
		require.NoError(t, err)
		r.SetBasicAuth(cred.AccessKeyID, cred.SecretAccessKey)
		resp, err := http.DefaultClient.Do(r)
		require.NoError(t, err)
		raw, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		require.NoError(t, err)
		require.NotContains(t, string(raw), "access_key_id", path)
		require.NotContains(t, string(raw), "secret_access_key", path)
	}
}
