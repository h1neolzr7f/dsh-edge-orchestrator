package agent

import (
	"context"
	"encoding/json"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceConfinementAndAtomicFiles(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	a, e := Open(Config{DBPath: filepath.Join(t.TempDir(), "a.db"), DeviceID: "w", Token: "s", CoordinatorURL: "https://localhost", Workspaces: map[string]string{"workspace/project": root}})
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	for _, path := range []string{"../escape", "x/../../escape", "/etc/passwd", "C:\\escape"} {
		if _, e = a.resolveWorkspacePath("workspace/project", path); e == nil {
			t.Fatalf("escape accepted %s", path)
		}
	}
	if e = os.Symlink(outside, filepath.Join(root, "outside")); e == nil {
		if _, e = a.resolveWorkspacePath("workspace/project", "outside/secret"); e == nil {
			t.Fatal("symlink escape accepted")
		}
	}
	task := protocol.TaskEnvelope{TaskID: "t", Capability: protocol.CapabilityRef{ID: "filesystem.write", Version: "1.x"}, Input: json.RawMessage(`{"workspace":"workspace/project","path":"out/note.txt","content":"hello","overwrite":false}`)}
	if _, e = a.execute(context.Background(), task); e != nil {
		t.Fatal(e)
	}
	if _, e = a.execute(context.Background(), task); e == nil {
		t.Fatal("overwrite=false replaced existing file")
	}
	task.Capability.ID = "filesystem.read"
	task.Input = json.RawMessage(`{"workspace":"workspace/project","path":"out/note.txt"}`)
	result, e := a.execute(context.Background(), task)
	if e != nil {
		t.Fatal(e)
	}
	var out struct{ Content, SHA256 string }
	json.Unmarshal(result, &out)
	if out.Content != "hello" || out.SHA256 != "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824" {
		t.Fatalf("bad content/hash %s", result)
	}
}
func TestMissingWriteContentDoesNotEraseFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "important.txt")
	os.WriteFile(path, []byte("keep me"), 0600)
	a, e := Open(Config{DBPath: filepath.Join(t.TempDir(), "a.db"), DeviceID: "w", Token: "s", CoordinatorURL: "https://localhost", Workspaces: map[string]string{"workspace/project": root}})
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	for _, body := range []string{`{"workspace":"workspace/project","path":"important.txt","overwrite":true}`, `{"workspace":"workspace/project","path":"important.txt","content":null,"overwrite":true}`} {
		_, err := a.execute(context.Background(), protocol.TaskEnvelope{Capability: protocol.CapabilityRef{ID: "filesystem.write"}, Input: json.RawMessage(body)})
		b, _ := os.ReadFile(path)
		if err == nil || string(b) != "keep me" {
			t.Fatalf("invalid write erased file: error=%v content=%q", err, b)
		}
	}
}
func TestLogChunkPreservesUTF8Boundary(t *testing.T) {
	a, e := Open(Config{DBPath: filepath.Join(t.TempDir(), "a.db"), DeviceID: "w", Token: "s", CoordinatorURL: "https://localhost"})
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	task := protocol.TaskEnvelope{TaskID: "log", PlanID: "p", TargetDeviceID: "w", IdempotencyKey: "p:log", Capability: protocol.CapabilityRef{ID: "device.inspect", Version: "1.x"}, DependsOn: []string{}, Input: json.RawMessage(`{}`)}
	a.Accept(task)
	a.claim(task)
	dir := t.TempDir()
	text := strings.Repeat("a", 4095) + "€"
	os.WriteFile(filepath.Join(dir, "stdout.log"), []byte(text), 0600)
	if e = a.tailLogs(task, dir); e != nil {
		t.Fatal(e)
	}
	events, e := a.outbox(0)
	if e != nil {
		t.Fatal(e)
	}
	got := ""
	for _, ev := range events {
		if ev.Type == "task.log" {
			var p protocol.EventPayload
			json.Unmarshal(ev.Payload, &p)
			got += p.Text
		}
	}
	if got != text {
		t.Fatalf("UTF-8 corrupted near boundary: %q", got[len(got)-12:])
	}
}

func TestDataDirectoryUsesCanonicalPath(t *testing.T) {
	root := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if e := os.Symlink(root, alias); e != nil {
		t.Skipf("directory symlink unavailable: %v", e)
	}
	cfg := Config{DBPath: filepath.Join(alias, "a.db"), Workspaces: map[string]string{"workspace/project": root}}
	data, _, e := configureLocal(&cfg)
	if e != nil {
		t.Fatal(e)
	}
	real, _ := filepath.EvalSymlinks(root)
	if data != real {
		t.Fatalf("task directory uses unresolved alias: %q != %q", data, real)
	}
}
