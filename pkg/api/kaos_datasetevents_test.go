// KAOS (kaos-lake, PRD novelcore/kaos#1283 D-05): new file, Apache-2.0.

package api_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/treeverse/lakefs/pkg/api/apigen"
	"github.com/treeverse/lakefs/pkg/catalog"
	"github.com/treeverse/lakefs/pkg/config"
	"github.com/treeverse/lakefs/pkg/kaos/datasetevents"
	"github.com/treeverse/lakefs/pkg/testutil"
)

type eventCount struct{ n atomic.Int32 }

func (c *eventCount) Submit(context.Context) error { c.n.Add(1); return nil }

// D-05: every API path that changes a dataset's versions or membership tells the project's apps;
// anything else does not.
func TestController_DatasetEvents(t *testing.T) {
	clt, deps := setupClientWithAdmin(t)
	ctx := t.Context()
	c := &eventCount{}
	n := datasetevents.NewNotifier(c, nil).WithTiming(20*time.Millisecond, time.Millisecond)
	datasetevents.Activate(n)
	t.Cleanup(func() { datasetevents.Activate(nil) })

	commits := map[string]string{}
	for _, name := range []string{"dataset-cars", "plain-repo"} {
		_, err := deps.catalog.CreateRepository(ctx, name, config.SingleBlockstoreID, onBlock(deps, name), "main", false)
		testutil.Must(t, err)
		testutil.MustDo(t, "entry", deps.catalog.CreateEntry(ctx, name, "main", catalog.DBEntry{Path: "a", PhysicalAddress: "a", CreationDate: time.Now(), Size: 1, Checksum: "c"}))
		cm, err := deps.catalog.Commit(ctx, name, "main", "m", DefaultUserID, nil, nil, nil, false)
		testutil.Must(t, err)
		commits[name] = cm.Reference
	}
	tag := func(repo, id string) {
		resp, err := clt.CreateTagWithResponse(ctx, repo, apigen.CreateTagJSONRequestBody{Id: id, Ref: commits[repo]})
		verifyResponseOK(t, resp, err)
		n.Wait()
	}
	expect := func(want int32, what string) {
		t.Helper()
		require.Equal(t, want, c.n.Load(), what)
	}

	tag("plain-repo", "v1")
	tag("plain-repo", "snapshot")
	tag("dataset-cars", "k-7f3a")
	expect(0, "a non-dataset repository, and a kept run's k-* tag, trigger nothing")

	tag("dataset-cars", "v1")
	expect(1, "a released version")

	del, err := clt.DeleteTagWithResponse(ctx, "dataset-cars", "v1", &apigen.DeleteTagParams{})
	verifyResponseOK(t, del, err)
	n.Wait()
	expect(2, "a deleted version leaves the dropdown")

	// PRD novelcore/kaos#1911 D-03: a named version joins and leaves the dropdown like a vN.
	c.n.Store(0)
	tag("dataset-cars", "snapshot")
	expect(1, "a named version")
	del, err = clt.DeleteTagWithResponse(ctx, "dataset-cars", "snapshot", &apigen.DeleteTagParams{})
	verifyResponseOK(t, del, err)
	n.Wait()
	expect(2, "a deleted named version leaves the dropdown")
	del, err = clt.DeleteTagWithResponse(ctx, "dataset-cars", "k-7f3a", &apigen.DeleteTagParams{})
	verifyResponseOK(t, del, err)
	n.Wait()
	expect(2, "a deleted k-* tag triggers nothing")

	set, err := clt.SetRepositoryMetadataWithResponse(ctx, "dataset-cars", apigen.SetRepositoryMetadataJSONRequestBody{
		Metadata: apigen.RepositoryMetadataSet_Metadata{AdditionalProperties: map[string]string{"kaos.description": "cars"}},
	})
	verifyResponseOK(t, set, err)
	n.Wait()
	expect(2, "a description is not a dataset change")

	set, err = clt.SetRepositoryMetadataWithResponse(ctx, "dataset-cars", apigen.SetRepositoryMetadataJSONRequestBody{
		Metadata: apigen.RepositoryMetadataSet_Metadata{AdditionalProperties: map[string]string{"kaos.kind": "dataset"}},
	})
	verifyResponseOK(t, set, err)
	n.Wait()
	expect(3, "labelling a repository a dataset")

	rm, err := clt.DeleteRepositoryWithResponse(ctx, "dataset-cars", &apigen.DeleteRepositoryParams{})
	verifyResponseOK(t, rm, err)
	n.Wait()
	expect(4, "a deleted dataset leaves the dropdown")

	rm, err = clt.DeleteRepositoryWithResponse(ctx, "plain-repo", &apigen.DeleteRepositoryParams{})
	verifyResponseOK(t, rm, err)
	n.Wait()
	expect(4, "deleting a non-dataset repository triggers nothing")
}
