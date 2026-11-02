// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-12): new file, Apache-2.0.

package sharing_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/treeverse/lakefs/pkg/kaos/datacatalog"
	"github.com/treeverse/lakefs/pkg/kaos/sharing"
)

func sharedWithUs(t *testing.T, h sharing.SharedWithUsHandler) (string, sharing.SharedWithUsResponse) {
	t.Helper()
	rec := serve(h, "alice@vision")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	var body sharing.SharedWithUsResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	return rec.Body.String(), body
}

// The datasets peers share, with the repository in the owner's lakeFS and a link to it there; a
// note for each owner that is too old or down. Nothing about tokens, endpoints or other consumers.
func TestSharedWithUsHandler(t *testing.T) {
	cars := newPeer(t, "cars", `{"owner":"cars","datasets":[{"name":"dataset-roads","default":true,"description":"roads",
	 "location":"europe-west1 · Belgium","template":"yolo","latest":"h1","versions":[{"tag":"v2","commit":"c2"},{"tag":"v1","commit":"c1"}]}]}`)
	orbits := newPeer(t, "orbits", `{"owner":"orbits","datasets":[{"name":"dataset-stars","versions":[]}]}`)
	old := newPeer(t, "old", "")
	old.set(http.StatusNotFound, "")
	down := newPeer(t, "down", "")
	down.set(http.StatusInternalServerError, `{"message":"secret internals"}`)
	a := aggregator(newClock(), cars, down, old, orbits)

	raw, _ := sharedWithUs(t, sharing.SharedWithUsHandler{Project: "vision", Aggregator: a})
	require.JSONEq(t, `{"project":"vision","datasets":[
	 {"name":"cars/dataset-roads","owner":"cars","repo":"dataset-roads","location":"europe-west1 · Belgium","description":"roads",
	  "template":"yolo","latest":"h1","versions":[{"tag":"v2","commit":"c2"},{"tag":"v1","commit":"c1"}],
	  "url":"https://lakefs-cars.acme.kaos.eu/repositories/dataset-roads/objects"},
	 {"name":"orbits/dataset-stars","owner":"orbits","repo":"dataset-stars","versions":[],
	  "url":"https://lakefs-orbits.acme.kaos.eu/repositories/dataset-stars/objects"}],
	 "notes":[
	 {"owner":"down","note":"Project down's lakeFS did not answer; what it shares is not shown."},
	 {"owner":"old","note":"Project old's lakeFS is too old to share datasets; what it shares is not shown."}]}`, raw)
	for _, leak := range []string{"machine-token", cars.URL, down.URL, "secret internals", "default", "shared_with", "endpoint"} {
		require.NotContains(t, raw, leak)
	}
}

// One fan-out and one cache serve the catalog and the Datasets page: a read for one is reused by
// the other, and a peer that went down is served from the same stale copy, with a note here.
func TestSharedWithUsHandler_SharesTheCatalogCache(t *testing.T) {
	c := newClock()
	cars := newPeer(t, "cars", roadsV1)
	a := aggregator(c, cars)
	a.Timeout = 100 * time.Millisecond
	h := sharing.SharedWithUsHandler{Project: "vision", Aggregator: a}

	require.Equal(t, []datacatalog.Dataset{roads}, a.Shared(t.Context()), "the catalog")
	_, body := sharedWithUs(t, h)
	require.Len(t, body.Datasets, 1)
	require.Empty(t, body.Notes)
	require.EqualValues(t, 1, cars.hits.Load(), "the page reuses the catalog's answer")

	cars.set(http.StatusBadGateway, "")
	c.Add(61 * time.Second)
	_, body = sharedWithUs(t, h)
	require.Equal(t, "cars/dataset-roads", body.Datasets[0].Name, "the stale copy")
	require.Equal(t, []sharing.Note{{Owner: "cars", Note: "Project cars's lakeFS did not answer; showing its last known list."}}, body.Notes)
	require.Equal(t, []datacatalog.Dataset{roads}, a.Shared(t.Context()), "the catalog serves the same stale copy")
	require.EqualValues(t, 2, cars.hits.Load(), "and it is one request for both")

	c.Add(10 * time.Minute)
	_, body = sharedWithUs(t, h)
	require.Empty(t, body.Datasets, "older than 10 minutes: dropped")
	require.Equal(t, []sharing.Note{{Owner: "cars", Note: "Project cars's lakeFS did not answer; what it shares is not shown."}}, body.Notes)
	require.Empty(t, a.Shared(t.Context()))
}

// A peer that shares nothing (401, or an empty list) is no note: nothing is missing.
func TestSharedWithUsHandler_NothingShared(t *testing.T) {
	cars := newPeer(t, "cars", "")
	cars.set(http.StatusUnauthorized, `{"message":"nothing is shared"}`)
	orbits := newPeer(t, "orbits", `{"owner":"orbits","datasets":[]}`)
	raw, _ := sharedWithUs(t, sharing.SharedWithUsHandler{Project: "vision", Aggregator: aggregator(newClock(), cars, orbits)})
	require.JSONEq(t, `{"project":"vision","datasets":[],"notes":[]}`, raw)
}

// Without kaos.sharing the answer is empty, never null; a caller who is not signed in gets 401.
func TestSharedWithUsHandler_OffAndUnauthenticated(t *testing.T) {
	raw, _ := sharedWithUs(t, sharing.SharedWithUsHandler{Project: "vision"})
	require.JSONEq(t, `{"project":"vision","datasets":[],"notes":[]}`, raw)

	cars := newPeer(t, "cars", roadsV1)
	rec := serve(sharing.SharedWithUsHandler{Project: "vision", Aggregator: aggregator(newClock(), cars)}, "")
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.NotContains(t, rec.Body.String(), "dataset-roads")
	require.Zero(t, cars.hits.Load(), "no peer is asked for a caller who is not signed in")
}
