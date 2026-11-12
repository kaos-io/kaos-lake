// KAOS (kaos-lake, PRD novelcore/kaos#1911): new file, Apache-2.0.

package repos

import (
	"context"
	"crypto/md5" //nolint:gosec // the checksum lakeFS writes for an upload; not a security use
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
	"sync"

	"github.com/treeverse/lakefs/pkg/catalog"
	"github.com/treeverse/lakefs/pkg/graveler"
)

const (
	// KeySeed is the commit metadata that marks the sample commit.
	KeySeed = "kaos.seed"
	// SeedSample is KeySeed's value: the sample's version.
	SeedSample = "sample-v1"
	// seedBranch is the branch the sample is staged and committed on. It is not a run-* branch
	// (those belong to pipelines). Nothing stops a person creating a branch of that name, so one
	// is removed only when it is provably the seed's own (ownSeedBranch).
	seedBranch  = "kaos-seed-sample"
	seedMessage = "kaos: sample data so a new project's default dataset runs as it is"
	// seedCommitter is the identity of the platform's own commits.
	seedCommitter = "kaos-lake"
)

// SeedCatalog is what seedDefault needs of the lakeFS catalog, beyond Catalog.
type SeedCatalog interface {
	Catalog
	ListBranches(ctx context.Context, repositoryID string, prefix string, limit int, after string, opts ...graveler.ListOptionsFunc) ([]*catalog.Branch, bool, error)
	CreateBranch(ctx context.Context, repositoryID string, branch string, sourceBranch string, opts ...graveler.SetOptionsFunc) (*catalog.CommitLog, error)
	DeleteBranch(ctx context.Context, repositoryID string, branch string, opts ...graveler.SetOptionsFunc) error
	ListEntries(ctx context.Context, repositoryID string, reference string, prefix string, after string, delimiter string, limit int) ([]*catalog.DBEntry, bool, error)
	PutObject(ctx context.Context, repositoryID, branch, path string, content []byte, contentType string) error
	Commit(ctx context.Context, repositoryID, branch, message, committer string, metadata catalog.Metadata, date *int64, sourceMetarange *string, allowEmpty bool, opts ...graveler.SetOptionsFunc) (*catalog.CommitLog, error)
	Merge(ctx context.Context, repositoryID string, destinationBranch string, sourceRef string, committer string, message string, metadata catalog.Metadata, strategy string, opts ...graveler.SetOptionsFunc) (string, error)
}

var _ SeedCatalog = (*catalog.Catalog)(nil)

// seed gives the default dataset its sample when the catalog is the real one: the fakes of the
// tests that do not seed do not have the methods.
func seed(ctx context.Context, c Catalog, d Declared, note func(string, ...any)) error {
	if sc, ok := c.(SeedCatalog); ok {
		return seedDefault(ctx, sc, d, note)
	}
	return nil
}

//go:embed sample
var sampleFS embed.FS

// sampleFile is one file of the sample, at its path in the dataset.
type sampleFile struct {
	path        string
	content     []byte
	contentType string
}

// sampleFiles reads the embedded sample: sample/<path> is <path> in the dataset.
func sampleFiles() ([]sampleFile, error) {
	var files []sampleFile
	err := fs.WalkDir(sampleFS, "sample", func(p string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return err
		}
		content, err := sampleFS.ReadFile(p)
		if err != nil {
			return err
		}
		ct := catalog.DefaultContentType
		switch path.Ext(p) { // not the host's mime table
		case ".txt":
			ct = "text/plain; charset=utf-8"
		case ".csv":
			ct = "text/csv"
		}
		files = append(files, sampleFile{path: strings.TrimPrefix(p, "sample/"), content: content, contentType: ct})
		return nil
	})
	return files, err
}

// seedDefault gives the project's default dataset the sample, once (PRD novelcore/kaos#1911: a
// new app made from the starter template runs as it is). It writes only when all of these hold,
// and does nothing, now and on every later pass, otherwise:
//   - d is the declared default dataset and no pipeline produces it;
//   - main is the store's initial commit (no parent), holds no object, committed or staged;
//   - the only branches are main and, from a crashed earlier attempt, seedBranch (and only when
//     it is provably the seed's own: ownSeedBranch).
//
// The sample goes through a branch and a squash merge, so it lands whether or not main is
// protected (a merge is not a direct write) and main gets one commit, the sample's. The merge names main's head as it was read: two attempts at
// once (two replicas) both stage a branch, and only the first merge finds main there; the other
// is refused and leaves nothing on main. The commit that is merged is checked to hold exactly the
// sample before it is, so a branch two attempts wrote into at once never reaches main half done.
func seedDefault(ctx context.Context, c SeedCatalog, d Declared, note func(string, ...any)) (err error) {
	if !d.Default || d.Kind != KindDataset || d.Producer != "" {
		return nil
	}
	md, err := c.GetRepositoryMetadata(ctx, d.Name)
	if err != nil {
		return err
	}
	if md[KeyKind] != KindDataset || md[KeyProducer] != "" {
		return nil
	}
	head, leftover, err := untouched(ctx, c, d.Name)
	if err != nil || head == "" {
		return err
	}
	files, err := sampleFiles()
	if err != nil {
		return err
	}
	if leftover != nil {
		if !ownSeedBranch(ctx, c, d.Name, head, leftover, files) {
			// Someone else's branch: the dataset is not the seed's to fill, now or later.
			if _, logged := seedRefused.LoadOrStore(d.Name, true); !logged {
				note("%s is left without the sample: it has a branch %s that is not the platform's own", d.Name, seedBranch)
			}
			return nil
		}
		if err := c.DeleteBranch(ctx, d.Name, seedBranch); err != nil && !errors.Is(err, graveler.ErrNotFound) {
			return fmt.Errorf("sample: remove the branch %s a crashed attempt left: %w", seedBranch, err)
		}
		note("removed %s of %s, left by an attempt that did not finish", seedBranch, d.Name)
	}
	if _, err := c.CreateBranch(ctx, d.Name, seedBranch, head); err != nil {
		if errors.Is(err, graveler.ErrBranchExists) {
			return nil // another attempt is at it; the next pass sees what it left
		}
		return fmt.Errorf("sample: create branch %s: %w", seedBranch, err)
	}
	// On every path out the branch goes, so that it never outlives the attempt; if it cannot be
	// removed, the next pass finds it and removes it first.
	defer func() {
		if rerr := c.DeleteBranch(context.WithoutCancel(ctx), d.Name, seedBranch); rerr != nil && !errors.Is(rerr, graveler.ErrNotFound) {
			err = errors.Join(err, fmt.Errorf("sample: remove branch %s: %w", seedBranch, rerr))
		}
	}()
	return stageAndMerge(ctx, c, d.Name, head, files, note)
}

