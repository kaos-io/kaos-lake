// KAOS (kaos-lake, PRD novelcore/kaos#1911): new file, Apache-2.0.

package catalog

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // an entry's checksum, as lakeFS's own upload writes it; not a security use
	"encoding/hex"
	"time"

	"github.com/treeverse/lakefs/pkg/block"
)

// PutObject writes content to the repository's block store and stages it as path on branch, as an
// upload does: for the platform's own small objects, written in process. It does not commit.
func (c *Catalog) PutObject(ctx context.Context, repositoryID, branch, path string, content []byte, contentType string) error {
	repository, err := c.getRepository(ctx, repositoryID)
	if err != nil {
		return err
	}
	address := c.PathProvider.NewPath()
	obj := block.ObjectPointer{
		StorageID:        repository.StorageID.String(),
		StorageNamespace: repository.StorageNamespace.String(),
		IdentifierType:   block.IdentifierTypeRelative,
		Identifier:       address,
	}
	if _, err := c.BlockAdapter.Put(ctx, obj, int64(len(content)), bytes.NewReader(content), block.PutOpts{}); err != nil {
		return err
	}
	sum := md5.Sum(content) //nolint:gosec // see import
	return c.CreateEntry(ctx, repositoryID, branch, DBEntry{
		Path:            path,
		PhysicalAddress: address,
		AddressType:     AddressTypeRelative,
		CreationDate:    time.Now(),
		Size:            int64(len(content)),
		Checksum:        hex.EncodeToString(sum[:]),
		ContentType:     contentType,
	})
}
