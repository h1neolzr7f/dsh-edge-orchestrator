package coordinator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	edgelink "github.com/h1neolzr7f/dsh-edge-orchestrator/packages/edge-link"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWorkerArtifactAuthorityFollowsDispatchAndTerminalReplay(t *testing.T) {
	s := setup(t)
	ts := httptest.NewTLSServer(s.Handler())
	defer ts.Close()
	ctx := context.Background()
	c := edgelink.NewClient(ts.URL, "controller", ts.Client())
	w := edgelink.NewClient(ts.URL, "worker", ts.Client())
	bytes := []byte("confidential patch")
	h := sha256.Sum256(bytes)
	in := protocol.ArtifactUploadRequest{PlanID: "p", TaskID: "t", Name: "input", MediaType: "text/plain", SizeBytes: int64(len(bytes)), SHA256: hex.EncodeToString(h[:])}
	u, e := c.CreateArtifactUpload(ctx, in)
	if e != nil {
		t.Fatal(e)
	}
	c.UploadArtifactChunk(ctx, u.UploadID, 0, bytes)
	ref, e := c.CompleteArtifactUpload(ctx, u.UploadID)
	if e != nil {
		t.Fatal(e)
	}
	p := samplePlan()
	p.Tasks[0].RequiresApproval = true
	p.Tasks[0].InputArtifacts = []protocol.InputArtifact{{ArtifactID: ref.ArtifactID, SHA256: ref.SHA256, MountPath: "input"}}
	if _, e = s.Submit(p); e != nil {
		t.Fatal(e)
	}
	s.mu.Lock()
	s.schedule()
	s.mu.Unlock()
	dest := filepath.Join(t.TempDir(), "copy")
	if e = w.DownloadArtifactFile(ctx, ref.ArtifactID, dest, ref.SHA256); e == nil {
		t.Fatal("waiting approval disclosed input")
	}
	in.Name = "output"
	if _, e = w.CreateArtifactUpload(ctx, in); e == nil {
		t.Fatal("undispatched output upload allowed")
	}
	s.mu.Lock()
	_, e = s.db.Exec(`UPDATE tasks SET status='running' WHERE id='t'`)
	s.mu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	if e = w.DownloadArtifactFile(ctx, ref.ArtifactID, dest, ref.SHA256); e != nil {
		t.Fatal(e)
	}
	u, e = w.CreateArtifactUpload(ctx, in)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = w.UploadArtifactChunk(ctx, u.UploadID, 0, bytes); e != nil {
		t.Fatal(e)
	}
	if _, e = w.CompleteArtifactUpload(ctx, u.UploadID); e != nil {
		t.Fatal(e)
	}
	for _, status := range []string{"rejected", "cancelled", "succeeded"} {
		s.mu.Lock()
		_, e = s.db.Exec(`UPDATE tasks SET status=? WHERE id='t'`, status)
		s.mu.Unlock()
		if e != nil {
			t.Fatal(e)
		}
		if _, e = w.CreateArtifactUpload(ctx, in); e != nil {
			t.Fatal("same ready lookup must remain harmless", e)
		}
		if _, e = w.CompleteArtifactUpload(ctx, u.UploadID); e != nil {
			t.Fatal("completed replay", e)
		}
		next := in
		next.Name = "late-" + status
		if _, e = w.CreateArtifactUpload(ctx, next); e == nil {
			t.Fatal("terminal new evidence", status)
		}
	}
}
func TestWorkerCannotPublishAcrossTerminalHashBoundary(t *testing.T) {
	s := setup(t)
	p := samplePlan()
	s.Submit(p)
	s.mu.Lock()
	s.db.Exec(`UPDATE tasks SET status='running' WHERE id='t'`)
	s.mu.Unlock()
	ts := httptest.NewTLSServer(s.Handler())
	defer ts.Close()
	w := edgelink.NewClient(ts.URL, "worker", ts.Client())
	ctx := context.Background()
	h := sha256.Sum256([]byte("data"))
	u, e := w.CreateArtifactUpload(ctx, protocol.ArtifactUploadRequest{PlanID: "p", TaskID: "t", Name: "late", MediaType: "text/plain", SizeBytes: 4, SHA256: hex.EncodeToString(h[:])})
	if e != nil {
		t.Fatal(e)
	}
	w.UploadArtifactChunk(ctx, u.UploadID, 0, []byte("data"))
	s.artifactPublish = func(src, dst string) error {
		if strings.HasSuffix(dst, ".ready") {
			s.mu.Lock()
			_, e := s.db.Exec(`UPDATE tasks SET status='cancelled' WHERE id='t'`)
			s.mu.Unlock()
			if e != nil {
				return e
			}
		}
		return durableArtifactMove(src, dst)
	}
	if _, e = w.CompleteArtifactUpload(ctx, u.UploadID); e == nil {
		t.Fatal("hash race published after cancellation")
	}
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM artifacts WHERE name='late'`).Scan(&n)
	if n != 0 {
		t.Fatal("late ready row committed")
	}
}
func TestUndispatchedEventsQuarantineWithoutTaskMutation(t *testing.T) {
	for _, status := range []string{"queued", "waiting_approval", "rejected"} {
		t.Run(status, func(t *testing.T) {
			s := setup(t)
			p := samplePlan()
			s.Submit(p)
			other := samplePlan()
			other.PlanID = "other"
			other.IdempotencyKey = "other"
			other.Tasks[0].TaskID = "other_task"
			s.Submit(other)
			s.mu.Lock()
			s.db.Exec(`UPDATE tasks SET status=? WHERE id='t'`, status)
			s.db.Exec(`UPDATE tasks SET status='accepted' WHERE id='other_task'`)
			s.mu.Unlock()
			for i, kind := range []string{"task.failed", "task.log", "task.cancelled"} {
				ev := protocol.TaskEvent{EventID: protocol.NewID("ev"), PlanID: "p", TaskID: "t", DeviceID: "w", WorkerSeq: int64(i + 1), TaskSeq: 1, Type: kind, Timestamp: time.Now().UTC(), Payload: json.RawMessage(`{}`)}
				ack, e := s.AppendEvent("w", ev)
				if e == nil || ack != int64(i+1) {
					t.Fatalf("invalid sequence not quarantined %d %v", ack, e)
				}
				snap, _ := s.Plan("p")
				if snap.Tasks[0].Status != status {
					t.Fatalf("mutated %s to %s", status, snap.Tasks[0].Status)
				}
				if _, e = s.AppendEvent("w", ev); e != nil {
					t.Fatal("historical duplicate not acknowledged", e)
				}
			}
			ev := protocol.TaskEvent{EventID: protocol.NewID("ev"), PlanID: "other", TaskID: "other_task", DeviceID: "w", WorkerSeq: 4, TaskSeq: 1, Type: "task.started", Timestamp: time.Now().UTC(), Payload: json.RawMessage(`{}`)}
			if ack, e := s.AppendEvent("w", ev); e != nil || ack != 4 {
				t.Fatalf("valid task wedged %d %v", ack, e)
			}
		})
	}
}
func TestRetainedCompleteDownloadGetsAuthenticatedDigest(t *testing.T) {
	t.Setenv("GODEBUG", "httpservecontentkeepheaders=0")
	s := setup(t)
	ts := httptest.NewTLSServer(s.Handler())
	defer ts.Close()
	c := edgelink.NewClient(ts.URL, "controller", ts.Client())
	ctx := context.Background()
	f := filepath.Join(t.TempDir(), "patch")
	data := []byte("retained patch bytes")
	os.WriteFile(f, data, 0600)
	ref, e := c.UploadArtifactFile(ctx, protocol.ArtifactUploadRequest{PlanID: "future", TaskID: "patch", Name: "patch", MediaType: "text/plain"}, f)
	if e != nil {
		t.Fatal(e)
	}
	os.WriteFile(f, []byte(strings.Repeat("x", len(data))), 0600)
	if e = c.DownloadArtifactFile(ctx, ref.ArtifactID, f, ""); e != nil {
		t.Fatal("complete retained patch failed", e)
	}
}
