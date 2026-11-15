package agent

import (
	"context"
	"encoding/json"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestProcessIdentityRejectsReusedPID(t *testing.T) {
	id, e := processIdentity(os.Getpid())
	if e != nil {
		t.Fatal(e)
	}
	if !sameProcess(id) {
		t.Fatal("own process identity failed")
	}
	id.StartTimeNS++
	if sameProcess(id) {
		t.Fatal("reused PID accepted with different creation time")
	}
	id.StartTimeNS--
	id.BootID = "different-boot"
	if sameProcess(id) {
		t.Fatal("process accepted after boot changed")
	}
}
func TestReadyRunnerHandshakeResumesAfterRestart(t *testing.T) {
	root := t.TempDir()
	goExe := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		goExe += ".exe"
	}
	cfg := Config{DBPath: filepath.Join(t.TempDir(), "a.db"), DeviceID: "w", Token: "s", CoordinatorURL: "https://localhost", Workspaces: map[string]string{"workspace/project": root}, AllowedPrograms: []string{goExe}}
	a, e := Open(cfg)
	if e != nil {
		t.Fatal(e)
	}
	task := protocol.TaskEnvelope{TaskID: "gate", PlanID: "p", TargetDeviceID: "w", IdempotencyKey: "p:gate", Capability: protocol.CapabilityRef{ID: "process.exec", Version: "1.x"}, DependsOn: []string{}, Input: json.RawMessage(`{}`)}
	a.Accept(task)
	a.claim(task)
	dir := a.taskDir(task.TaskID)
	os.MkdirAll(dir, 0700)
	spec := runnerSpec{TaskID: task.TaskID, Fingerprint: protocol.Fingerprint(task), Nonce: protocol.NewID("run"), Program: goExe, Args: []string{"version"}, CWD: root, Dir: dir, JobName: "Local\\dsh-gate-" + protocol.NewID("job"), Env: os.Environ(), GraceMS: 100, Deadline: time.Now().Add(2 * time.Second)}
	atomicJSON(filepath.Join(dir, "spec.json"), spec)
	exe, _ := os.Executable()
	cmd := exec.Command(exe, "--dsh-internal-runner", filepath.Join(dir, "spec.json"))
	configureRunner(cmd)
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	defer cmd.Wait()
	defer cmd.Process.Kill()
	var ready runnerReady
	limit := time.Now().Add(time.Second)
	for readJSON(filepath.Join(dir, "ready.json"), &ready) != nil {
		if time.Now().After(limit) {
			t.Fatal("runner not ready")
		}
		time.Sleep(5 * time.Millisecond)
	}
	a.Close()
	a, e = Open(cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	pending, e := a.recoverTasks()
	if e != nil || len(pending) != 1 {
		t.Fatalf("handshake not recognized as recoverable: %v %v", pending, e)
	}
	result, e := a.runProcess(context.Background(), task)
	if e != nil || !strings.Contains(string(result), "go version") {
		t.Fatalf("ready runner never released after restart: %s %v", result, e)
	}
	if sameProcess(ready.Identity) {
		t.Fatal("process result returned while its runner is still alive")
	}
	if n, _ := a.ExecutionCount(task.TaskID); n != 1 {
		t.Fatal("handshake reran task")
	}
}
