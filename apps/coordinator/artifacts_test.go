package coordinator

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	edgelink "github.com/h1neolzr7f/dsh-edge-orchestrator/packages/edge-link"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestArtifactCrashAfterRenameAndReadyImmutability(t *testing.T) {
	s := setup(t)
	ts := httptest.NewTLSServer(s.Handler())
	defer ts.Close()
	c := edgelink.NewClient(ts.URL, "controller", ts.Client())
	ctx := context.Background()
	h := sha256.Sum256([]byte("ready"))
	in := protocol.ArtifactUploadRequest{PlanID: "future", TaskID: "stage", Name: "patch", MediaType: "text/plain", SizeBytes: 5, SHA256: hex.EncodeToString(h[:])}
	u, e := c.CreateArtifactUpload(ctx, in)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.UploadArtifactChunk(ctx, u.UploadID, 0, []byte("ready")); e != nil {
		t.Fatal(e)
	}
	s.mu.Lock()
	stored, e := s.uploadBy("upload_id=?", u.UploadID)
	s.mu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Rename(stored.Path, strings.TrimSuffix(stored.Path, ".part")+".ready"); e != nil {
		t.Fatal(e)
	}
	ref, e := c.CompleteArtifactUpload(ctx, u.UploadID)
	if e != nil {
		t.Fatal(e)
	}
	again, e := c.CompleteArtifactUpload(ctx, u.UploadID)
	if e != nil || again != ref {
		t.Fatalf("completion not idempotent %v", e)
	}
	if _, e = c.UploadArtifactChunk(ctx, u.UploadID, 5, []byte("overwrite")); e == nil {
		t.Fatal("ready mutable")
	}
	r := httptest.NewRequest("GET", ref.DownloadURI, nil)
	r.Header.Set("Authorization", "Bearer controller")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if !bytes.Equal(w.Body.Bytes(), []byte("ready")) {
		t.Fatal("ready bytes changed")
	}
	page, e := s.Events(0, 0, 100)
	if e != nil {
		t.Fatal(e)
	}
	count := 0
	for _, ev := range page.Events {
		if ev.Type == "artifact.ready" {
			count++
			if ev.WorkerSeq != 0 || ev.TaskSeq != 0 || ev.DeviceID != "" {
				t.Fatal("manufactured worker sequence")
			}
		}
	}
	if count != 1 {
		t.Fatalf("artifact events %d", count)
	}
}
func TestArtifactOffsetHashAndChunkLimits(t *testing.T) {
	s := setup(t)
	ts := httptest.NewTLSServer(s.Handler())
	defer ts.Close()
	c := edgelink.NewClient(ts.URL, "controller", ts.Client())
	ctx := context.Background()
	u, e := c.CreateArtifactUpload(ctx, protocol.ArtifactUploadRequest{PlanID: "future", TaskID: "invalid_hash", Name: "bad", MediaType: "text/plain", SizeBytes: 5, SHA256: strings.Repeat("0", 64)})
	if e != nil {
		t.Fatal(e)
	}
	r := httptest.NewRequest("PATCH", "/v1/artifacts/uploads/"+u.UploadID+"?offset=1", strings.NewReader("ready"))
	r.Header.Set("Authorization", "Bearer controller")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 409 || !strings.Contains(w.Body.String(), `"confirmed_offset":0`) {
		t.Fatalf("offset %d %s", w.Code, w.Body.String())
	}
	if _, e = c.UploadArtifactChunk(ctx, u.UploadID, 0, make([]byte, (4<<20)+1)); e == nil {
		t.Fatal("oversized chunk allowed")
	}
	u, e = c.CreateArtifactUpload(ctx, protocol.ArtifactUploadRequest{PlanID: "future", TaskID: "invalid_hash", Name: "bad", MediaType: "text/plain", SizeBytes: 5, SHA256: strings.Repeat("0", 64)})
	if e != nil || u.ConfirmedOffset != 0 {
		t.Fatal("oversized request advanced offset")
	}
	if _, e = c.UploadArtifactChunk(ctx, u.UploadID, 0, []byte("ready")); e != nil {
		t.Fatal(e)
	}
	if _, e = c.CompleteArtifactUpload(ctx, u.UploadID); e == nil || !strings.Contains(e.Error(), "ARTIFACT_HASH_MISMATCH") {
		t.Fatalf("wrong hash ready: %v", e)
	}
}
func TestArtifactMissingBindingRejectedWhileSchedulingPaused(t *testing.T) {
	s := setup(t)
	p := samplePlan()
	next := p.Tasks[0]
	next.TaskID = "downstream"
	next.DependsOn = []string{"t"}
	next.InputArtifactBindings = []protocol.ArtifactBinding{{FromTaskID: "t", Name: "missing", MountPath: "input"}}
	p.Tasks = append(p.Tasks, next)
	if _, e := s.Submit(p); e != nil {
		t.Fatal(e)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, e := s.db.Exec(`UPDATE plans SET paused=1 WHERE id='p'; UPDATE tasks SET status='succeeded' WHERE id='t'`); e != nil {
		t.Fatal(e)
	}
	if e := s.schedule(); e != nil {
		t.Fatal(e)
	}
	var status string
	if e := s.db.QueryRow(`SELECT status FROM tasks WHERE id='downstream'`).Scan(&status); e != nil || status != "rejected" {
		t.Fatalf("ready missing binding was gated by pause: %s %v", status, e)
	}
}
