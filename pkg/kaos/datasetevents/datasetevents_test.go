// KAOS (kaos-lake, PRD novelcore/kaos#1283 D-05): new file, Apache-2.0.

package datasetevents_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/treeverse/lakefs/pkg/kaos/datasetevents"
)

type countingSubmitter struct {
	calls atomic.Int32
	fail  int32 // fail this many calls first
}

func (s *countingSubmitter) Submit(context.Context) error {
	if n := s.calls.Add(1); n <= s.fail {
		return errors.New("refused")
	}
	return nil
}

func fast(s datasetevents.Submitter) *datasetevents.Notifier {
	return datasetevents.NewNotifier(s, nil).WithTiming(50*time.Millisecond, time.Millisecond)
}

// A burst of releases is one Workflow: five tags in a few milliseconds, one submission.
func TestBurstIsOneSubmission(t *testing.T) {
	s := &countingSubmitter{}
	n := fast(s)
	for i := 0; i < 5; i++ {
		n.Notify("release")
	}
	n.Wait()
	require.EqualValues(t, 1, s.calls.Load())
}

// A change after the window closed is a new event: it must not be swallowed.
func TestChangeAfterTheWindowIsAnotherSubmission(t *testing.T) {
	s := &countingSubmitter{}
	n := fast(s)
	n.Notify("first")
	n.Wait()
	n.Notify("second")
	n.Wait()
	require.EqualValues(t, 2, s.calls.Load())
}

// A refused submission is retried a few times, then given up without a panic: the hourly
// reconcile catches up.
func TestRefusedSubmissionIsRetriedThenGivenUp(t *testing.T) {
	s := &countingSubmitter{fail: 1}
	n := fast(s)
	n.Notify("x")
	n.Wait()
	require.EqualValues(t, 2, s.calls.Load(), "second attempt succeeds")

	s = &countingSubmitter{fail: 100}
	n = fast(s)
	n.Notify("x")
	n.Wait()
	require.EqualValues(t, 3, s.calls.Load(), "bounded attempts")
}

// Without a configured Notifier a change is ignored.
func TestNotifyWithoutConfigurationIsANoop(t *testing.T) {
	datasetevents.Activate(nil)
	datasetevents.Notify("x")
}

func TestConfigureWithoutNamespaceIsOff(t *testing.T) {
	n, err := datasetevents.Configure("", nil)
	require.NoError(t, err)
	require.Nil(t, n)
}

// The fake API server receives exactly one authenticated Workflow create in the CI namespace.
func TestKubeSubmitterCreatesTheWorkflow(t *testing.T) {
	var mu sync.Mutex
	var got []map[string]any
	var path, auth string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var wf map[string]any
		_ = json.Unmarshal(b, &wf)
		mu.Lock()
		got, path, auth = append(got, wf), r.Method+" "+r.URL.Path, r.Header.Get("Authorization")
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	sub := &datasetevents.KubeSubmitter{Namespace: "proj-ci", Server: srv.URL, Client: srv.Client(),
		Token: func() (string, error) { return "sa-token", nil }}
	n := fast(sub)
	for i := 0; i < 5; i++ {
		n.Notify("release")
	}
	n.Wait()

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, got, 1)
	require.Equal(t, "POST /apis/argoproj.io/v1alpha1/namespaces/proj-ci/workflows", path)
	require.Equal(t, "Bearer sa-token", auth)
	meta := got[0]["metadata"].(map[string]any)
	require.Equal(t, "proj-ci", meta["namespace"])
	spec := got[0]["spec"].(map[string]any)
	require.Equal(t, datasetevents.ServiceAccount, spec["serviceAccountName"])
	ref := spec["workflowTemplateRef"].(map[string]any)
	require.Equal(t, datasetevents.Template, ref["name"])
	require.Equal(t, true, ref["clusterScope"])
}

// A refusal names the status but never the token.
func TestKubeSubmitterErrorNeverCarriesTheToken(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer srv.Close()
	sub := &datasetevents.KubeSubmitter{Namespace: "proj-ci", Server: srv.URL, Client: srv.Client(),
		Token: func() (string, error) { return "sa-secret-token", nil }}
	err := sub.Submit(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "403")
	require.NotContains(t, err.Error(), "sa-secret-token")

	sub.Server = "https://127.0.0.1:1"
	err = sub.Submit(context.Background())
	require.Error(t, err)
	require.NotContains(t, err.Error(), "sa-secret-token")
}
