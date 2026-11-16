package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
)

// cleanupTaskWorkspace releases only the disposable checkout. Call after durable
// completion, log drain, and output collection/upload; runner diagnostics remain.
// It is idempotent and deliberately uses a fresh context after task cancellation.
func (a *Agent) cleanupTaskWorkspace(t protocol.TaskEnvelope) error {
	if t.WorkspaceContext == nil && len(t.InputArtifacts) == 0 {
		return nil
	}
	dir := a.taskDir(t.TaskID)
	workspace := filepath.Join(dir, "workspace")
	var ready runnerReady
	if readJSON(filepath.Join(dir, "ready.json"), &ready) == nil {
		var spec runnerSpec
		if err := readJSON(filepath.Join(dir, "spec.json"), &spec); err != nil {
			return err
		}
		if sameProcess(ready.Identity) || treeBusy(ready.Identity, spec.JobName) {
			return fmt.Errorf("workspace cleanup: runner still active")
		}
		var exit runnerExit
		if err := readJSON(filepath.Join(dir, "exit.json"), &exit); err != nil {
			return err
		}
		if !a.logsDrained(t, dir) {
			return fmt.Errorf("workspace cleanup: logs not drained")
		}
	}
	if t.WorkspaceContext == nil {
		return os.RemoveAll(workspace)
	}
	root, err := a.resolveWorkspacePath(t.WorkspaceContext.RootDir, ".")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// The checkout is agent-owned and disposable even if a task locked it.
	// Git requires force twice to remove a locked worktree deliberately.
	_, removeErr := gitOutput(ctx, root, "worktree", "remove", "--force", "--force", workspace)
	// A partial checkout or a previously removed registration still needs disk
	// removal. Preserve the run directory and its diagnostics.
	diskErr := os.RemoveAll(workspace)
	// Never globally prune a shared repository here: Git prunes even a fresh
	// administrative directory while another Worker is still running add.
	// The targeted remove owns deregistration; verify only our checkout below.
	// Only treat
	// removal errors as an idempotent success after verifying its absence.
	listed, listErr := gitOutput(ctx, root, "worktree", "list", "--porcelain", "-z")
	if diskErr != nil || listErr != nil {
		return errors.Join(removeErr, diskErr, listErr)
	}
	if len(listed) >= 256<<10 {
		return errors.Join(removeErr, fmt.Errorf("workspace cleanup: worktree listing exceeds verification limit"))
	}
	for _, field := range strings.Split(listed, "\x00") {
		if !strings.HasPrefix(field, "worktree ") {
			continue
		}
		registered := filepath.FromSlash(strings.TrimPrefix(field, "worktree "))
		if rel, err := filepath.Rel(workspace, registered); err == nil && rel == "." {
			return errors.Join(removeErr, fmt.Errorf("workspace cleanup: Git worktree registration remains for %s", workspace))
		}
	}
	return nil
}
