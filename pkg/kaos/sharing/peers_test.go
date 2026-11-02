// KAOS (kaos-lake, PRD novelcore/kaos#1283 F-12): new file, Apache-2.0.

package sharing_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/treeverse/lakefs/pkg/kaos/sharing"
	"github.com/treeverse/lakefs/pkg/logging"
)

const peersJSON = `{"project":"vision","peers":[
 {"project":"orbits","endpoint":"http://orbits-lakefs.ml-orbits.svc.cluster.local:80","url":"https://lakefs-orbits.acme.kaos.eu"},
 {"project":"cars","endpoint":"https://lakefs-cars.acme.kaos.eu","url":"https://lakefs-cars.acme.kaos.eu","future":"ignored"}
]}`

var (
	orbits = sharing.Peer{Project: "orbits", Endpoint: "http://orbits-lakefs.ml-orbits.svc.cluster.local:80", URL: "https://lakefs-orbits.acme.kaos.eu"}
	cars   = sharing.Peer{Project: "cars", Endpoint: "https://lakefs-cars.acme.kaos.eu", URL: "https://lakefs-cars.acme.kaos.eu"}
)

func TestParsePeers(t *testing.T) {
	got, dropped, err := sharing.ParsePeers([]byte(peersJSON), "vision")
	require.NoError(t, err)
	require.Empty(t, dropped)
	require.Equal(t, sharing.Peers{Project: "vision", Peers: []sharing.Peer{cars, orbits}}, got, "sorted by project")
}

// The own project is never its own peer, whether the file or token identity names it.
func TestParsePeers_SelfExcluded(t *testing.T) {
	raw := `{"project":"vision","peers":[
	 {"project":"vision","endpoint":"http://a","url":"https://a"},
	 {"project":"ml","endpoint":"http://b","url":"https://b"},
	 {"project":"cars","endpoint":"http://c","url":"https://c"}]}`
	got, dropped, err := sharing.ParsePeers([]byte(raw), "ml")
	require.NoError(t, err)
	require.Empty(t, dropped)
	require.Equal(t, []sharing.Peer{{Project: "cars", Endpoint: "http://c", URL: "https://c"}}, got.Peers)
}

// A peer entry that breaks a rule is left out and named; the rest of the file is used.
func TestParsePeers_DropsInvalidEntries(t *testing.T) {
	raw := `{"project":"vision","peers":[
	 {"project":"Cars","endpoint":"http://a","url":"https://a"},
	 {"project":"a_b","endpoint":"http://a","url":"https://a"},
	 {"project":"-x","endpoint":"http://a","url":"https://a"},
	 {"project":"","endpoint":"http://a","url":"https://a"},
	 {"project":"../etc","endpoint":"http://a","url":"https://a"},
	 {"project":"noendpoint","url":"https://a"},
	 {"project":"ftp","endpoint":"ftp://a","url":"https://a"},
	 {"project":"relative","endpoint":"/kaos","url":"https://a"},
	 {"project":"creds","endpoint":"http://u:p@a","url":"https://a"},
	 {"project":"nourl","endpoint":"http://a"},
	 {"project":"good","endpoint":"http://g","url":"https://g"},
	 {"project":"good","endpoint":"http://other","url":"https://other"}]}`
	got, dropped, err := sharing.ParsePeers([]byte(raw), "")
	require.NoError(t, err)
	require.Equal(t, []sharing.Peer{{Project: "good", Endpoint: "http://g", URL: "https://g"}}, got.Peers, "the first of a duplicate wins")
	require.Len(t, dropped, 11)
	require.Contains(t, dropped[0], `"Cars": not a project name`)
	require.Contains(t, dropped[10], "listed twice")
}

func TestParsePeers_InvalidFile(t *testing.T) {
	for name, raw := range map[string]string{
		"not json":            `{"project":"vision",`,
		"empty":               ``,
		"an array":            `[{"project":"cars"}]`,
		"own project invalid": `{"project":"Vision","peers":[]}`,
		"peers not a list":    `{"project":"vision","peers":{"project":"cars"}}`,
	} {
		_, _, err := sharing.ParsePeers([]byte(raw), "vision")
		require.ErrorIs(t, err, sharing.ErrInvalidPeers, name)
	}
}

