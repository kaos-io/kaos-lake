// KAOS (kaos-lake, PRD novelcore/kaos#1217 T-07): new file, Apache-2.0.

// Package rekey moves stored access-key secrets from a previous auth.encrypt.secret_key to the
// current one, so an installation can leave a known key (such as the Helm chart's built-in
// default) without reissuing any access key.
package rekey

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/treeverse/lakefs/pkg/auth"
	"github.com/treeverse/lakefs/pkg/auth/crypt"
	"github.com/treeverse/lakefs/pkg/auth/model"
	"github.com/treeverse/lakefs/pkg/config"
	"github.com/treeverse/lakefs/pkg/kv"
)

// credentialsPrefix is the key prefix of model.CredentialPath ("uCredentials/<user>/credentials/<id>").
var credentialsPrefix = []byte("uCredentials/")

// partitions hold credentials: the multi-user store and basic auth mode.
var partitions = []string{model.PartitionKey, auth.BasicPartitionKey}

var ErrUndecryptable = errors.New("stored secret opens with neither the current nor the previous encryption key")

// FromConfig re-encrypts with auth.encrypt.secret_key every secret that only
// auth.encrypt.previous_secret_key opens. It does nothing when no previous key is set.
func FromConfig(ctx context.Context, cfg config.Config, store kv.Store) (int, error) {
	enc := cfg.AuthConfig().GetBaseAuthConfig().Encrypt
	if enc.PreviousSecretKey == "" || enc.PreviousSecretKey == enc.SecretKey {
		return 0, nil
	}
	return Reencrypt(ctx, store, crypt.NewSecretStore([]byte(enc.SecretKey)), crypt.NewSecretStore([]byte(enc.PreviousSecretKey)))
}

// Reencrypt rewrites, under current, every stored access-key secret that current cannot open but
// previous can, and returns how many it rewrote. Secrets current already opens are left alone, so
// later runs do nothing. A secret neither key opens is an error: starting would leave that access
// key unusable, and there is no key to fall back to.
func Reencrypt(ctx context.Context, store kv.Store, current, previous crypt.SecretStore) (int, error) {
	n := 0
	for _, partition := range partitions {
		keys, err := credentialKeys(ctx, store, partition)
		if err != nil {
			return n, err
		}
		for _, key := range keys {
			done, err := reencrypt(ctx, store, partition, key, current, previous)
			if err != nil {
				return n, err
			}
			if done {
				n++
			}
		}
	}
	return n, nil
}

func credentialKeys(ctx context.Context, store kv.Store, partition string) ([][]byte, error) {
	it, err := kv.ScanPrefix(ctx, store, []byte(partition), credentialsPrefix, nil)
	if err != nil {
		return nil, fmt.Errorf("scan %s credentials: %w", partition, err)
	}
	defer it.Close()
	var keys [][]byte
	for it.Next() {
		keys = append(keys, bytes.Clone(it.Entry().Key))
	}
	if err := it.Err(); err != nil {
		return nil, fmt.Errorf("scan %s credentials: %w", partition, err)
	}
	return keys, nil
}

func reencrypt(ctx context.Context, store kv.Store, partition string, key []byte, current, previous crypt.SecretStore) (bool, error) {
	var c model.CredentialData
	pred, err := kv.GetMsg(ctx, store, partition, key, &c)
	if errors.Is(err, kv.ErrNotFound) { // deleted since the scan
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read %s %s: %w", partition, key, err)
	}
	if _, err := current.Decrypt(c.GetSecretAccessKeyEncryptedBytes()); err == nil {
		return false, nil
	}
	plain, err := previous.Decrypt(c.GetSecretAccessKeyEncryptedBytes())
	if err != nil {
		return false, fmt.Errorf("%s %s: %w", partition, key, ErrUndecryptable)
	}
	c.SecretAccessKeyEncryptedBytes, err = current.Encrypt(plain)
	if err != nil {
		return false, fmt.Errorf("encrypt %s %s: %w", partition, key, err)
	}
	err = kv.SetMsgIf(ctx, store, partition, key, &c, pred)
	if errors.Is(err, kv.ErrPredicateFailed) { // changed since the read, e.g. by another replica
		return reencrypt(ctx, store, partition, key, current, previous)
	}
	if err != nil {
		return false, fmt.Errorf("write %s %s: %w", partition, key, err)
	}
	return true, nil
}
