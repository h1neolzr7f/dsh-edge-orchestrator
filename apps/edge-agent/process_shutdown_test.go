package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestShutdownBoundaryChild(t *testing.T) {
	if os.Getenv("DSH_SHUTDOWN_BOUNDARY_CHILD") != "1" {
		return
	}
	fmt.Println("restart-start")
	if e := os.WriteFile("started", []byte("ready"), 0600); e != nil {
		t.Fatal(e)
	}
	for !exists("release") {
		time.Sleep(5 * time.Millisecond)
	}
	fmt.Println("restart-finish")
}

func shutdownRunnerFixture(t *testing.T) (*Agent, Config, protocol.TaskEnvelope, runnerSpec, runnerReady) {
	t.Helper()
	root := t.TempDir()
	exe, _ := os.Executable()
	cfg := Config{DBPath: filepath.Join(t.TempDir(), "a.db"), DeviceID: "w", Token: "s", CoordinatorURL: "https://localhost", Workspaces: map[string]string{"workspace/project": root}, AllowedPrograms: []string{exe}}
	a, e := Open(cfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { a.Close() })
	in, _ := json.Marshal(map[string]any{"program": exe, "args": []string{"-test.run=^TestShutdownBoundaryChild$"}, "cwd": "workspace/project"})
	task := protocol.TaskEnvelope{TaskID: "shutdown_boundary", PlanID: "p", TargetDeviceID: "w", IdempotencyKey: "p:shutdown_boundary", Capability: protocol.CapabilityRef{ID: "process.exec", Version: "1.x"}, DependsOn: []string{}, Input: in}
	if _, _, e = a.Accept(task); e != nil {
		t.Fatal(e)
	}
	if ok, e := a.claim(task); e != nil || !ok {
		t.Fatalf("claim %v %v", ok, e)
	}
	dir := a.taskDir(task.TaskID)
	if e = os.MkdirAll(dir, 0700); e != nil {
		t.Fatal(e)
	}
	spec := runnerSpec{TaskID: task.TaskID, Fingerprint: protocol.Fingerprint(task), Nonce: protocol.NewID("run"), Program: exe, Args: []string{"-test.run=^TestShutdownBoundaryChild$"}, CWD: root, Dir: dir, JobName: "Local\\dsh-shutdown-" + protocol.NewID("job"), Env: append(os.Environ(), "DSH_SHUTDOWN_BOUNDARY_CHILD=1"), GraceMS: 100, Deadline: time.Now().Add(10 * time.Second)}
	if e = atomicJSON(filepath.Join(dir, "spec.json"), spec); e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command(exe, "--dsh-internal-runner", filepath.Join(dir, "spec.json"))
	configureRunner(cmd)
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.WriteFile(filepath.Join(root, "release"), []byte("go"), 0600); cmd.Wait() })
	var ready runnerReady
	waitArtifact(t, func() bool { return readJSON(filepath.Join(dir, "ready.json"), &ready) == nil })
	if e = a.saveProcess(task, ready, spec); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(dir, "go"), []byte(spec.Nonce), 0600); e != nil {
		t.Fatal(e)
	}
	waitArtifact(t, func() bool { return exists(filepath.Join(root, "started")) })
	return a, cfg, task, spec, ready
}

func TestProcessShutdownDuringLogDrainDetachesWithoutCancellation(t *testing.T) {
	for _, source := range []string{"close", "run_context"} {
		t.Run(source, func(t *testing.T) {
			a, cfg, task, spec, ready := shutdownRunnerFixture(t)
			entered := make(chan struct{})
			release := make(chan struct{})
			var once sync.Once
			tail := func(t protocol.TaskEnvelope, dir string, final ...bool) error {
				e := a.tailLogs(t, dir, final...)
				once.Do(func() { close(entered); <-release })
				return e
			}
			done := make(chan error, 1)
			a.wg.Add(1)
			ctx, cancel := context.WithCancel(a.ctx)
			defer cancel()
			go func() { defer a.wg.Done(); _, e := a.monitorProcess(ctx, task, spec, ready, tail); done <- e }()
			<-entered
			closed := make(chan error, 1)
			if source == "run_context" {
				parent, cancelParent := context.WithCancel(context.Background())
				stop := context.AfterFunc(parent, a.stopRoot)
				defer stop()
				cancelParent()
				<-a.ctx.Done()
			}
			go func() { closed <- a.Close() }()
			<-a.ctx.Done()
			close(release)
			if e := <-done; !errors.Is(e, errAgentDetached) {
				t.Fatalf("shutdown monitor: %v", e)
			}
			if e := <-closed; e != nil {
				t.Fatal(e)
			}
			if exists(filepath.Join(spec.Dir, "cancel")) {
				t.Error("parent shutdown wrote user-cancel marker")
			}
			if !sameProcess(ready.Identity) {
				t.Error("parent shutdown terminated verified persistent runner")
			}
			resumed, e := Open(cfg)
			if e != nil {
				t.Fatal(e)
			}
			defer resumed.Close()
			pending, e := resumed.recoverTasks()
			if e != nil || len(pending) != 1 {
				t.Fatalf("recover %v %v", pending, e)
			}
			os.WriteFile(filepath.Join(spec.CWD, "release"), []byte("go"), 0600)
			resumed.launch(pending[0])
			waitArtifact(t, func() bool { s, _ := resumed.taskSnapshot(task.TaskID); return protocol.Terminal(s.Status) })
			snap, e := resumed.taskSnapshot(task.TaskID)
			if e != nil || snap.Status != "succeeded" || !strings.Contains(processSummary(filepath.Join(spec.Dir, "stdout.log")), "restart-finish") {
				t.Fatalf("resumed result %+v %v", snap, e)
			}
			if n, _ := resumed.ExecutionCount(task.TaskID); n != 1 {
				t.Fatalf("reran process %d", n)
			}
		})
	}
}

func TestCompletedProcessShutdownBeforeFinishRemainsRecoverable(t *testing.T) {
	a, cfg, task, spec, ready := shutdownRunnerFixture(t)
	os.WriteFile(filepath.Join(spec.CWD, "release"), []byte("go"), 0600)
	var exit runnerExit
	waitArtifact(t, func() bool {
		return readJSON(filepath.Join(spec.Dir, "exit.json"), &exit) == nil && !sameProcess(ready.Identity)
	})
	result, e := a.processResult(spec, exit)
	if e != nil {
		t.Fatal(e)
	}
	a.wg.Add(1)
	closed := make(chan error, 1)
	go func() { closed <- a.Close() }()
	<-a.ctx.Done()
	e = a.finish(task, result, protocol.Err("interrupted_by_restart", "agent stopped before durable completion"), false)
	if !errors.Is(e, errAgentDetached) {
		t.Errorf("completed persistent result became terminal on shutdown: %v", e)
	}
	a.wg.Done()
	if e = <-closed; e != nil {
		t.Fatal(e)
	}
	resumed, e := Open(cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer resumed.Close()
	snap, e := resumed.taskSnapshot(task.TaskID)
	if e != nil || snap.Status != "running" {
		t.Fatalf("completed result not recoverable: %+v %v", snap, e)
	}
	pending, e := resumed.recoverTasks()
	if e != nil || len(pending) != 1 {
		t.Fatalf("completed result recovery %v %v", pending, e)
	}
	resumed.launch(pending[0])
	waitArtifact(t, func() bool { s, _ := resumed.taskSnapshot(task.TaskID); return s.Status == "succeeded" })
}
