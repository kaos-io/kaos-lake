// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-10): new file, Apache-2.0.

package meluxina_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/treeverse/lakefs/pkg/auth"
	"github.com/treeverse/lakefs/pkg/auth/model"
	"github.com/treeverse/lakefs/pkg/kaos/meluxina"
	"github.com/treeverse/lakefs/pkg/kv/kvtest"
)

var now = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

const valid = `{"taken_at":"2026-11-01T11:59:00Z",
 "space":{"used_bytes":10,"limit_bytes":100,"free_bytes":90,"used_files":3,"limit_files":1000},
 "staged":[{"repo":"dataset-cars","commit":"abc123","ref":"v1","files":3,"bytes":10,
   "staged_at":"2026-11-01T10:00:00Z","last_used":"2026-11-01T11:00:00Z","leased":true}],
 "retention":"least recently used first","last_error":""}`

func newHandler(t *testing.T) meluxina.Handler {
	t.Helper()
	return meluxina.Handler{
		Service:    meluxina.NewService(meluxina.KVStore{KV: kvtest.GetStore(t.Context(), t)}),
		Namespaces: []string{"stager"},
		Now:        func() time.Time { return now },
		Verify: func(_ context.Context, token string, ns []string) (string, error) {
			if token == "stager-token" && ns[0] == "stager" {
				return "stager", nil
			}
			return "", errors.New("denied")
		},
	}
}

func post(h meluxina.Handler, authz, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/kaos/v1/meluxina/snapshot", strings.NewReader(body))
	if authz != "" {
		r.Header.Set("Authorization", authz)
	}
	rec := httptest.NewRecorder()
	h.Post(rec, r)
	return rec
}

func get(h meluxina.Handler, signedIn bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "/kaos/v1/meluxina", nil)
	if signedIn {
		r = r.WithContext(auth.WithUser(r.Context(), &model.User{Username: "alice"}))
	}
	rec := httptest.NewRecorder()
	h.Get(rec, r)
	return rec
}

func TestPostAndGet(t *testing.T) {
	h := newHandler(t)
	require.Equal(t, http.StatusUnauthorized, get(h, false).Code, "people must be signed in")
	empty := get(h, true)
	require.Equal(t, http.StatusOK, empty.Code)
	require.JSONEq(t, `{"snapshot":null,"server_time":"2026-11-01T12:00:00Z"}`, empty.Body.String())

	require.Equal(t, http.StatusUnauthorized, post(h, "", valid).Code)
	require.Equal(t, http.StatusUnauthorized, post(h, "Bearer other", valid).Code)
	ok := post(h, "Bearer stager-token", valid)
	require.Equal(t, http.StatusOK, ok.Code, ok.Body.String())
	require.JSONEq(t, `{"stored":true}`, ok.Body.String())

	got := get(h, true)
	require.Equal(t, http.StatusOK, got.Code)
	require.Contains(t, got.Body.String(), `"repo":"dataset-cars"`)
	require.Contains(t, got.Body.String(), `"leased":true`)
	require.Contains(t, got.Body.String(), `"free_bytes":90`)

	// An older snapshot does not replace the latest.
	older := strings.Replace(valid, "2026-11-01T11:59:00Z", "2026-11-01T11:00:00Z", 1)
	older = strings.Replace(older, "dataset-cars", "dataset-old", 1)
	res := post(h, "Bearer stager-token", older)
	require.Equal(t, http.StatusOK, res.Code)
	require.JSONEq(t, `{"stored":false}`, res.Body.String())
	require.Contains(t, get(h, true).Body.String(), "dataset-cars")
}

// The latest snapshot survives a restart: a new Service over the same store serves it.
func TestSurvivesRestart(t *testing.T) {
	store := meluxina.KVStore{KV: kvtest.GetStore(t.Context(), t)}
	h := meluxina.Handler{Service: meluxina.NewService(store), Namespaces: []string{"stager"}, Now: func() time.Time { return now },
		Verify: func(context.Context, string, []string) (string, error) { return "stager", nil }}
	require.Equal(t, http.StatusOK, post(h, "Bearer x", valid).Code)
	restarted := h
	restarted.Service = meluxina.NewService(store)
	require.Contains(t, get(restarted, true).Body.String(), "dataset-cars")
}

