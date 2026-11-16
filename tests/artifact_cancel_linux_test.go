package tests

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// A FIFO substituted in the coordinator's private test directory is a controlled
// slow read: completion has opened its source, but cannot finish hashing it yet.
func TestArtifactSlowCompletionDoesNotBlockLiveProcessCancellation(t *testing.T) {
	e := newPhase2(t)
	exe, _ := os.Executable()
	job := task("hash_cancel_tree", "process.exec", map[string]any{"program": exe, "args": []string{"--phase2-workload", "spawn"}, "cwd": "workspace/project", "timeout_ms": 60000})
	p := protocol.PlanEnvelope{PlanID: "hash_cancel_plan", IdempotencyKey: "hash_cancel:key", Goal: "cancel during hashing", ConcurrencyLimit: 1, CreatedAt: time.Now().UTC(), Tasks: []protocol.TaskEnvelope{job}}
	if _, err := e.c.Submit(e.ctx, p); err != nil {
		t.Fatal(err)
	}
	pid := 0
	await(t, e.ctx, func() bool {
		b, err := os.ReadFile(filepath.Join(e.root, "child.pid"))
		if err != nil {
			return false
		}
		pid, _ = strconv.Atoi(string(b))
		return pid > 0
	})
	h := sha256.Sum256([]byte("ready"))
	u, err := e.c.CreateArtifactUpload(e.ctx, protocol.ArtifactUploadRequest{PlanID: "future", TaskID: "slow_complete", Name: "slow", MediaType: "text/plain", SizeBytes: 5, SHA256: hex.EncodeToString(h[:])})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.c.UploadArtifactChunk(e.ctx, u.UploadID, 0, []byte("ready")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(e.artifactDir, u.ArtifactID+".part")
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err = unix.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	opened := make(chan error, 1)
	release := make(chan struct{})
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		f, err := os.OpenFile(path, os.O_WRONLY, 0600)
		opened <- err
		if err != nil {
			return
		}
		defer f.Close()
		f.Write([]byte("r"))
		<-release
		f.Write([]byte("eady"))
	}()
	complete := make(chan error, 1)
	go func() { _, err := e.c.CompleteArtifactUpload(e.ctx, u.UploadID); complete <- err }()
	select {
	case err = <-opened:
		if err != nil {
			t.Fatal(err)
		}
	case <-e.ctx.Done():
		t.Fatal("completion did not open source")
	}
	defer func() {
		close(release)
		<-writerDone
		select {
		case <-complete:
		case <-e.ctx.Done():
			t.Error("completion did not drain")
		}
	}()
	ctx, cancel := context.WithTimeout(e.ctx, 2*time.Second)
	defer cancel()
	start := time.Now()
	if _, err = e.c.Control(ctx, p.PlanID, "cancel"); err != nil {
		t.Fatalf("live cancellation blocked by completion: %v", err)
	}
	await(t, ctx, func() bool { s, err := e.c.Plan(ctx, p.PlanID); return err == nil && s.Status == "cancelled" })
	if time.Since(start) > 2*time.Second {
		t.Fatal("live cancellation exceeded strict two-second bound")
	}
	if childAlive(pid) {
		t.Fatalf("live orphan child %d", pid)
	}
}
