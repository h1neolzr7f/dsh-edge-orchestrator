package artifact_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	coordinator "github.com/h1neolzr7f/dsh-edge-orchestrator/apps/coordinator"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/artifact"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestUploadOpenFileKeepsCheckedDescriptorAfterPathReplacement(t *testing.T) {
	s, e := coordinator.Open(coordinator.Config{DBPath: filepath.Join(t.TempDir(), "c.db"), ControllerToken: "controller", WorkerTokens: map[string]string{"w": "worker"}})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ts := httptest.NewTLSServer(s.Handler())
	defer ts.Close()
	c := artifact.NewClient(ts.URL, "controller", ts.Client())
	path := filepath.Join(t.TempDir(), "output")
	original := []byte("checked original")
	if e = os.WriteFile(path, original, 0600); e != nil {
		t.Fatal(e)
	}
	f, e := os.Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	if e = os.Rename(path, path+".old"); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, []byte("replaced unsafe"), 0600); e != nil {
		t.Fatal(e)
	}
	f.Seek(5, 0) // Descriptor API hashes from zero, independently of caller offset.
	ctx := context.Background()
	ref, e := c.UploadOpenFile(ctx, protocol.ArtifactUploadRequest{PlanID: "future", TaskID: "descriptor", Name: "out", MediaType: "text/plain"}, f)
	if e != nil {
		t.Fatal(e)
	}
	hash := sha256.Sum256(original)
	if ref.SHA256 != hex.EncodeToString(hash[:]) {
		t.Fatal("uploaded replacement pathname instead of checked descriptor")
	}
	dest := filepath.Join(t.TempDir(), "download")
	if e = c.DownloadFile(ctx, ref.ArtifactID, dest, ref.SHA256); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(dest)
	if e != nil || string(b) != string(original) {
		t.Fatalf("descriptor bytes=%q %v", b, e)
	}
	if _, e = f.Stat(); e != nil {
		t.Fatal("client closed caller-owned descriptor")
	}
}