// seedRefused is the datasets whose refusal has been logged: once per process, not every pass.
var seedRefused sync.Map

// untouched returns main's head when main still is the initial commit, empty and alone; empty
// when anything else is there. leftover is the branch seedBranch when it exists.
func untouched(ctx context.Context, c SeedCatalog, repo string) (head string, leftover *catalog.Branch, err error) {
	branches, more, err := c.ListBranches(ctx, repo, "", 10, "")
	if err != nil || more {
		return "", nil, err
	}
	hasMain := false
	for _, b := range branches {
		switch b.Name {
		case defaultBranch:
			hasMain = true
		case seedBranch:
			leftover = b
		default:
			return "", nil, nil
		}
	}
	if !hasMain {
		return "", nil, nil
	}
	commit, err := c.GetCommit(ctx, repo, defaultBranch)
	if err != nil || len(commit.Parents) != 0 {
		return "", nil, err
	}
	entries, _, err := c.ListEntries(ctx, repo, defaultBranch, "", "", "", 1)
	if err != nil || len(entries) != 0 {
		return "", nil, err
	}
	return commit.Reference, leftover, nil
}

// ownSeedBranch reports whether the existing branch seedBranch of repo is provably the seed's own,
// left by an attempt that did not finish: it starts at main's initial commit (is it, or one
// commit on it) and everything on it, committed or staged, is a file of the sample with the
// sample's size and checksum. A branch that holds anything else, or that does not start there,
// is someone's.
func ownSeedBranch(ctx context.Context, c SeedCatalog, repo, head string, b *catalog.Branch, files []sampleFile) bool {
	if b.Reference != head {
		commit, err := c.GetCommit(ctx, repo, b.Reference)
		if err != nil || len(commit.Parents) != 1 || commit.Parents[0] != head {
			return false
		}
	}
	entries, more, err := c.ListEntries(ctx, repo, seedBranch, "", "", "", len(files)+1)
	if err != nil || more || len(entries) > len(files) {
		return false
	}
	for _, e := range entries {
		i := slices.IndexFunc(files, func(f sampleFile) bool { return f.path == e.Path })
		if i < 0 {
			return false
		}
		sum := md5.Sum(files[i].content) //nolint:gosec // see import
		if e.Size != int64(len(files[i].content)) || e.Checksum != hex.EncodeToString(sum[:]) {
			return false
		}
	}
	return true
}

func stageAndMerge(ctx context.Context, c SeedCatalog, repo, head string, files []sampleFile, note func(string, ...any)) error {
	for _, f := range files {
		if err := c.PutObject(ctx, repo, seedBranch, f.path, f.content, f.contentType); err != nil {
			return fmt.Errorf("sample: write %s: %w", f.path, err)
		}
	}
	meta := catalog.Metadata{KeySeed: SeedSample}
	commit, err := c.Commit(ctx, repo, seedBranch, seedMessage, seedCommitter, meta, nil, nil, false)
	if err != nil {
		return fmt.Errorf("sample: commit: %w", err)
	}
	if err := holdsOnly(ctx, c, repo, commit.Reference, files); err != nil {
		return fmt.Errorf("sample: not merged: %w", err)
	}
	if _, err := c.Merge(ctx, repo, defaultBranch, commit.Reference, seedCommitter, seedMessage, meta, "",
		graveler.WithExpectedHead(graveler.CommitID(head)), graveler.WithSquashMerge(true)); err != nil {
		return fmt.Errorf("sample: merge into %s: %w", defaultBranch, err)
	}
	note("gave %s the sample: %d files, commit %s", repo, len(files), commit.Reference)
	return nil
}

// holdsOnly checks that the commit ref holds the sample's files and nothing else.
func holdsOnly(ctx context.Context, c SeedCatalog, repo, ref string, files []sampleFile) error {
	entries, more, err := c.ListEntries(ctx, repo, ref, "", "", "", len(files)+1)
	if err != nil {
		return err
	}
	if more || len(entries) != len(files) {
		return fmt.Errorf("%s holds %d entries, the sample has %d", ref, len(entries), len(files))
	}
	for _, f := range files {
		i := slices.IndexFunc(entries, func(e *catalog.DBEntry) bool { return e.Path == f.path })
		if i < 0 || entries[i].Size != int64(len(f.content)) {
			return fmt.Errorf("%s does not hold %s as the sample has it", ref, f.path)
		}
	}
	return nil
}
