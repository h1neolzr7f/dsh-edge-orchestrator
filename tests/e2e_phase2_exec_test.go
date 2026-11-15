package tests

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	coordinator "github.com/h1neolzr7f/dsh-edge-orchestrator/apps/coordinator"
	agent "github.com/h1neolzr7f/dsh-edge-orchestrator/apps/edge-agent"
	edgelink "github.com/h1neolzr7f/dsh-edge-orchestrator/packages/edge-link"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "--phase2-workload" {
		switch os.Args[2] {
		case "flood":
			fmt.Print(strings.Repeat("z", 1200<<10))
		case "burst":
			fmt.Print(strings.Repeat("x", 8<<20))
			os.WriteFile("burst-ready", []byte("ready"), 0600)
			time.Sleep(30 * time.Second)
		case "log-long":
			fmt.Println("restart-start")
			time.Sleep(time.Second)
			fmt.Println("restart-finish")
		case "log":
			fmt.Println("real-exec-log")
			time.Sleep(300 * time.Millisecond)
			os.WriteFile("test-report.txt", []byte("tests passed"), 0600)
		case "fail":
			fmt.Fprintln(os.Stderr, "expected failure")
			os.Exit(7)
		case "sleep":
			fmt.Println("sleep-ready")
			time.Sleep(30 * time.Second)
		case "spawn":
			child := exec.Command(os.Args[0], "--phase2-workload", "sleep")
			if e := child.Start(); e != nil {
				panic(e)
			}
			os.WriteFile("child.pid", []byte(strconv.Itoa(child.Process.Pid)), 0600)
			fmt.Println("child_pid", child.Process.Pid)
			child.Wait()
		case "marker":
			os.WriteFile("must-not-exist", []byte("executed"), 0600)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type phase2Env struct {
	cfgA agent.Config
	c    *edgelink.Client
	s    *coordinator.Server
	a, b *agent.Agent
	root string
	head string
	ctx  context.Context
}

func newPhase2(t *testing.T, budgets ...time.Duration) *phase2Env {
	t.Helper()
	budget := 15 * time.Second
	if len(budgets) > 0 {
		budget = budgets[0]
	}
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	t.Cleanup(cancel)
	root := t.TempDir()
	git := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		out, e := cmd.CombinedOutput()
		if e != nil {
			t.Fatalf("git %v: %s %v", args, out, e)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	os.WriteFile(filepath.Join(root, "README.md"), []byte("fixture"), 0600)
	git("add", ".")
	git("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "initial")
	head := git("rev-parse", "HEAD")
	s, e := coordinator.Open(coordinator.Config{DBPath: filepath.Join(t.TempDir(), "c.db"), ControllerToken: "c", WorkerTokens: map[string]string{"worker-a": "a-secret", "worker-b": "b-secret"}})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	ts := httptest.NewTLSServer(s.Handler())
	t.Cleanup(ts.Close)
	exe, _ := os.Executable()
	workers := []*agent.Agent{}
	var configs []agent.Config
	for _, d := range []struct{ id, token string }{{"worker-a", "a-secret"}, {"worker-b", "b-secret"}} {
		cfg := agent.Config{DBPath: filepath.Join(t.TempDir(), "a.db"), DeviceID: d.id, Token: d.token, CoordinatorURL: ts.URL, HTTPClient: ts.Client(), Workspaces: map[string]string{"workspace/project": root}, AllowedPrograms: []string{exe, "git"}}
		configs = append(configs, cfg)
		a, e := agent.Open(cfg)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { a.Close() })
		workers = append(workers, a)
		go a.Run(ctx)
	}
	c := edgelink.NewClient(ts.URL, "c", ts.Client())
	await(t, ctx, func() bool { d, e := c.Devices(ctx); return e == nil && len(d) == 2 && d[0].Online && d[1].Online })
	return &phase2Env{cfgA: configs[0], c: c, s: s, a: workers[0], b: workers[1], root: root, head: head, ctx: ctx}
}
func input(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func (e *phase2Env) run(t *testing.T, tasks ...protocol.TaskEnvelope) protocol.PlanSnapshot {
	t.Helper()
	p := protocol.PlanEnvelope{PlanID: protocol.NewID("plan"), IdempotencyKey: protocol.NewID("key"), Goal: "Phase 2 execution", ConcurrencyLimit: 2, CreatedAt: time.Now().UTC(), Tasks: tasks}
	if _, err := e.c.Submit(e.ctx, p); err != nil {
		t.Fatal(err)
	}
	var snap protocol.PlanSnapshot
	await(t, e.ctx, func() bool {
		var err error
		snap, err = e.c.Plan(e.ctx, p.PlanID)
		return err == nil && protocol.Terminal(snap.Status)
	})
	return snap
}
func task(id, cap string, v any) protocol.TaskEnvelope {
	return protocol.TaskEnvelope{TaskID: id, TargetDeviceID: "worker-a", Capability: protocol.CapabilityRef{ID: cap, Version: "1.x"}, Input: input(v), DependsOn: []string{}}
}
func TestPhase2ParallelTestAndGitDiffWorkflow(t *testing.T) {
	e := newPhase2(t)
	exe, _ := os.Executable()
	a := task("a_exec", "process.exec", map[string]any{"program": exe, "args": []string{"--phase2-workload", "log"}, "cwd": "workspace/project", "timeout_ms": 5000})
	b := task("b_git", "git.inspect", map[string]any{"workspace": "workspace/project", "mode": "summary"})
	b.TargetDeviceID = "worker-b"
	c := task("c_summary", "report.aggregate", map[string]any{})
	c.DependsOn = []string{a.TaskID, b.TaskID}
	for _, p := range []*protocol.TaskEnvelope{&a, &b, &c} {
		p.WorkspaceContext = &protocol.WorkspaceContext{RootDir: "workspace/project", ExpectedGitHead: e.head, Policy: "require_clean_or_match"}
	}
	snap := e.run(t, a, b, c)
	if snap.Status != "succeeded" {
		t.Fatalf("workflow: %s", input(snap))
	}
	page, err := e.c.Events(e.ctx, 0, 0, 500)
	if err != nil {
		t.Fatal(err)
	}
	var log bool
	starts := map[string]time.Time{}
	ends := map[string]time.Time{}
	for _, v := range page.Events {
		if v.Type == "task.log" {
			log = true
		}
		if v.Type == "task.started" {
			starts[v.TaskID] = v.Timestamp
		}
		if v.Type == "task.succeeded" {
			ends[v.TaskID] = v.Timestamp
		}
	}
	if !log || !starts[a.TaskID].Before(ends[b.TaskID]) || !starts[b.TaskID].Before(ends[a.TaskID]) {
		t.Fatalf("no logs or parallel overlap: starts=%v ends=%v", starts, ends)
	}
	// Parallelism is measured over each task's complete running interval,
	// including required Git preflight. A millisecond Git read can legitimately
	// finish before the other worker's payload process has finished starting.
	for _, v := range snap.Tasks {
		if v.Task.TaskID == b.TaskID {
			var r struct {
				HEAD string `json:"head"`
			}
			json.Unmarshal(v.Result, &r)
			if r.HEAD != e.head {
				t.Fatalf("wrong Git baseline %s", v.Result)
			}
		}
		if v.Task.TaskID == a.TaskID {
			var r struct {
				ExitCode int    `json:"exit_code"`
				Stdout   string `json:"stdout_summary"`
			}
			json.Unmarshal(v.Result, &r)
			if r.ExitCode != 0 || !strings.Contains(r.Stdout, "real-exec-log") {
				t.Fatalf("process result %s", v.Result)
			}
		}
	}
}
func TestPhase2WorkspaceMismatchRejectsExecution(t *testing.T) {
	e := newPhase2(t)
	exe, _ := os.Executable()
	for _, mode := range []string{"wrong_head", "dirty"} {
		t.Run(mode, func(t *testing.T) {
			head := strings.Repeat("0", 40)
			if mode == "dirty" {
				head = e.head
				os.WriteFile(filepath.Join(e.root, "dirty"), []byte("dirty"), 0600)
			}
			a := task("mismatch_"+mode, "process.exec", map[string]any{"program": exe, "args": []string{"--phase2-workload", "marker"}, "cwd": "workspace/project"})
			a.WorkspaceContext = &protocol.WorkspaceContext{RootDir: "workspace/project", ExpectedGitHead: head, Policy: "require_clean_or_match"}
			snap := e.run(t, a)
			if snap.Status != "failed" || snap.Tasks[0].Error == nil || snap.Tasks[0].Error.Code != "WORKSPACE_MISMATCH" {
				t.Fatalf("mismatch outcome %s", input(snap))
			}
			if _, err := os.Stat(filepath.Join(e.root, "must-not-exist")); !os.IsNotExist(err) {
				t.Fatal("rejected program was started")
			}
		})
	}
}
func TestPhase2CancelKillsChildProcessTree(t *testing.T) {
	e := newPhase2(t)
	exe, _ := os.Executable()
	a := task("tree", "process.exec", map[string]any{"program": exe, "args": []string{"--phase2-workload", "spawn"}, "cwd": "workspace/project", "timeout_ms": 60000})
	p := protocol.PlanEnvelope{PlanID: "tree_plan", IdempotencyKey: "tree:key", Goal: "cancel tree", ConcurrencyLimit: 1, CreatedAt: time.Now().UTC(), Tasks: []protocol.TaskEnvelope{a}}
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
	start := time.Now()
	if _, err := e.c.Control(e.ctx, p.PlanID, "cancel"); err != nil {
		t.Fatal(err)
	}
	await(t, e.ctx, func() bool { s, err := e.c.Plan(e.ctx, p.PlanID); return err == nil && s.Status == "cancelled" })
	if time.Since(start) > 2*time.Second {
		t.Fatal("tree cancellation exceeded 2 seconds")
	}
	if childAlive(pid) {
		t.Fatalf("live orphan child %d", pid)
	}
}
func TestPhase2FilesystemReadWriteConfinement(t *testing.T) {
	e := newPhase2(t)
	write := task("write", "filesystem.write", map[string]any{"workspace": "workspace/project", "path": "out/note.txt", "content": "hello", "overwrite": false})
	read := task("read", "filesystem.read", map[string]any{"workspace": "workspace/project", "path": "out/note.txt"})
	read.DependsOn = []string{write.TaskID}
	snap := e.run(t, write, read)
	if snap.Status != "succeeded" {
		t.Fatalf("files %s", input(snap))
	}
	for _, s := range snap.Tasks {
		if s.Task.TaskID == "read" {
			var r struct{ Content, SHA256 string }
			json.Unmarshal(s.Result, &r)
			if r.Content != "hello" || r.SHA256 != "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824" {
				t.Fatalf("read/hash %s", s.Result)
			}
		}
	}
	bad := task("escape", "filesystem.read", map[string]any{"workspace": "workspace/project", "path": "../escape"})
	snap = e.run(t, bad)
	if snap.Tasks[0].Error == nil || snap.Tasks[0].Error.Code != "PATH_OUTSIDE_WORKSPACE" {
		t.Fatalf("escape outcome %s", input(snap))
	}
	outside := t.TempDir()
	if os.Symlink(outside, filepath.Join(e.root, "outside")) == nil {
		bad.TaskID = "symlink"
		bad.Input = input(map[string]any{"workspace": "workspace/project", "path": "outside/secret"})
		snap = e.run(t, bad)
		if snap.Tasks[0].Error == nil || snap.Tasks[0].Error.Code != "PATH_OUTSIDE_WORKSPACE" {
			t.Fatal("symlink escaped")
		}
	}
}
func TestPhase2NonzeroTimeoutAndAllowlist(t *testing.T) {
	e := newPhase2(t)
	exe, _ := os.Executable()
	for _, tc := range []struct {
		id, mode, program, code string
		timeout                 int
	}{{"nonzero", "fail", exe, "PROCESS_EXIT_NONZERO", 5000}, {"timeout", "sleep", exe, "DEADLINE_EXCEEDED", 150}, {"blocked", "marker", "never-allowed-program", "PROGRAM_NOT_ALLOWED", 5000}} {
		t.Run(tc.id, func(t *testing.T) {
			job := task(tc.id, "process.exec", map[string]any{"program": tc.program, "args": []string{"--phase2-workload", tc.mode}, "cwd": "workspace/project", "timeout_ms": tc.timeout})
			snap := e.run(t, job)
			if snap.Status != "failed" || snap.Tasks[0].Error == nil || snap.Tasks[0].Error.Code != tc.code {
				t.Fatalf("error=%s", input(snap))
			}
			if tc.id == "nonzero" {
				var r struct {
					Code   int    `json:"exit_code"`
					Stderr string `json:"stderr_summary"`
				}
				json.Unmarshal(snap.Tasks[0].Result, &r)
				if r.Code != 7 || !strings.Contains(r.Stderr, "expected failure") {
					t.Fatalf("lost failure details %s", snap.Tasks[0].Result)
				}
			}
		})
	}
}
func TestPhase2AgentRestartResumesProcessLogs(t *testing.T) {
	e := newPhase2(t)
	exe, _ := os.Executable()
	job := task("restart_exec", "process.exec", map[string]any{"program": exe, "args": []string{"--phase2-workload", "log-long"}, "cwd": "workspace/project", "timeout_ms": 6000})
	job.WorkspaceContext = &protocol.WorkspaceContext{RootDir: "workspace/project", ExpectedGitHead: e.head, Policy: "require_clean_or_match"}
	plan := protocol.PlanEnvelope{PlanID: "restart_process", IdempotencyKey: "restart:process", Goal: "resume live process", ConcurrencyLimit: 1, CreatedAt: time.Now().UTC(), Tasks: []protocol.TaskEnvelope{job}}
	if _, err := e.c.Submit(e.ctx, plan); err != nil {
		t.Fatal(err)
	}
	await(t, e.ctx, func() bool {
		page, err := e.c.Events(e.ctx, 0, 0, 500)
		if err != nil {
			return false
		}
		for _, ev := range page.Events {
			if ev.Type == "task.log" && strings.Contains(string(ev.Payload), "restart-start") {
				return true
			}
		}
		return false
	})
	if err := e.a.Close(); err != nil {
		t.Fatal(err)
	}
	restored, err := agent.Open(e.cfgA)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restored.Close() })
	go restored.Run(e.ctx)
	var snap protocol.PlanSnapshot
	await(t, e.ctx, func() bool {
		var err error
		snap, err = e.c.Plan(e.ctx, plan.PlanID)
		return err == nil && protocol.Terminal(snap.Status)
	})
	if snap.Status != "succeeded" {
		t.Fatalf("live process lost on restart %s", input(snap))
	}
	if n, err := restored.ExecutionCount(job.TaskID); err != nil || n != 1 {
		t.Fatalf("process reran: %d %v", n, err)
	}
	page, err := e.c.Events(e.ctx, 0, 0, 500)
	if err != nil {
		t.Fatal(err)
	}
	text := ""
	next := int64(0)
	for _, ev := range page.Events {
		if ev.TaskID == job.TaskID && ev.Type == "task.log" {
			var payload protocol.EventPayload
			json.Unmarshal(ev.Payload, &payload)
			if payload.Stream == "stdout" {
				if payload.Offset != next {
					t.Fatalf("log offsets overlapped or skipped: %d != %d", payload.Offset, next)
				}
				next += payload.Length
				text += payload.Text
			}
		}
	}
	if !strings.Contains(text, "restart-start") || !strings.Contains(text, "restart-finish") {
		t.Fatalf("lost recovered logs %s", text)
	}
}
func TestPhase2CancelledProcessDrainsLogBacklog(t *testing.T) {
	// The 8 MiB event integrity stress is deliberately separate from the hard
	// two-second process-tree stop test; -race instruments the pure-Go SQLite VM.
	e := newPhase2(t, 60*time.Second)
	defer func() {
		if t.Failed() {
			db, err := sql.Open("sqlite", e.cfgA.DBPath)
			if err == nil {
				defer db.Close()
				var offset, generated, acked, pending int64
				var path, status string
				db.QueryRow(`SELECT stdout_offset,stdout_path,status FROM local_task_ledger WHERE id='burst_logs'`).Scan(&offset, &path, &status)
				db.QueryRow(`SELECT worker_seq,last_ack FROM counters WHERE id=1`).Scan(&generated, &acked)
				db.QueryRow(`SELECT COUNT(*) FROM event_outbox`).Scan(&pending)
				var size int64
				if info, err := os.Stat(path); err == nil {
					size = info.Size()
				}
				t.Logf("backlog diagnosis status=%s offset=%d/%d generated=%d ack=%d pending=%d agent_error=%v", status, offset, size, generated, acked, pending, e.a.LastError())
			}
		}
	}()
	exe, _ := os.Executable()
	job := task("burst_logs", "process.exec", map[string]any{"program": exe, "args": []string{"--phase2-workload", "burst"}, "cwd": "workspace/project", "timeout_ms": 60000})
	p := protocol.PlanEnvelope{PlanID: "burst_plan", IdempotencyKey: "burst:key", Goal: "cancel backlog", ConcurrencyLimit: 1, CreatedAt: time.Now().UTC(), Tasks: []protocol.TaskEnvelope{job}}
	if _, err := e.c.Submit(e.ctx, p); err != nil {
		t.Fatal(err)
	}
	await(t, e.ctx, func() bool { _, err := os.Stat(filepath.Join(e.root, "burst-ready")); return err == nil })
	e.c.Control(e.ctx, p.PlanID, "cancel")
	await(t, e.ctx, func() bool {
		time.Sleep(100 * time.Millisecond)
		s, err := e.c.Plan(e.ctx, p.PlanID)
		return err == nil && s.Status == "cancelled"
	})
	db, err := sql.Open("sqlite", e.cfgA.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var offset int64
	var path string
	if err = db.QueryRow(`SELECT stdout_offset,stdout_path FROM local_task_ledger WHERE id=?`, job.TaskID).Scan(&offset, &path); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if offset != info.Size() || offset < 8<<20 {
		t.Fatalf("cancelled task lost durable log events: offset=%d file=%d", offset, info.Size())
	}
}
func TestPhase2CancellationPersistsAcrossAgentRestart(t *testing.T) {
	e := newPhase2(t)
	exe, _ := os.Executable()
	job := task("persist_cancel", "process.exec", map[string]any{"program": exe, "args": []string{"--phase2-workload", "sleep"}, "cwd": "workspace/project", "timeout_ms": 60000})
	p := protocol.PlanEnvelope{PlanID: "persist_cancel_plan", IdempotencyKey: "persist:cancel", Goal: "recover cancellation", ConcurrencyLimit: 1, CreatedAt: time.Now().UTC(), Tasks: []protocol.TaskEnvelope{job}}
	e.c.Submit(e.ctx, p)
	await(t, e.ctx, func() bool {
		page, err := e.c.Events(e.ctx, 0, 0, 500)
		if err != nil {
			return false
		}
		for _, ev := range page.Events {
			if ev.TaskID == job.TaskID && ev.Type == "task.log" {
				return true
			}
		}
		return false
	})
	db, err := sql.Open("sqlite", e.cfgA.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO cancellations(task_id) VALUES(?)`, job.TaskID)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	e.a.Close()
	restored, err := agent.Open(e.cfgA)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { restored.Close() })
	go restored.Run(e.ctx)
	t.Cleanup(func() {
		stop, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		e.c.Control(stop, p.PlanID, "cancel")
		for stop.Err() == nil {
			s, err := e.c.Plan(stop, p.PlanID)
			if err == nil && protocol.Terminal(s.Status) {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	})
	check, cancel := context.WithTimeout(e.ctx, 2*time.Second)
	defer cancel()
	await(t, check, func() bool { s, err := e.c.Plan(check, p.PlanID); return err == nil && s.Status == "cancelled" })
}
func TestPhase2ProcessCancelCapability(t *testing.T) {
	e := newPhase2(t)
	exe, _ := os.Executable()
	job := task("local_target", "process.exec", map[string]any{"program": exe, "args": []string{"--phase2-workload", "sleep"}, "cwd": "workspace/project", "timeout_ms": 60000})
	p := protocol.PlanEnvelope{PlanID: "local_target_plan", IdempotencyKey: "local:target", Goal: "cancel local process", ConcurrencyLimit: 1, CreatedAt: time.Now().UTC(), Tasks: []protocol.TaskEnvelope{job}}
	if _, err := e.c.Submit(e.ctx, p); err != nil {
		t.Fatal(err)
	}
	await(t, e.ctx, func() bool {
		page, err := e.c.Events(e.ctx, 0, 0, 500)
		if err != nil {
			return false
		}
		for _, ev := range page.Events {
			if ev.TaskID == job.TaskID && ev.Type == "task.log" {
				return true
			}
		}
		return false
	})
	cancelTask := task("local_cancel", "process.cancel", map[string]any{"task_id": job.TaskID})
	result := e.run(t, cancelTask)
	if result.Status != "succeeded" {
		t.Fatalf("process.cancel failed %s", input(result))
	}
	await(t, e.ctx, func() bool { s, err := e.c.Plan(e.ctx, p.PlanID); return err == nil && s.Status == "cancelled" })
}
func TestPhase2GitInspectShowsDirtyDiff(t *testing.T) {
	e := newPhase2(t)
	os.WriteFile(filepath.Join(e.root, "README.md"), []byte("changed\n"), 0600)
	result := e.run(t, task("dirty_diff", "git.inspect", map[string]any{"workspace": "workspace/project", "mode": "diff"}))
	if result.Status != "succeeded" {
		t.Fatalf("Git diff failed %s", input(result))
	}
	var report struct {
		HEAD         string `json:"head"`
		Status, Diff string
	}
	json.Unmarshal(result.Tasks[0].Result, &report)
	if report.HEAD != e.head || !strings.Contains(report.Status, "README.md") || !strings.Contains(report.Diff, "+changed") {
		t.Fatalf("dirty diff missing %s", result.Tasks[0].Result)
	}
}
