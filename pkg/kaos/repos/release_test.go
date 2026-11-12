// KAOS (kaos-lake, PRD novelcore/kaos#1283 D-05): new file, Apache-2.0.

package repos_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/treeverse/lakefs/pkg/kaos/datasetevents"
	"github.com/treeverse/lakefs/pkg/kaos/repos"
)

func TestReleaseTag(t *testing.T) {
	for _, tc := range []struct {
		repo, tag string
		want      bool
	}{
		{"dataset-cars", "v1", true},
		{"dataset-cars", "v12", true},
		{"dataset-default", "v2", true},
		// PRD novelcore/kaos#1911 D-03: a named version is a version too; a kept run's tag is not.
		{"dataset-cars", "golden", true},
		{"dataset-cars", "v1.0", true},
		{"dataset-cars", "release-v1", true},
		{"dataset-cars", "k-7f3a", false},
		{"dataset-cars", "k-", false},
		{"outputs-app", "golden", false},
		{"outputs-app", "v1", false}, // an outputs repository, not a dataset
		{"myrepo", "v1", false},      // not a dataset-* repository
		{"dataset-ab", "v1", false},  // not a valid dataset name
	} {
		require.Equal(t, tc.want, repos.ReleaseTag(tc.repo, tc.tag), "%s %s", tc.repo, tc.tag)
	}
}

// PRD novelcore/kaos#1911 D-03: on a produced dataset a tag is refused when it is vN, latest, main
// or made only of hexadecimal characters (lakeFS resolves a tag before a commit prefix); a
// dataset people upload takes any.
func TestValidateTag(t *testing.T) {
	produced := map[string]string{repos.KeyProducer: "yolo"}
	for _, id := range []string{"v1", "v12"} {
		require.ErrorIs(t, repos.ValidateTag("dataset-clean", produced, id), repos.ErrNumberedVersion, id)
	}
	for _, id := range []string{"latest", "main", "abcd", "ABCD", "0123456789ab", "deadbeef", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"} {
		require.ErrorIs(t, repos.ValidateTag("dataset-clean", produced, id), repos.ErrReservedTagName, id)
	}
	for _, id := range []string{"golden", "abc", "beef-1", "g00d", "latest-2", "main2", "v1.0", "k-7f3a", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef0"} {
		require.NoError(t, repos.ValidateTag("dataset-clean", produced, id), id)
	}
	for _, id := range []string{"v1", "latest", "main", "deadbeef"} {
		require.NoError(t, repos.ValidateTag("dataset-cars", map[string]string{repos.KeyKind: "dataset"}, id), id)
	}
}

type count struct{ n atomic.Int32 }

func (c *count) Submit(context.Context) error { c.n.Add(1); return nil }

// NoteRelease and NoteDataset reach the notifier only for datasets and versions.
func TestNotesReachTheNotifierOnlyForDatasets(t *testing.T) {
	c := &count{}
	n := datasetevents.NewNotifier(c, nil).WithTiming(20*time.Millisecond, time.Millisecond)
	datasetevents.Activate(n)
	t.Cleanup(func() { datasetevents.Activate(nil) })

	repos.NoteRelease("myrepo", "v1")
	repos.NoteRelease("outputs-app", "v1")
	repos.NoteRelease("dataset-cars", "k-7f3a") // a kept run: every run sets one
	repos.NoteDataset("outputs-app")
	repos.NoteDataset("myrepo")
	n.Wait()
	require.EqualValues(t, 0, c.n.Load(), "nothing that is not a dataset or a version triggers")

	repos.NoteRelease("dataset-cars", "v2")
	n.Wait()
	require.EqualValues(t, 1, c.n.Load())

	repos.NoteDataset("dataset-cars") // labelled, or deleted
	n.Wait()
	require.EqualValues(t, 2, c.n.Load())

	repos.NoteRelease("dataset-cars", "golden") // a named version reaches the pickers at once
	n.Wait()
	require.EqualValues(t, 3, c.n.Load())
}
