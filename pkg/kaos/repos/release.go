// KAOS (kaos-lake, PRD novelcore/kaos#1283 D-05): new file, Apache-2.0.

package repos

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/treeverse/lakefs/pkg/kaos/datasetevents"
)

// VersionTag matches the tag of a released dataset version: v1, v2, ...
var VersionTag = regexp.MustCompile(`^v(\d+)$`)

// KeptTagPrefix starts the tags a producing app sets on the commits its runs keep (PRD
// novelcore/kaos#1911 D-04): k-<run>. They pin a commit and name no version.
const KeptTagPrefix = "k-"

// VersionName reports whether tag names a version of a dataset: a release (vN) or any other name
// (PRD novelcore/kaos#1911 D-03), never the k-* tag of a kept run.
func VersionName(tag string) bool {
	return !strings.HasPrefix(tag, KeptTagPrefix)
}

// ReleaseTag reports whether tag, created or deleted on repo, changes a dataset's versions: a
// version name (VersionName) on a dataset-* repository. Whether the repository is also labelled a dataset is not asked:
// a version is often released before the label (it is set after the first commit, or by the
// reconcile pass), and the label is itself a change (NoteDataset).
func ReleaseTag(repo, tag string) bool {
	return ValidName(repo, DatasetPrefix) && VersionName(tag)
}

// ErrNumberedVersion refuses a vN tag on a dataset a pipeline produces.
var ErrNumberedVersion = errors.New("a dataset a pipeline produces has commits, latest and names; vN is for datasets people upload")

// ValidateTag checks a tag a caller creates on a repository labelled md. A dataset a pipeline
// produces takes no numbered release (PRD novelcore/kaos#1911 D-03).
func ValidateTag(md map[string]string, tag string) error {
	if md[KeyProducer] != "" && VersionTag.MatchString(tag) {
		return fmt.Errorf("%w: %s is produced by %s, name the version instead", ErrNumberedVersion, tag, md[KeyProducer])
	}
	return nil
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
