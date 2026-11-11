// KAOS (kaos-lake, PRD novelcore/kaos#1911 F-12): new file, Apache-2.0.

package api_test

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-openapi/swag"
	"github.com/stretchr/testify/require"
	"github.com/treeverse/lakefs/pkg/api/apigen"
	"github.com/treeverse/lakefs/pkg/api/apiutil"
	"github.com/treeverse/lakefs/pkg/catalog"
	"github.com/treeverse/lakefs/pkg/config"
	"github.com/treeverse/lakefs/pkg/graveler"
)

// runBranch gives repo a branch off main's current head with one committed file of its own, as a
// run that stages a dataset version does.
func runBranch(t *testing.T, deps *dependencies, repo, branch string) {
	t.Helper()
	ctx := t.Context()
	_, err := deps.catalog.CreateBranch(ctx, repo, branch, "main")
	require.NoError(t, err)
	require.NoError(t, deps.catalog.CreateEntry(ctx, repo, branch, catalog.DBEntry{
		Path: branch + ".txt", PhysicalAddress: branch + "-addr", CreationDate: time.Now(), Size: 1, Checksum: branch,
	}))
	_, err = deps.catalog.Commit(ctx, repo, branch, "stage "+branch, DefaultUserID, nil, nil, nil, false)
	require.NoError(t, err)
}

func mainHead(t *testing.T, deps *dependencies, repo string) string {
	t.Helper()
	head, err := deps.catalog.GetBranchReference(t.Context(), repo, "main")
	require.NoError(t, err)
	return head
}

// PRD novelcore/kaos#1911 D-05: a merge that names the head it expects lands only on that head.
func TestController_MergeExpectedHead(t *testing.T) {
	clt, deps := setupClientWithAdmin(t)
	ctx := t.Context()
	repo := testUniqueRepoName()
	_, err := deps.catalog.CreateRepository(ctx, repo, config.SingleBlockstoreID, onBlock(deps, repo), "main", false)
	require.NoError(t, err)
	runBranch(t, deps, repo, "run-a")
	runBranch(t, deps, repo, "run-b")
	start := mainHead(t, deps, repo)
	merge := func(source string, expected *string) *apigen.MergeIntoBranchResponse {
		resp, err := clt.MergeIntoBranchWithResponse(ctx, repo, source, "main", apigen.MergeIntoBranchJSONRequestBody{ExpectedHead: expected})
		require.NoError(t, err)
		return resp
	}

	// Not main's head: refused, saying what main is at, and main does not move. A branch name
	// or a short id is not a head either.
	for _, wrong := range []string{strings.Repeat("0", 64), "main", start[:12]} {
		resp := merge("run-a", &wrong)
		require.Equal(t, http.StatusPreconditionFailed, resp.StatusCode(), wrong)
		require.NotNil(t, resp.JSON412)
		require.Contains(t, resp.JSON412.Message, start, "the refusal names the head to restage from")
		require.Equal(t, start, mainHead(t, deps, repo))
	}

	// The head it is: lands.
	resp := merge("run-a", &start)
	require.Equal(t, http.StatusOK, resp.StatusCode(), string(resp.Body))
	moved := mainHead(t, deps, repo)
	require.Equal(t, resp.JSON200.Reference, moved)

	// The second run staged from the old head: refused, and nothing of it reaches main.
	resp = merge("run-b", &start)
	require.Equal(t, http.StatusPreconditionFailed, resp.StatusCode())
	require.Contains(t, resp.JSON412.Message, moved)
	require.Equal(t, moved, mainHead(t, deps, repo))
	_, err = deps.catalog.GetEntry(ctx, repo, "main", "run-b.txt", catalog.GetEntryParams{})
	require.ErrorIs(t, err, graveler.ErrNotFound)

	// No expected head, or an empty one: an ordinary merge.
	require.Equal(t, http.StatusOK, merge("run-b", apiutil.Ptr("")).StatusCode())
}

