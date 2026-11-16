package coordinator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	edgelink "github.com/h1neolzr7f/dsh-edge-orchestrator/packages/edge-link"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestArtifactDirectoryRetryRepeatsFailedParentSync(t *testing.T) {
	path := filepath.Join(t.TempDir(), "artifacts")
	parent := filepath.Dir(path)
	calls := 0
	injected := errors.New("parent sync failed")
	sync := func(dir string) error {
		if dir == parent {
			calls++
			return injected
		}
		return syncArtifactDir(dir)
	}
	if e := durableArtifactDirWithSync(path, sync); !errors.Is(e, injected) {
		t.Fatalf("initial sync=%v", e)
	}
	if _, e := os.Stat(path); e != nil {
		t.Fatal(e)
	}
	if e := durableArtifactDirWithSync(path, sync); !errors.Is(e, injected) {
		t.Fatalf("retry skipped parent durability barrier: %v", e)
	}
	if calls != 2 {
		t.Fatalf("parent sync calls=%d", calls)
	}
}

func TestArtifactDirectoryConcurrentCreateCannotSkipStalledParentSync(t *testing.T) {
	path := filepath.Join(t.TempDir(), "artifacts")
	parent := filepath.Dir(path)
	entered := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan error, 1)
	var calls atomic.Int32
	sync := func(dir string) error {
		if dir == parent {
			if calls.Add(1) == 1 {
				close(entered)
				<-release
				return errors.New("first sync failed")
			}
			return errors.New("second sync failed")
		}
		return syncArtifactDir(dir)
	}
	go func() { firstDone <- durableArtifactDirWithSync(path, sync) }()
	<-entered
	// The directory is visible, but the first parent's publication barrier has
	// not succeeded. A second upload must establish its own barrier or fail.
	second := durableArtifactDirWithSync(path, sync)
	close(release)
	first := <-firstDone
	if first == nil || second == nil {
		t.Fatalf("concurrent create bypassed parent sync: first=%v second=%v", first, second)
	}
}

func TestArtifactDirectoryBelowTraversalOnlyAncestor(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires non-root execution to enforce directory read permissions")
	}
	shared := filepath.Join(t.TempDir(), "shared")
	state := filepath.Join(shared, "private-state")
	if e := os.MkdirAll(state, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.Chmod(shared, 0111); e != nil {
		t.Fatal(e)
	}
	defer os.Chmod(shared, 0700)
	// Confirm the fixture actually denies listing while allowing the coordinator
	// to operate its own previously established database directory below it.
	if _, e := os.ReadDir(shared); !os.IsPermission(e) {
		t.Fatalf("ancestor is not traversal-only: %v", e)
	}
	dbPath := filepath.Join(state, "coordinator.db")
	s, e := Open(Config{DBPath: dbPath, ControllerToken: "controller", WorkerTokens: map[string]string{"w": "worker"}})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if _, e = os.ReadFile(dbPath); e != nil {
		t.Fatalf("valid state DB unreadable: %v", e)
	}
	ts := httptest.NewTLSServer(s.Handler())
	defer ts.Close()
	c := edgelink.NewClient(ts.URL, "controller", ts.Client())
	h := sha256.Sum256(nil)
	in := protocol.ArtifactUploadRequest{PlanID: "future", TaskID: "traversal", Name: "empty", MediaType: "application/octet-stream", SHA256: hex.EncodeToString(h[:])}
	u, e := c.CreateArtifactUpload(context.Background(), in)
	if e != nil {
		t.Fatalf("upload failed below valid traversal-only ancestor: %v", e)
	}
	if _, e = c.CompleteArtifactUpload(context.Background(), u.UploadID); e != nil {
		t.Fatalf("ready publication: %v", e)
	}
}
