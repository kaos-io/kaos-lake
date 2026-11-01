// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-10, D-08): new file, Apache-2.0.

// Package meluxina keeps the latest snapshot of what the project has staged on MeluXina's Lustre.
// The stager's per-project job posts it with its service-account token; the web UI reads it.
// kaos-lake never talks to MeluXina itself and holds no MeluXina credentials.
package meluxina

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/treeverse/lakefs/pkg/auth"
	"github.com/treeverse/lakefs/pkg/kv"
)

const (
	// MaxBodyBytes bounds a posted snapshot.
	MaxBodyBytes = 2 << 20
	// MaxStaged bounds the staged versions of a snapshot.
	MaxStaged = 5000
	// maxText bounds each free-text field; maxLongText the retention rule and the last error.
	maxText     = 512
	maxLongText = 4096
	// maxFuture is how far ahead of the server's clock a snapshot's taken_at may be.
	maxFuture = 5 * time.Minute

	partition   = "kaos"
	snapshotKey = "meluxina/snapshot"
)

// ErrInvalid marks a snapshot that fails validation; its message says which rule.
var ErrInvalid = errors.New("invalid MeluXina snapshot")

var commitID = regexp.MustCompile(`^[0-9a-fA-F]{6,64}$`)

// Space is the project's quota on MeluXina's Lustre. A value is null when the quota is unlimited
// or could not be read.
type Space struct {
	UsedBytes  *int64 `json:"used_bytes"`
	LimitBytes *int64 `json:"limit_bytes"`
	FreeBytes  *int64 `json:"free_bytes"`
	UsedFiles  *int64 `json:"used_files"`
	LimitFiles *int64 `json:"limit_files"`
}

func (sp Space) values() []*int64 {
	return []*int64{sp.UsedBytes, sp.LimitBytes, sp.FreeBytes, sp.UsedFiles, sp.LimitFiles}
}

// Staged is one staged version: a commit of a repository. Times and counts may be null.
type Staged struct {
	Repo     string    `json:"repo"`
	Commit   string    `json:"commit"`
	Ref      string    `json:"ref,omitempty"`
	Files    *int64    `json:"files"`
	Bytes    *int64    `json:"bytes"`
	StagedAt time.Time `json:"staged_at,omitzero"`
	LastUsed time.Time `json:"last_used,omitzero"`
	// Leased: a queued or running job needs this version, so it is never evicted. Null: unknown
	// (the queue could not be read).
	Leased *bool `json:"leased"`
}

// Snapshot is what the stager reports at one moment, as kept and served.
//
// A failed run of the stager's job posts only taken_at, an all-null space, no staged versions and
// last_error. Put then keeps the previous good staged/space/retention and records just
// last_error and taken_at; LastSuccessAt stays at the last good snapshot (server-set; a posted
// value is ignored).
type Snapshot struct {
	TakenAt       time.Time  `json:"taken_at"`
	LastSuccessAt *time.Time `json:"last_success_at"`
	Space         Space      `json:"space"`
	Staged        []Staged   `json:"staged"`
	Retention     string     `json:"retention,omitempty"`
	LastError     string     `json:"last_error,omitempty"`
}

// IsFailure reports whether s is a failed run's report: an error and no data.
func (s *Snapshot) IsFailure() bool {
	if s.LastError == "" || len(s.Staged) > 0 || s.Retention != "" {
		return false
	}
	for _, v := range s.Space.values() {
		if v != nil {
			return false
		}
	}
	return true
}

// Validate checks the size and shape of s against now. The error wraps ErrInvalid.
func (s *Snapshot) Validate(now time.Time) error {
	bad := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
	}
	switch {
	case s.TakenAt.IsZero():
		return bad("taken_at is required (RFC3339)")
	case s.TakenAt.After(now.Add(maxFuture)):
		return bad("taken_at %s is in the future", s.TakenAt.Format(time.RFC3339))
	case slices.ContainsFunc(s.Space.values(), func(v *int64) bool { return v != nil && *v < 0 }):
		return bad("space values cannot be negative")
	case len(s.Staged) > MaxStaged:
		return bad("at most %d staged versions, got %d", MaxStaged, len(s.Staged))
	case len(s.Retention) > maxLongText || len(s.LastError) > maxLongText:
		return bad("retention and last_error are at most %d characters", maxLongText)
	}
	for i, v := range s.Staged {
		switch {
		case v.Repo == "" || len(v.Repo) > maxText:
			return bad("staged[%d].repo is required, at most %d characters", i, maxText)
		case !commitID.MatchString(v.Commit):
			return bad("staged[%d].commit must be a commit id (hexadecimal)", i)
		case len(v.Ref) > maxText:
			return bad("staged[%d].ref is at most %d characters", i, maxText)
		case (v.Files != nil && *v.Files < 0) || (v.Bytes != nil && *v.Bytes < 0):
			return bad("staged[%d]: files and bytes cannot be negative", i)
		}
	}
	return nil
}