// meetInPreMerge holds the first two merges inside the branch update, after the expected head
// was compared and before the branch is written, until both are there.
type meetInPreMerge struct {
	graveler.HooksNoOp
	mu      sync.Mutex
	arrived int
	both    chan struct{}
}

func (h *meetInPreMerge) PreMergeHook(ctx context.Context, _ graveler.HookRecord) error {
	h.mu.Lock()
	h.arrived++
	n := h.arrived
	if n == 2 {
		close(h.both)
	}
	h.mu.Unlock()
	if n <= 2 {
		select {
		case <-h.both:
		case <-ctx.Done():
		case <-time.After(10 * time.Second): // the test fails on arrived, not by hanging
		}
	}
	return nil
}

// PRD novelcore/kaos#1911 D-05, CON-03, AC-08: two promotions race with the same expected head,
// and both have passed the comparison before either writes the branch. Exactly one lands; the other
// is answered 412 and leaves nothing on main. A comparison made before the atomic branch update
// (read, then merge) would let both land here.
func TestController_MergeExpectedHead_TwoRace(t *testing.T) {
	clt, deps := setupClientWithAdmin(t)
	ctx := t.Context()
	repo := testUniqueRepoName()
	_, err := deps.catalog.CreateRepository(ctx, repo, config.SingleBlockstoreID, onBlock(deps, repo), "main", false)
	require.NoError(t, err)
	runBranch(t, deps, repo, "run-a")
	runBranch(t, deps, repo, "run-b")
	start := mainHead(t, deps, repo)

	hooks := &meetInPreMerge{both: make(chan struct{})}
	deps.catalog.SetHooksHandler(hooks)

	runs := []string{"run-a", "run-b"}
	resps := make([]*apigen.MergeIntoBranchResponse, len(runs))
	var wg sync.WaitGroup
	for i, run := range runs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := clt.MergeIntoBranchWithResponse(ctx, repo, run, "main", apigen.MergeIntoBranchJSONRequestBody{ExpectedHead: &start})
			if err == nil {
				resps[i] = resp
			}
		}()
	}
	wg.Wait()

	require.Equal(t, 2, hooks.arrived, "both merges were inside the branch update with the same head; the loser's retry stopped at the comparison")
	require.NotNil(t, resps[0])
	require.NotNil(t, resps[1])
	codes := []int{resps[0].StatusCode(), resps[1].StatusCode()}
	slices.Sort(codes)
	require.Equal(t, []int{http.StatusOK, http.StatusPreconditionFailed}, codes, "exactly one promotion wins; the other gets 412")

	won, lost := 0, 1
	if resps[0].StatusCode() != http.StatusOK {
		won, lost = 1, 0
	}
	head := mainHead(t, deps, repo)
	require.Equal(t, resps[won].JSON200.Reference, head)
	require.Contains(t, resps[lost].JSON412.Message, head, "the loser is told the head to restage from")
	_, err = deps.catalog.GetEntry(ctx, repo, "main", runs[won]+".txt", catalog.GetEntryParams{})
	require.NoError(t, err)
	_, err = deps.catalog.GetEntry(ctx, repo, "main", runs[lost]+".txt", catalog.GetEntryParams{})
	require.ErrorIs(t, err, graveler.ErrNotFound, "the two runs are not mixed")
}

