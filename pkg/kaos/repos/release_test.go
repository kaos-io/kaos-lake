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
		{"dataset-cars", "latest", false},     // not a version
		{"dataset-cars", "v1.0", false},       // not vN
		{"dataset-cars", "release-v1", false}, // not vN
		{"dataset-cars", "V1", false},
		{"outputs-app", "v1", false}, // an outputs repository, not a dataset
		{"myrepo", "v1", false},      // not a dataset-* repository
		{"dataset-ab", "v1", false},  // not a valid dataset name
	} {
		require.Equal(t, tc.want, repos.ReleaseTag(tc.repo, tc.tag), "%s %s", tc.repo, tc.tag)
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
	repos.NoteRelease("dataset-cars", "latest")
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
}
