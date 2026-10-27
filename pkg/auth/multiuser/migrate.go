// KAOS (kaos-lake, PRD novelcore/kaos#1217 F-02): new file, Apache-2.0.

package multiuser

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/treeverse/lakefs/pkg/auth"
	"github.com/treeverse/lakefs/pkg/auth/crypt"
	"github.com/treeverse/lakefs/pkg/auth/model"
	"github.com/treeverse/lakefs/pkg/auth/setup"
	"github.com/treeverse/lakefs/pkg/kv"
)

// migratedKey marks, in the multi-user partition, that the store is set up and needs no migration.
var migratedKey = []byte("kaos/basic_auth_migrated")

// MigrateFromBasic moves an installation that ran in basic auth mode to the multi-user store: it
// creates the RBAC base groups and policies, copies the basic admin user with its access keys and
// adds it to Admins. It returns the migrated username, or "" when there was nothing to migrate.
//
// The basic-mode records are left untouched, so switching back to rbac "none" keeps working with
// the same keys. Every step accepts records an interrupted run already wrote, and a marker written
// last makes later starts a no-op.
func MigrateFromBasic(ctx context.Context, store kv.Store, secretStore crypt.SecretStore, svc auth.Service) (string, error) {
	_, err := store.Get(ctx, []byte(model.PartitionKey), migratedKey)
	if err == nil {
		return "", nil
	}
	if !errors.Is(err, kv.ErrNotFound) {
		return "", fmt.Errorf("read migration marker: %w", err)
	}

	user, creds, err := basicAdmin(ctx, store, secretStore)
	if errors.Is(err, kv.ErrNotFound) {
		// No basic admin: this installation was set up in multi-user mode, unless it has no users.
		users, _, err := svc.ListUsers(ctx, &model.PaginationParams{Amount: 1})
		if err != nil {
			return "", fmt.Errorf("list users: %w", err)
		}
		if len(users) == 0 {
			return "", fmt.Errorf("lakeFS is set up but has neither a basic auth admin nor users: %w", auth.ErrMigrationNotPossible)
		}
		return "", markMigrated(ctx, store)
	}
	if err != nil {
		return "", err
	}

	s := ignoreExisting{Service: svc}
	if err := setup.CreateRBACBaseGroups(ctx, s, time.Now()); err != nil {
		return "", fmt.Errorf("create base groups: %w", err)
	}
	if _, err := s.CreateUser(ctx, user); existingOK(err) != nil {
		return "", fmt.Errorf("create user %s: %w", user.Username, err)
	}
	if err := s.AddUserToGroup(ctx, user.Username, setup.AdminsGroup); existingOK(err) != nil {
		return "", fmt.Errorf("add user %s to %s: %w", user.Username, setup.AdminsGroup, err)
	}
	for _, c := range creds {
		if _, err := s.AddCredentials(ctx, user.Username, c.AccessKeyID, c.SecretAccessKey); existingOK(err) != nil {
			return "", fmt.Errorf("add credentials %s for %s: %w", c.AccessKeyID, user.Username, err)
		}
	}
	return user.Username, markMigrated(ctx, store)
}

// basicAdmin reads the single basic-mode user and its decrypted credentials.
func basicAdmin(ctx context.Context, store kv.Store, secretStore crypt.SecretStore) (*model.User, []*model.Credential, error) {
	var userData model.UserData
	if _, err := kv.GetMsg(ctx, store, auth.BasicPartitionKey, model.UserPath(auth.SuperAdminKey), &userData); err != nil {
		return nil, nil, err
	}
	it, err := kv.NewPrimaryIterator(ctx, store, (&model.CredentialData{}).ProtoReflect().Type(), auth.BasicPartitionKey,
		model.CredentialPath(auth.SuperAdminKey, ""), kv.IteratorOptionsAfter([]byte("")))
	if err != nil {
		return nil, nil, fmt.Errorf("list basic auth credentials: %w", err)
	}
	defer it.Close()
	var creds []*model.Credential
	for it.Next() {
		c, err := model.CredentialFromProto(secretStore, it.Entry().Value.(*model.CredentialData))
		if err != nil {
			return nil, nil, fmt.Errorf("decrypt basic auth credentials: %w", err)
		}
		creds = append(creds, c)
	}
	if err := it.Err(); err != nil {
		return nil, nil, fmt.Errorf("list basic auth credentials: %w", err)
	}
	return model.UserFromProto(&userData), creds, nil
}

func markMigrated(ctx context.Context, store kv.Store) error {
	err := store.SetIf(ctx, []byte(model.PartitionKey), migratedKey, []byte(time.Now().UTC().Format(time.RFC3339)), nil)
	if errors.Is(err, kv.ErrPredicateFailed) { // another replica finished first
		return nil
	}
	return err
}

func existingOK(err error) error {
	if errors.Is(err, auth.ErrAlreadyExists) {
		return nil
	}
	return err
}

// ignoreExisting lets setup.CreateRBACBaseGroups complete over a partial earlier run.
type ignoreExisting struct {
	auth.Service
}

func (s ignoreExisting) CreateGroup(ctx context.Context, group *model.Group) (*model.Group, error) {
	g, err := s.Service.CreateGroup(ctx, group)
	return g, existingOK(err)
}

func (s ignoreExisting) WritePolicy(ctx context.Context, policy *model.Policy, update bool) error {
	return existingOK(s.Service.WritePolicy(ctx, policy, update))
}

func (s ignoreExisting) AttachPolicyToGroup(ctx context.Context, policyDisplayName, groupDisplayName string) error {
	return existingOK(s.Service.AttachPolicyToGroup(ctx, policyDisplayName, groupDisplayName))
}