// PRD novelcore/kaos#1911 CON-03: main of a dataset a pipeline produces takes a merge only with
// the head it expects, so no caller can mix two runs by leaving the field out. The first promotion
// names the initial commit. Other branches and other repositories merge as before.
func TestController_MergeIntoProducedDatasetNeedsExpectedHead(t *testing.T) {
	clt, deps := setupClientWithAdmin(t)
	ctx := t.Context()
	const produced, uploaded = "dataset-clean", "dataset-cars"
	for _, repo := range []string{produced, uploaded} {
		_, err := deps.catalog.CreateRepository(ctx, repo, config.SingleBlockstoreID, onBlock(deps, repo), "main", false)
		require.NoError(t, err)
		runBranch(t, deps, repo, "run-a")
		runBranch(t, deps, repo, "run-b")
	}
	// The labels the repositories reconcile sets; the API refuses kaos.producer to every caller.
	require.NoError(t, deps.catalog.UpdateRepositoryMetadata(ctx, produced, map[string]string{"kaos.kind": "dataset", "kaos.producer": "yolo"}))
	require.NoError(t, deps.catalog.UpdateRepositoryMetadata(ctx, uploaded, map[string]string{"kaos.kind": "dataset"}))
	merge := func(repo, source, dest string, body apigen.MergeIntoBranchJSONRequestBody) *apigen.MergeIntoBranchResponse {
		resp, err := clt.MergeIntoBranchWithResponse(ctx, repo, source, dest, body)
		require.NoError(t, err)
		return resp
	}
	fresh := mainHead(t, deps, produced) // the initial commit: a fresh dataset's main has a head too

	for name, body := range map[string]apigen.MergeIntoBranchJSONRequestBody{
		"no field": {}, "empty": {ExpectedHead: apiutil.Ptr("")},
	} {
		resp := merge(produced, "run-a", "main", body)
		require.Equal(t, http.StatusBadRequest, resp.StatusCode(), name)
		require.Contains(t, resp.JSON400.Message, "expected_head", name)
		require.Equal(t, fresh, mainHead(t, deps, produced), name)
	}

	resp := merge(produced, "run-a", "main", apigen.MergeIntoBranchJSONRequestBody{ExpectedHead: &fresh})
	require.Equal(t, http.StatusOK, resp.StatusCode(), string(resp.Body))
	resp = merge(produced, "run-b", "main", apigen.MergeIntoBranchJSONRequestBody{ExpectedHead: &fresh})
	require.Equal(t, http.StatusPreconditionFailed, resp.StatusCode(), "a stale head")

	// Elsewhere nothing changed: another branch of the produced dataset, and a dataset people upload.
	require.Equal(t, http.StatusOK, merge(produced, "main", "run-b", apigen.MergeIntoBranchJSONRequestBody{}).StatusCode())
	require.Equal(t, http.StatusOK, merge(uploaded, "run-a", "main", apigen.MergeIntoBranchJSONRequestBody{}).StatusCode())
}