// Store keeps one snapshot value across restarts.
type Store interface {
	Get(ctx context.Context) ([]byte, error) // kv.ErrNotFound when nothing is stored
	Set(ctx context.Context, value []byte) error
}

// KVStore is a Store in lakeFS's own key-value database.
type KVStore struct{ KV kv.Store }

// Get returns the stored snapshot.
func (k KVStore) Get(ctx context.Context) ([]byte, error) {
	res, err := k.KV.Get(ctx, []byte(partition), []byte(snapshotKey))
	if err != nil {
		return nil, err
	}
	return res.Value, nil
}

// Set replaces the stored snapshot.
func (k KVStore) Set(ctx context.Context, value []byte) error {
	return k.KV.Set(ctx, []byte(partition), []byte(snapshotKey), value)
}

// Service keeps the latest snapshot in memory and in its Store.
type Service struct {
	store Store
	mu    sync.Mutex
	last  *Snapshot
}

// NewService returns a Service over store.
func NewService(store Store) *Service { return &Service{store: store} }

// Put keeps s when it is newer than the stored snapshot, and reports whether it did.
func (s *Service) Put(ctx context.Context, snap *Snapshot) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, err := s.load(ctx)
	if err != nil {
		return false, err
	}
	if cur != nil && snap.TakenAt.Before(cur.TakenAt) {
		return false, nil
	}
	if snap.IsFailure() {
		// Keep the last good data; record only the failure and when it happened.
		kept := Snapshot{}
		if cur != nil {
			kept = *cur
		}
		kept.TakenAt, kept.LastError = snap.TakenAt, snap.LastError
		if kept.Staged == nil {
			kept.Staged = []Staged{}
		}
		snap = &kept
	} else {
		ok := snap.TakenAt
		snap.LastSuccessAt = &ok
	}
	data, err := json.Marshal(snap)
	if err != nil {
		return false, err
	}
	if err := s.store.Set(ctx, data); err != nil {
		return false, fmt.Errorf("store snapshot: %w", err)
	}
	s.last = snap
	return true, nil
}

// Get returns the latest snapshot, or nil when none was posted yet. When the store cannot be
// read it serves the one in memory, if any.
func (s *Service) Get(ctx context.Context) (*Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load(ctx)
}

func (s *Service) load(ctx context.Context) (*Snapshot, error) {
	data, err := s.store.Get(ctx)
	switch {
	case errors.Is(err, kv.ErrNotFound):
		return s.last, nil
	case err != nil:
		if s.last != nil {
			return s.last, nil
		}
		return nil, fmt.Errorf("read snapshot: %w", err)
	}
	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("stored snapshot: %w", err)
	}
	s.last = &snap
	return &snap, nil
}

// Handler serves POST /kaos/v1/meluxina/snapshot (the stager's pods, by service-account token)
// and GET /kaos/v1/meluxina (signed-in people).
type Handler struct {
	Service    *Service
	Verify     func(ctx context.Context, token string, namespaces []string) (string, error)
	Namespaces []string
	// Now is the clock; nil: time.Now.
	Now func() time.Time
}

func (h Handler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

// Post keeps the snapshot in the request body.
func (h Handler) Post(w http.ResponseWriter, r *http.Request) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"message": "a service-account token is required as bearer"})
		return
	}
	if _, err := h.Verify(r.Context(), token, h.Namespaces); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"message": "token not accepted for the MeluXina snapshot"})
		return
	}
	var snap Snapshot
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, MaxBodyBytes)).Decode(&snap); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": "body: " + err.Error()})
		return
	}
	if err := snap.Validate(h.now()); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
		return
	}
	if snap.Staged == nil {
		snap.Staged = []Staged{}
	}
	stored, err := h.Service.Put(r.Context(), &snap)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"stored": stored})
}

// Get answers the latest snapshot to any signed-in user; server_time lets the UI judge staleness
// without trusting the browser's clock.
func (h Handler) Get(w http.ResponseWriter, r *http.Request) {
	if _, err := auth.GetUser(r.Context()); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"message": "sign in to see what is staged on MeluXina"})
		return
	}
	snap, err := h.Service.Get(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"snapshot": snap, "server_time": h.now().UTC()})
}

var (
	activeMu sync.RWMutex
	active   *Service
)

// Configure makes the Service over store the one the API serves (called once, by lakefs run).
func Configure(store kv.Store) {
	activeMu.Lock()
	defer activeMu.Unlock()
	active = NewService(KVStore{KV: store})
}

// Active returns the configured Service, or nil.
func Active() *Service {
	activeMu.RLock()
	defer activeMu.RUnlock()
	return active
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
