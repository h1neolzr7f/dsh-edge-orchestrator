package agent

import (
	"context"
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
	for _, name := range []string{"stdout.log", "stderr.log", "exit.json"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("keep"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.cleanupTaskWorkspace(task); err != nil {
		t.Fatal(err)
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