// PRD novelcore/kaos#1911 CON-03: a pull request carries no expected head, so it never merges
// into main of a dataset a pipeline produces; otherwise a stale run refused as a plain merge would
// land as a pull request and main would hold two runs. Pull requests elsewhere merge as before.
func TestController_MergePullRequestIntoProducedDatasetRefused(t *testing.T) {
	clt, deps := setupClientWithAdmin(t)
	ctx := t.Context()
	const produced, uploaded = "dataset-clean", "dataset-cars"
	for _, repo := range []string{produced, uploaded} {
		_, err := deps.catalog.CreateRepository(ctx, repo, config.SingleBlockstoreID, onBlock(deps, repo), "main", false)
		require.NoError(t, err)
		runBranch(t, deps, repo, "run-a")
		runBranch(t, deps, repo, "run-b")
	}
	require.NoError(t, deps.catalog.UpdateRepositoryMetadata(ctx, produced, map[string]string{"kaos.kind": "dataset", "kaos.producer": "yolo"}))
	start := mainHead(t, deps, produced)
	pullRequest := func(repo, source, dest string) string {
		resp, err := clt.CreatePullRequestWithResponse(ctx, repo, apigen.CreatePullRequestJSONRequestBody{
			Title: source, SourceBranch: source, DestinationBranch: dest,
		})
		require.NoError(t, err)
		require.NotNil(t, resp.JSON201, string(resp.Body))
		return resp.JSON201.Id
	}

	// run-a is promoted with the head it expects.
	merge, err := clt.MergeIntoBranchWithResponse(ctx, produced, "run-a", "main", apigen.MergeIntoBranchJSONRequestBody{ExpectedHead: &start})
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, merge.StatusCode(), string(merge.Body))
	promoted := mainHead(t, deps, produced)

	// run-b, staged from the old head: refused as a plain merge, and as a pull request.
	merge, err = clt.MergeIntoBranchWithResponse(ctx, produced, "run-b", "main", apigen.MergeIntoBranchJSONRequestBody{})
	require.NoError(t, err)
	require.Equal(t, http.StatusBadRequest, merge.StatusCode())
	id := pullRequest(produced, "run-b", "main")
	pr, err := clt.MergePullRequestWithResponse(ctx, produced, id)
	require.NoError(t, err)
	require.Equal(t, http.StatusBadRequest, pr.StatusCode(), string(pr.Body))
	require.Contains(t, pr.JSON400.Message, "expected_head")
	require.Equal(t, promoted, mainHead(t, deps, produced), "main did not move")
	_, err = deps.catalog.GetEntry(ctx, produced, "main", "run-b.txt", catalog.GetEntryParams{})
	require.ErrorIs(t, err, graveler.ErrNotFound, "the two runs are not mixed")
	got, err := clt.GetPullRequestWithResponse(ctx, produced, id)
	require.NoError(t, err)
	require.Equal(t, "open", swag.StringValue(got.JSON200.Status), "the refused pull request stays open")

	// Into another branch of the produced dataset, and into an uploaded dataset's main: as before.
	pr, err = clt.MergePullRequestWithResponse(ctx, produced, pullRequest(produced, "main", "run-b"))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, pr.StatusCode(), string(pr.Body))
	pr, err = clt.MergePullRequestWithResponse(ctx, uploaded, pullRequest(uploaded, "run-a", "main"))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, pr.StatusCode(), string(pr.Body))
}

// A produced dataset's main that holds uncommitted objects takes no promotion (lakeFS refuses to
// merge into a dirty branch). No run stages on main, so the refusal says what it is and who can
// clear it, instead of the bare "dirty branch" other repositories get.
func TestController_MergeIntoDirtyProducedMainSaysWhy(t *testing.T) {
	clt, deps := setupClientWithAdmin(t)
	ctx := t.Context()
	const produced, uploaded = "dataset-clean", "dataset-cars"
	for _, repo := range []string{produced, uploaded} {
		_, err := deps.catalog.CreateRepository(ctx, repo, config.SingleBlockstoreID, onBlock(deps, repo), "main", false)
		require.NoError(t, err)
		runBranch(t, deps, repo, "run-a")
		require.NoError(t, deps.catalog.CreateEntry(ctx, repo, "main", catalog.DBEntry{
			Path: "stray.jpg", PhysicalAddress: "stray-addr", CreationDate: time.Now(), Size: 1, Checksum: "stray",
		}))
	}
	require.NoError(t, deps.catalog.UpdateRepositoryMetadata(ctx, produced, map[string]string{"kaos.kind": "dataset", "kaos.producer": "yolo"}))

	head := mainHead(t, deps, produced)
	resp, err := clt.MergeIntoBranchWithResponse(ctx, produced, "run-a", "main", apigen.MergeIntoBranchJSONRequestBody{ExpectedHead: &head})
	require.NoError(t, err)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode(), string(resp.Body))
	require.Contains(t, resp.JSON400.Message, "main of dataset-clean, a dataset produced by yolo, holds uncommitted objects that no run wrote")
	require.Contains(t, resp.JSON400.Message, "an admin must reset main")
	require.Equal(t, head, mainHead(t, deps, produced))

	// A dataset people upload keeps lakeFS's own answer.
	resp, err = clt.MergeIntoBranchWithResponse(ctx, uploaded, "run-a", "main", apigen.MergeIntoBranchJSONRequestBody{})
	require.NoError(t, err)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode())
	require.Equal(t, graveler.ErrDirtyBranch.Error(), resp.JSON400.Message)
}
