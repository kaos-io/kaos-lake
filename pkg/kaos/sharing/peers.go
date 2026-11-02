// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-12): new file, Apache-2.0.

// Package sharing lets a project's pipelines read the datasets that sibling projects of its
// organisation share with it (kaos.shared_with, pkg/kaos/repos).
//
// The owner side answers GET /kaos/v1/shared: to a caller in kaos-consumer-<project> groups, the
// datasets shared with those projects. The consumer side reads the peers file (this project and
// its siblings' lakeFS endpoints, written by the operator), asks every peer's /kaos/v1/shared
// with a token minted from this project's own Zitadel machine key, and adds the answers to the
// dataset catalog as <owner>/<dataset>. It never sends this project's lakeFS access keys to a
// peer, and serves names and versions only, never data.
package sharing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/treeverse/lakefs/pkg/kaos/repos"
	"github.com/treeverse/lakefs/pkg/logging"
)

// Peer is a sibling project's lakeFS.
type Peer struct {
	Project string `json:"project"`
	// Endpoint is where this server reaches the peer: in-cluster when both run in the same
	// cluster, else its public URL.
	Endpoint string `json:"endpoint"`
	// URL is the peer's public URL, for clients outside the cluster.
	URL string `json:"url"`
}

// Peers is the peers file: this project and its peers, sorted by project.
type Peers struct {
	Project string `json:"project"`
	Peers   []Peer `json:"peers"`
}

// ErrInvalidPeers marks a peers file that is not used; the last good copy stays.
var ErrInvalidPeers = errors.New("invalid peers file")

// ParsePeers decodes and checks a peers file. A file that is not JSON, or names an invalid own
// project, is an error. A peer entry that breaks a rule (a project name as KubeProject names go,
// an absolute http(s) endpoint and url, each project once) is left out and described in dropped.
// The own project, from the file or self, is never a peer of itself.
func ParsePeers(raw []byte, self string) (Peers, []string, error) {
	var in Peers
	if err := json.Unmarshal(raw, &in); err != nil {
		return Peers{}, nil, fmt.Errorf("%w: %w", ErrInvalidPeers, err)
	}
	if in.Project != "" && !repos.ValidProject(in.Project) {
		return Peers{}, nil, fmt.Errorf("%w: project %q is not a project name", ErrInvalidPeers, in.Project)
	}
	out := Peers{Project: in.Project, Peers: []Peer{}}
	var dropped []string
	for _, p := range in.Peers {
		switch {
		case !repos.ValidProject(p.Project):
			dropped = append(dropped, fmt.Sprintf("peer %q: not a project name", p.Project))
		case p.Project == in.Project || p.Project == self:
			// this project itself
		case slices.ContainsFunc(out.Peers, func(o Peer) bool { return o.Project == p.Project }):
			dropped = append(dropped, fmt.Sprintf("peer %q: listed twice, the first is used", p.Project))
		case !httpURL(p.Endpoint):
			dropped = append(dropped, fmt.Sprintf("peer %q: endpoint %q is not an absolute http(s) URL", p.Project, p.Endpoint))
		case !httpURL(p.URL):
			dropped = append(dropped, fmt.Sprintf("peer %q: url %q is not an absolute http(s) URL", p.Project, p.URL))
		default:
			out.Peers = append(out.Peers, p)
		}
	}
	slices.SortFunc(out.Peers, func(a, b Peer) int { return strings.Compare(a.Project, b.Project) })
	return out, dropped, nil
}

func httpURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil
}

// PeersFile is the peers file as last read. A missing file means no peers (the operator's
// ConfigMap is mounted optional); an invalid one keeps the last good copy.
type PeersFile struct {
	path, self string

	mu         sync.RWMutex
	cur        Peers
	lastReport string
}

// NewPeersFile returns the peers file at path, not read yet. self is this project
// (kaos.token_identity.project), never a peer.
func NewPeersFile(path, self string) *PeersFile {
	return &PeersFile{path: path, self: self, cur: Peers{Peers: []Peer{}}}
}

// Load reads the file again. On an error the last good copy stays. dropped describes the peer
// entries left out.
func (f *PeersFile) Load() (dropped []string, err error) {
	raw, err := os.ReadFile(f.path)
	var p Peers
	switch {
	case errors.Is(err, fs.ErrNotExist):
		p = Peers{Project: f.self, Peers: []Peer{}}
	case err != nil:
		return nil, fmt.Errorf("%w: %w", ErrInvalidPeers, err)
	default:
		if p, dropped, err = ParsePeers(raw, f.self); err != nil {
			return nil, err
		}
	}
	if p.Project == "" {
		p.Project = f.self
	}
	f.mu.Lock()
	f.cur = p
	f.mu.Unlock()
	return dropped, nil
}

// Get returns the peers as last read.
func (f *PeersFile) Get() Peers {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return Peers{Project: f.cur.Project, Peers: slices.Clone(f.cur.Peers)}
}

// Run reads the file now and every interval until ctx ends. It logs what it could not use, once
// per change.
func (f *PeersFile) Run(ctx context.Context, interval time.Duration, log logging.Logger) {
	log = log.WithField("service", "kaos_sharing")
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		f.reload(log)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (f *PeersFile) reload(log logging.Logger) {
	dropped, err := f.Load()
	report := strings.Join(dropped, "; ")
	if err != nil {
		report = err.Error()
	}
	f.mu.Lock()
	changed := report != f.lastReport
	f.lastReport = report
	f.mu.Unlock()
	switch {
	case !changed:
	case err != nil:
		log.WithError(err).WithField("path", f.path).Error("Peers file not applied; the last good copy stays")
	case len(dropped) > 0:
		log.WithField("path", f.path).WithField("dropped", dropped).Warn("Peers file entries left out")
	}
}
