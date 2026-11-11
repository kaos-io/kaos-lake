// KAOS (kaos-lake, PRD novelcore/kaos#1283 D-05): new file, Apache-2.0.

package repos

import (
	"regexp"

	"github.com/treeverse/lakefs/pkg/kaos/datasetevents"
)

// VersionTag matches the tag of a released dataset version: v1, v2, ...
var VersionTag = regexp.MustCompile(`^v(\d+)$`)

// KeptTagPrefix starts the tags a producing app sets on the commits its runs keep (PRD
// novelcore/kaos#1911 D-04): k-<run>. They pin a commit and name no version.
const KeptTagPrefix = "k-"

// ReleaseTag reports whether tag, created or deleted on repo, changes a dataset's versions: a `vN`
// tag on a dataset-* repository. Whether the repository is also labelled a dataset is not asked:
// a version is often released before the label (it is set after the first commit, or by the
// reconcile pass), and the label is itself a change (NoteDataset).
func ReleaseTag(repo, tag string) bool {
	return ValidName(repo, DatasetPrefix) && VersionTag.MatchString(tag)
}

// NoteRelease tells the project's ML apps that a version of repo was released or deleted, when it
// was a dataset's (ReleaseTag).
func NoteRelease(repo, tag string) {
	if ReleaseTag(repo, tag) {
		datasetevents.Notify("version " + tag + " of " + repo)
	}
}

// NoteDataset tells the project's ML apps that repo, a dataset-* repository, was labelled a
// dataset or deleted: the dataset joins or leaves the dropdown.
func NoteDataset(repo string) {
	if ValidName(repo, DatasetPrefix) {
		datasetevents.Notify("dataset " + repo)
	}
}
