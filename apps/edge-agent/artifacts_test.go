package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func artifactTask(id, hash string) protocol.TaskEnvelope {
	return protocol.TaskEnvelope{TaskID: id, PlanID: "p", IdempotencyKey: "p:" + id, TargetDeviceID: "w", Capability: protocol.CapabilityRef{ID: "filesystem.read", Version: "1.x"}, Input: json.RawMessage(`{"workspace":"workspace/project","path":"input"}`), DependsOn: []string{}, InputArtifacts: []protocol.InputArtifact{{ArtifactID: "artifact", SHA256: hash, MountPath: "input"}}}
}
func waitArtifact(t *testing.T, f func() bool) {
	t.Helper()
	end := time.Now().Add(5 * time.Second)
	for !f() {
		if time.Now().After(end) {
			t.Fatal("artifact wait timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
func TestArtifactAcceptedPreflightResumesRangeAfterRestart(t *testing.T) {
	data := []byte(strings.Repeat("download", 1<<18))
	h := sha256.Sum256(data)
	hash := hex.EncodeToString(h[:])
	var mode atomic.Int32
	var requests atomic.Int32
	var resumed atomic.Bool
	blocked := make(chan struct{}, 1)
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("ETag", `"`+hash+`"`)
		if mode.Load() == 0 {
			if requests.Add(1) == 1 {
				w.Header().Set("Content-Length", strconv.Itoa(len(data)))
				w.Write(data[:1<<20])
				w.(http.Flusher).Flush()
				panic(http.ErrAbortHandler)
			}
			select {
			case blocked <- struct{}{}:
			default:
			}
			<-r.Context().Done()
			return
		}
		start := 0
		if strings.HasPrefix(r.Header.Get("Range"), "bytes=") {
			fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-", &start)
			if start == 1<<20 {
				resumed.Store(true)
			}
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, len(data)-1, len(data)))
			w.WriteHeader(206)
		}
		w.Write(data[start:])
	}))
	defer ts.Close()
	var calls atomic.Int32
	cfg := Config{DBPath: filepath.Join(t.TempDir(), "agent.db"), DeviceID: "w", Token: "secret", CoordinatorURL: ts.URL, HTTPClient: ts.Client(), Executor: func(ctx context.Context, t protocol.TaskEnvelope) (json.RawMessage, error) {
		calls.Add(1)
		return json.RawMessage(`{}`), nil
	}}
	a, e := Open(cfg)
	if e != nil {
		t.Fatal(e)
	}
	task := artifactTask("resume", hash)
	if _, _, e = a.Accept(task); e != nil {
		t.Fatal(e)
	}
	a.launch(task)
	select {
	case <-blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("partial download did not block")
	}
	snap, e := a.taskSnapshot(task.TaskID)
	if e != nil || snap.Status != "accepted" {
		t.Fatalf("preflight status %s %v", snap.Status, e)
	}
	if n, _ := a.ExecutionCount(task.TaskID); n != 0 {
		t.Fatal("preflight claimed execution")
	}
	a.Close()
	mode.Store(1)
	a, e = Open(cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	pending, e := a.recoverTasks()
	if e != nil || len(pending) != 1 {
		t.Fatalf("accepted recovery %v %v", pending, e)
	}
	a.launch(pending[0])
	waitArtifact(t, func() bool { s, _ := a.taskSnapshot(task.TaskID); return s.Status == "succeeded" })
	if !resumed.Load() || calls.Load() != 1 {
		t.Fatalf("Range resume=%v executions=%d", resumed.Load(), calls.Load())
	}
	if n, _ := a.ExecutionCount(task.TaskID); n != 1 {
		t.Fatalf("claim count %d", n)
	}
	waitArtifact(t, func() bool {
		_, err := os.Stat(filepath.Join(a.taskDir(task.TaskID), "workspace"))
		return os.IsNotExist(err)
	})
}
func TestArtifactLateDuplicateDoesNotRebuildWorkspace(t *testing.T) {
	a, e := Open(Config{DBPath: filepath.Join(t.TempDir(), "agent.db"), DeviceID: "w", Token: "secret", CoordinatorURL: "https://localhost"})
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	h := strings.Repeat("0", 64)
	for _, status := range []string{"succeeded", "running"} {
		task := artifactTask(status, h)
		a.Accept(task)
		a.mu.Lock()
		_, e = a.db.Exec(`UPDATE local_task_ledger SET status=? WHERE id=?`, status, task.TaskID)
		a.mu.Unlock()
		if e != nil {
			t.Fatal(e)
		}
		dir := filepath.Join(a.taskDir(task.TaskID), "workspace")
		os.MkdirAll(dir, 0700)
		path := filepath.Join(dir, "sentinel")
		os.WriteFile(path, []byte("preserve"), 0600)
		a.launch(task)
		a.wg.Wait()
		b, e := os.ReadFile(path)
		if e != nil || string(b) != "preserve" {
			t.Fatalf("late %s duplicate rebuilt workspace", status)
		}
	}
}
func TestArtifactCancellationWinningClaimCleansPreparedWorkspace(t *testing.T) {
	data := []byte("input")
	h := sha256.Sum256(data)
	var a *Agent
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		_, e := a.db.Exec(`INSERT INTO cancellations(task_id) VALUES('lost_claim')`)
		a.mu.Unlock()
		if e != nil {
			t.Error(e)
		}
		w.Header().Set("ETag", `"`+hex.EncodeToString(h[:])+`"`)
		w.Write(data)
	}))
	defer ts.Close()
	var e error
	a, e = Open(Config{DBPath: filepath.Join(t.TempDir(), "agent.db"), DeviceID: "w", Token: "secret", CoordinatorURL: ts.URL, HTTPClient: ts.Client()})
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	task := artifactTask("lost_claim", hex.EncodeToString(h[:]))
	a.Accept(task)
	a.launch(task)
	a.wg.Wait()
	snap, e := a.taskSnapshot(task.TaskID)
	if e != nil || snap.Status != "cancelled" {
		t.Fatalf("lost claim status %s %v", snap.Status, e)
	}
	if _, e = os.Stat(filepath.Join(a.taskDir(task.TaskID), "workspace")); !os.IsNotExist(e) {
		t.Fatal("lost claim leaked prepared workspace")
	}
	if n, _ := a.ExecutionCount(task.TaskID); n != 0 {
		t.Fatal("cancelled preflight executed")
	}
}
func TestArtifactScratchUsesDeclaredAuthorizedWorkspace(t *testing.T) {
	data := []byte("scratch")
	h := sha256.Sum256(data)
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"`+hex.EncodeToString(h[:])+`"`)
		w.Write(data)
	}))
	defer ts.Close()
	a, e := Open(Config{DBPath: filepath.Join(t.TempDir(), "agent.db"), DeviceID: "w", Token: "secret", CoordinatorURL: ts.URL, HTTPClient: ts.Client(), Workspaces: map[string]string{"workspace/custom": t.TempDir()}})
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	task := artifactTask("custom", hex.EncodeToString(h[:]))
	task.Input = json.RawMessage(`{"workspace":"workspace/custom","path":"input"}`)
	ctx, e := a.prepareTask(context.Background(), task)
	if e != nil {
		t.Fatal(e)
	}
	out, e := a.execute(ctx, task)
	if e != nil || !strings.Contains(string(out), "scratch") {
		t.Fatalf("custom logical scratch: %s %v", out, e)
	}
}

func TestArtifactNestedProcessMount(t *testing.T) {
	for _, worktree := range []bool{false, true} {
		t.Run(fmt.Sprint(worktree), func(t *testing.T) {
			data := []byte("nested")
			h := sha256.Sum256(data)
			ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("ETag", `"`+hex.EncodeToString(h[:])+`"`)
				w.Write(data)
			}))
			defer ts.Close()
			root := t.TempDir()
			git := func(args ...string) string {
				cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
				b, e := cmd.CombinedOutput()
				if e != nil {
					t.Fatalf("git %v: %s %v", args, b, e)
				}
				return strings.TrimSpace(string(b))
			}
			head := ""
			if worktree {
				git("init", "-q")
				os.MkdirAll(filepath.Join(root, "sub", "deep"), 0700)
				os.WriteFile(filepath.Join(root, "sub", "deep", "tracked"), []byte("host"), 0600)
				git("add", ".")
				git("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "fixture")
				head = git("rev-parse", "HEAD")
			}
			a, e := Open(Config{DBPath: filepath.Join(t.TempDir(), "agent.db"), DeviceID: "w", Token: "secret", CoordinatorURL: ts.URL, HTTPClient: ts.Client(), Workspaces: map[string]string{"workspace/project": root}})
			if e != nil {
				t.Fatal(e)
			}
			defer a.Close()
			task := artifactTask("nested", hex.EncodeToString(h[:]))
			task.Capability.ID = "process.exec"
			task.Input = json.RawMessage(`{"program":"git","args":["hash-object","input"],"cwd":"workspace/project/sub/deep"}`)
			if worktree {
				task.WorkspaceContext = &protocol.WorkspaceContext{RootDir: "workspace/project", ExpectedGitHead: head}
			}
			ctx, e := a.prepareTask(context.Background(), task)
			if e != nil {
				t.Fatal(e)
			}
			defer a.cleanupTaskWorkspace(task)
			dir, e := a.processDir(ctx, "workspace/project/sub/deep")
			if e != nil {
				t.Fatal(e)
			}
			b, e := os.ReadFile(filepath.Join(dir, "input"))
			if e != nil || string(b) != "nested" {
				t.Fatalf("nested cwd mount %q: %v", b, e)
			}
			cmd := exec.Command("git", "hash-object", "input")
			cmd.Dir = dir
			if b, e = cmd.CombinedOutput(); e != nil {
				t.Fatalf("real process cannot read cwd mount: %s %v", b, e)
			}
			if _, e = os.Stat(filepath.Join(root, "sub", "deep", "input")); !os.IsNotExist(e) {
				t.Fatal("mounted into host checkout")
			}
			if _, e = os.Stat(filepath.Join(a.taskDir(task.TaskID), "workspace", "input")); !os.IsNotExist(e) {
				t.Fatal("mounted into checkout root instead of execution cwd")
			}
		})
	}
}

func TestArtifactClaimBoundaryGuards(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		t.Run(fmt.Sprint(shutdown), func(t *testing.T) {
			a, e := Open(Config{DBPath: filepath.Join(t.TempDir(), "agent.db"), DeviceID: "w", Token: "secret", CoordinatorURL: "https://localhost"})
			if e != nil {
				t.Fatal(e)
			}
			defer a.Close()
			task := artifactTask("boundary", strings.Repeat("0", 64))
			task.InputArtifacts = nil
			if !shutdown {
				deadline := time.Now().Add(-time.Second)
				task.Deadline = &deadline
			}
			if _, _, e = a.Accept(task); e != nil {
				t.Fatal(e)
			}
			if shutdown {
				a.cancel()
			}
			claimed, e := a.claim(task)
			if e != nil {
				t.Fatal(e)
			}
			if claimed {
				t.Error("claimed task with expired execution context")
			}
			if n, _ := a.ExecutionCount(task.TaskID); n != 0 {
				t.Errorf("execution count=%d", n)
			}
			events, e := a.outbox(0)
			if e != nil {
				t.Fatal(e)
			}
			for _, event := range events {
				if event.Type == "task.started" {
					t.Fatal("preflight emitted task.started")
				}
			}
			snap, e := a.taskSnapshot(task.TaskID)
			want := "failed"
			if shutdown {
				want = "accepted"
			}
			if e != nil || snap.Status != want {
				t.Fatalf("status=%s want=%s %v", snap.Status, want, e)
			}
		})
	}
}