// A failed run posts an error and no data: the previous good data stays, with the error and the
// failure's time; last_success_at keeps the last good snapshot's time.
func TestFailureKeepsLastGoodData(t *testing.T) {
	h := newHandler(t)
	require.Equal(t, http.StatusOK, post(h, "Bearer stager-token", valid).Code)
	failed := `{"taken_at":"2026-11-01T11:59:30Z","space":{"used_bytes":null,"limit_bytes":null,"free_bytes":null,"used_files":null,"limit_files":null},
	  "staged":[],"retention":"","last_error":"ssh list failed: timeout"}`
	res := post(h, "Bearer stager-token", failed)
	require.Equal(t, http.StatusOK, res.Code, res.Body.String())
	var body struct{ Snapshot meluxina.Snapshot }
	require.NoError(t, json.Unmarshal(get(h, true).Body.Bytes(), &body))
	s := body.Snapshot
	require.Equal(t, "ssh list failed: timeout", s.LastError)
	require.Equal(t, "2026-11-01T11:59:30Z", s.TakenAt.Format(time.RFC3339))
	require.Equal(t, "2026-11-01T11:59:00Z", s.LastSuccessAt.Format(time.RFC3339))
	require.Len(t, s.Staged, 1)
	require.Equal(t, int64(90), *s.Space.FreeBytes)
	require.Equal(t, "least recently used first", s.Retention)

	// The next good snapshot clears the error and moves last_success_at.
	good := strings.Replace(valid, "2026-11-01T11:59:00Z", "2026-11-01T11:59:45Z", 1)
	require.Equal(t, http.StatusOK, post(h, "Bearer stager-token", good).Code)
	body = struct{ Snapshot meluxina.Snapshot }{}
	require.NoError(t, json.Unmarshal(get(h, true).Body.Bytes(), &body))
	require.Empty(t, body.Snapshot.LastError)
	require.Equal(t, "2026-11-01T11:59:45Z", body.Snapshot.LastSuccessAt.Format(time.RFC3339))
}

// With nothing good yet, a failure is kept as it is: no last_success_at, no data.
func TestFailureWithoutPreviousData(t *testing.T) {
	h := newHandler(t)
	require.Equal(t, http.StatusOK, post(h, "Bearer stager-token", `{"taken_at":"2026-11-01T11:59:30Z","space":{},"staged":[],"last_error":"ssh list failed"}`).Code)
	out := get(h, true).Body.String()
	require.Contains(t, out, `"last_success_at":null`)
	require.Contains(t, out, `"staged":[]`)
}

// Null times, counts, space and leased are accepted and served as null; a posted last_success_at
// is ignored.
func TestNullsAccepted(t *testing.T) {
	h := newHandler(t)
	body := `{"taken_at":"2026-11-01T11:59:00Z","last_success_at":"2020-01-01T00:00:00Z",
	  "space":{"used_bytes":5,"limit_bytes":null,"free_bytes":null,"used_files":null,"limit_files":null},
	  "staged":[{"repo":"dataset-cars","commit":"abc123","files":null,"bytes":null,"staged_at":null,"last_used":null,"leased":null}]}`
	require.Equal(t, http.StatusOK, post(h, "Bearer stager-token", body).Code)
	out := get(h, true).Body.String()
	require.Contains(t, out, `"leased":null`)
	require.Contains(t, out, `"limit_bytes":null`)
	require.Contains(t, out, `"last_success_at":"2026-11-01T11:59:00Z"`)
	require.NotContains(t, out, "2020-01-01")
}

func TestPostRejectsInvalid(t *testing.T) {
	h := newHandler(t)
	long := strings.Repeat("x", 5000)
	for name, body := range map[string]string{
		"not json":        `{`,
		"no taken_at":     `{"staged":[]}`,
		"future":          `{"taken_at":"2026-11-01T13:00:00Z"}`,
		"negative space":  `{"taken_at":"2026-11-01T11:00:00Z","space":{"free_bytes":-1}}`,
		"no repo":         `{"taken_at":"2026-11-01T11:00:00Z","staged":[{"commit":"abc123","staged_at":"2026-11-01T10:00:00Z"}]}`,
		"bad commit":      `{"taken_at":"2026-11-01T11:00:00Z","staged":[{"repo":"d","commit":"not hex!","staged_at":"2026-11-01T10:00:00Z"}]}`,
		"negative bytes":  `{"taken_at":"2026-11-01T11:00:00Z","staged":[{"repo":"d","commit":"abc123","bytes":-5,"staged_at":"2026-11-01T10:00:00Z"}]}`,
		"long last_error": `{"taken_at":"2026-11-01T11:00:00Z","last_error":"` + long + `"}`,
		"bad time":        `{"taken_at":"yesterday"}`,
	} {
		rec := post(h, "Bearer stager-token", body)
		require.Equal(t, http.StatusBadRequest, rec.Code, name)
		require.Contains(t, rec.Body.String(), "message", name)
	}
	var many strings.Builder
	many.WriteString(`{"taken_at":"2026-11-01T11:00:00Z","staged":[`)
	for i := range meluxina.MaxStaged + 1 {
		if i > 0 {
			many.WriteString(",")
		}
		many.WriteString(`{"repo":"d","commit":"abc123","staged_at":"2026-11-01T10:00:00Z"}`)
	}
	many.WriteString("]}")
	require.Equal(t, http.StatusBadRequest, post(h, "Bearer stager-token", many.String()).Code)
	require.Equal(t, http.StatusBadRequest, post(h, "Bearer stager-token", strings.Repeat(" ", meluxina.MaxBodyBytes+1)).Code, "body too large")
}
