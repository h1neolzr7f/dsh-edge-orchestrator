package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
)

func TestCleanupTaskWorkspaceRemovesOwnedLockedRegistration(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	git := func(args ...string) string {
		t.Helper()
		out, err := gitOutput(ctx, root, args...)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "initial")
	// Use production initialization so both the authorized root and run directory
	// receive Windows short-path canonicalization before confinement checks.
	a, err := Open(Config{DBPath: filepath.Join(t.TempDir(), "agent.db"), DeviceID: "cleanup_worker", Token: "test-token", CoordinatorURL: "https://localhost", Workspaces: map[string]string{"workspace/project": root}, AllowedPrograms: []string{"git"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	root = a.cfg.Workspaces["workspace/project"]
	task := protocol.TaskEnvelope{TaskID: "locked_cleanup", WorkspaceContext: &protocol.WorkspaceContext{RootDir: "workspace/project", ExpectedGitHead: strings.TrimSpace(git("rev-parse", "HEAD"))}}
	if _, err := a.prepareTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	dir := a.taskDir(task.TaskID)
	workspace := filepath.Join(dir, "workspace")
	git("worktree", "lock", "--reason", "task-created lock", workspace)
	// Another process is between creation of the administrative directory and
	// publication of its gitdir/commondir. Cleanup must not prune its metadata.
	unrelated := filepath.Join(root, ".git", "worktrees", "in_progress_other")
	if err := os.MkdirAll(unrelated, 0700); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"stdout.log", "stderr.log", "exit.json"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("keep"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.cleanupTaskWorkspace(task); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatal("cleanup pruned another in-progress worktree", err)
	}
	if _, err := os.Stat(workspace); !os.IsNotExist(err) {
		t.Fatalf("workspace remains: %v", err)
	}
	if out := git("worktree", "list", "--porcelain"); strings.Contains(out, filepath.ToSlash(workspace)) {
		t.Fatalf("locked registration survived cleanup: %s", out)
	}
	for _, name := range []string{"stdout.log", "stderr.log", "exit.json"} {
		if b, err := os.ReadFile(filepath.Join(dir, name)); err != nil || string(b) != "keep" {
			t.Fatalf("diagnostic %s lost: %q %v", name, b, err)
		}
	}
	if err := a.cleanupTaskWorkspace(task); err != nil {
		t.Fatalf("repeated cleanup: %v", err)
	}
}

func TestRecoveryRetriesTerminalAndInterruptedWorkspaceCleanup(t *testing.T) {
	for _, terminal := range []bool{true, false} {
		t.Run(fmt.Sprint(terminal), func(t *testing.T) {
			root := t.TempDir()
			ctx := context.Background()
			git := func(args ...string) string {
				v, e := gitOutput(ctx, root, args...)
				if e != nil {
					t.Fatal(e)
				}
				return v
			}
			git("init", "-q")
			os.WriteFile(filepath.Join(root, "file"), []byte("base"), 0600)
			git("add", ".")
			git("-c", "user.name=Fixture", "-c", "user.email=test@example.invalid", "commit", "-qm", "initial")
			cfg := Config{DBPath: filepath.Join(t.TempDir(), "a.db"), DeviceID: "w", Token: "worker", CoordinatorURL: "https://localhost", Workspaces: map[string]string{"workspace/project": root}}
			a, e := Open(cfg)
			if e != nil {
				t.Fatal(e)
			}
			task := protocol.TaskEnvelope{TaskID: "recover_cleanup", PlanID: "p", TargetDeviceID: "w", IdempotencyKey: "cleanup", Capability: protocol.CapabilityRef{ID: "git.inspect", Version: "1.x"}, Input: json.RawMessage(`{"workspace":"workspace/project","mode":"status"}`), DependsOn: []string{}, WorkspaceContext: &protocol.WorkspaceContext{RootDir: "workspace/project", ExpectedGitHead: strings.TrimSpace(git("rev-parse", "HEAD")), Policy: "require_clean_or_match"}}
			a.Accept(task)
			if _, e = a.prepareTask(ctx, task); e != nil {
				t.Fatal(e)
			}
			a.claim(task)
			dir := a.taskDir(task.TaskID)
			os.WriteFile(filepath.Join(dir, "stdout.log"), []byte("diagnostic"), 0600)
			if terminal {
				if e = a.finish(task, json.RawMessage(`{}`), nil, false); e != nil {
					t.Fatal(e)
				}
			}
			a.Close()
			a, e = Open(cfg)
			if e != nil {
				t.Fatal(e)
			}
			defer a.Close()
			if _, e = a.recoverTasks(); e != nil {
				t.Fatal(e)
			}
			if _, e = os.Stat(filepath.Join(dir, "workspace")); !os.IsNotExist(e) {
				t.Fatal("recovery leaked checkout", e)
			}
			if b, e := os.ReadFile(filepath.Join(dir, "stdout.log")); e != nil || string(b) != "diagnostic" {
				t.Fatal("diagnostics lost")
			}
			if out := git("worktree", "list", "--porcelain"); strings.Contains(out, filepath.ToSlash(filepath.Join(dir, "workspace"))) {
				t.Fatal("registration retained")
			}
		})
	}
}