// A partial file is used as far as it goes.
func TestParsePeers_Partial(t *testing.T) {
	got, _, err := sharing.ParsePeers([]byte(`{"project":"vision"}`), "vision")
	require.NoError(t, err)
	require.Equal(t, sharing.Peers{Project: "vision", Peers: []sharing.Peer{}}, got)
	got, _, err = sharing.ParsePeers([]byte(`{}`), "")
	require.NoError(t, err)
	require.Empty(t, got.Peers)
	require.NotNil(t, got.Peers, "served as [] , never null")
}

func write(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

func TestPeersFile_LoadAndReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "peers.json")
	f := sharing.NewPeersFile(path, "vision")
	require.Equal(t, sharing.Peers{Peers: []sharing.Peer{}}, f.Get(), "nothing before the first load")

	// Missing: the ConfigMap is mounted optional, so no peers.
	_, err := f.Load()
	require.NoError(t, err)
	require.Equal(t, sharing.Peers{Project: "vision", Peers: []sharing.Peer{}}, f.Get())

	write(t, path, peersJSON)
	_, err = f.Load()
	require.NoError(t, err)
	require.Equal(t, []sharing.Peer{cars, orbits}, f.Get().Peers)

	// Reload picks up a change.
	write(t, path, `{"project":"vision","peers":[{"project":"cars","endpoint":"https://lakefs-cars.acme.kaos.eu","url":"https://lakefs-cars.acme.kaos.eu"}]}`)
	_, err = f.Load()
	require.NoError(t, err)
	require.Equal(t, []sharing.Peer{cars}, f.Get().Peers)

	// Invalid JSON keeps the last good copy.
	write(t, path, `{"project":"vision","peers":[{"project":`)
	_, err = f.Load()
	require.ErrorIs(t, err, sharing.ErrInvalidPeers)
	require.Equal(t, []sharing.Peer{cars}, f.Get().Peers)

	// A file without its own project takes token identity's.
	write(t, path, `{"peers":[{"project":"orbits","endpoint":"http://orbits-lakefs.ml-orbits.svc.cluster.local:80","url":"https://lakefs-orbits.acme.kaos.eu"},{"project":"BAD","endpoint":"http://x","url":"http://x"}]}`)
	dropped, err := f.Load()
	require.NoError(t, err)
	require.Len(t, dropped, 1)
	require.Equal(t, sharing.Peers{Project: "vision", Peers: []sharing.Peer{orbits}}, f.Get())

	// Removed: no peers any more.
	require.NoError(t, os.Remove(path))
	_, err = f.Load()
	require.NoError(t, err)
	require.Empty(t, f.Get().Peers)

	// Get hands out a copy.
	write(t, path, peersJSON)
	_, err = f.Load()
	require.NoError(t, err)
	got := f.Get()
	got.Peers[0].Project = "changed"
	require.Equal(t, "cars", f.Get().Peers[0].Project)
}

// Run re-reads the file every interval until its context ends.
func TestPeersFile_Run(t *testing.T) {
	path := filepath.Join(t.TempDir(), "peers.json")
	write(t, path, `{"project":"vision","peers":[]}`)
	f := sharing.NewPeersFile(path, "vision")
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); f.Run(ctx, 10*time.Millisecond, logging.Dummy()) }()
	require.Eventually(t, func() bool { return f.Get().Project == "vision" }, 5*time.Second, 5*time.Millisecond)
	write(t, path, peersJSON)
	require.Eventually(t, func() bool { return len(f.Get().Peers) == 2 }, 5*time.Second, 5*time.Millisecond)
	write(t, path, `not json`)
	time.Sleep(50 * time.Millisecond)
	require.Len(t, f.Get().Peers, 2, "an invalid file keeps the last good copy")
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop with its context")
	}
}
