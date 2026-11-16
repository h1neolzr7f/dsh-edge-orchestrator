package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
)

// cleanupTaskWorkspace releases only the disposable checkout. Call after durable
// completion, log drain, and output collection/upload; runner diagnostics remain.
// It is idempotent and deliberately uses a fresh context after task cancellation.
func (a *Agent) cleanupTaskWorkspace(t protocol.TaskEnvelope) error {
	if t.WorkspaceContext == nil {
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
	root, err := a.resolveWorkspacePath(t.WorkspaceContext.RootDir, ".")
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, removeErr := gitOutput(ctx, root, "worktree", "remove", "--force", workspace)
	// A partial checkout or a previously removed registration still needs disk
	// removal followed by pruning. Preserve the run directory and its diagnostics.
	diskErr := os.RemoveAll(workspace)
	_, pruneErr := gitOutput(ctx, root, "worktree", "prune")
	if diskErr != nil || pruneErr != nil {
		return errors.Join(removeErr, diskErr, pruneErr)
	}
	return nil
}
