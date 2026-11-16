package agent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

type preparedWorkspace struct{ Logical, Host string }
type workspaceKey struct{}

func configureLocal(cfg *Config) (string, map[string]string, error) {
	data, e := filepath.Abs(filepath.Dir(cfg.DBPath))
	if e != nil {
		return "", nil, e
	}
	// Windows temporary paths may use an 8.3 alias while EvalSymlinks returns
	// its long spelling. Keep task worktrees and authorized roots canonical.
	data, e = filepath.EvalSymlinks(data)
	if e != nil {
		return "", nil, e
	}
	if len(cfg.Workspaces) == 0 {
		root := filepath.Join(data, "workspaces", "default")
		cfg.Workspaces = map[string]string{"workspace/default": root, "workspace/project": root}
	}
	copyRoots := map[string]string{}
	for logical, host := range cfg.Workspaces {
		if logical == "" || strings.Contains(logical, "..") || filepath.IsAbs(logical) {
			return "", nil, fmt.Errorf("invalid logical workspace %s", logical)
		}
		if !filepath.IsAbs(host) {
			return "", nil, fmt.Errorf("workspace host path must be absolute")
		}
		if e = os.MkdirAll(host, 0700); e != nil {
			return "", nil, e
		}
		real, e := filepath.EvalSymlinks(host)
		if e != nil {
			return "", nil, e
		}
		copyRoots[logical] = real
	}
	cfg.Workspaces = copyRoots
	if cfg.AllowedPrograms == nil {
		cfg.AllowedPrograms = []string{"git", "go", "python", "python3", "pytest", "node", "npm", "sh", "bash", "cmd", "powershell"}
	}
	programs := map[string]string{}
	for _, name := range cfg.AllowedPrograms {
		resolved, e := exec.LookPath(name)
		if e != nil {
			continue
		}
		resolved, e = filepath.Abs(resolved)
		if e != nil {
			return "", nil, e
		}
		key := name
		if runtime.GOOS == "windows" {
			key = strings.TrimSuffix(strings.ToLower(key), ".exe")
		}
		programs[key] = resolved
	}
	return data, programs, nil
}
func confinedPath(root, rel string) (string, error) {
	deny := func() (string, error) {
		return "", protocol.Err("PATH_OUTSIDE_WORKSPACE", "path must remain within an authorized workspace")
	}
	rel = strings.ReplaceAll(rel, "\\", "/")
	if strings.HasPrefix(rel, "/") || filepath.IsAbs(rel) || strings.ContainsAny(rel, ":\x00") {
		return deny()
	}
	for _, part := range strings.Split(rel, "/") {
		if part == ".." {
			return deny()
		}
	}
	if rel == "" {
		rel = "."
	}
	candidate := filepath.Join(root, filepath.FromSlash(rel))
	probe := candidate
	var suffix []string
	for {
		_, e := os.Lstat(probe)
		if e == nil {
			break
		}
		if !errors.Is(e, os.ErrNotExist) {
			return "", e
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return deny()
		}
		suffix = append(suffix, filepath.Base(probe))
		probe = parent
	}
	actual, e := filepath.EvalSymlinks(probe)
	if e != nil {
		return deny()
	}
	for i := len(suffix) - 1; i >= 0; i-- {
		actual = filepath.Join(actual, suffix[i])
	}
	inside, e := filepath.Rel(root, actual)
	if e != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) || filepath.IsAbs(inside) {
		return deny()
	}
	return filepath.Clean(actual), nil
}
func (a *Agent) resolveWorkspacePath(logicalRoot, relPath string) (string, error) {
	root, ok := a.cfg.Workspaces[logicalRoot]
	if !ok {
		return "", protocol.Err("PATH_OUTSIDE_WORKSPACE", "workspace is not authorized")
	}
	return confinedPath(root, relPath)
}
func (a *Agent) taskPath(ctx context.Context, logical, rel string) (string, error) {
	if bound, ok := ctx.Value(workspaceKey{}).(preparedWorkspace); ok {
		if logical != bound.Logical {
			return "", protocol.Err("WORKSPACE_MISMATCH", "input workspace differs from checked workspace")
		}
		return confinedPath(bound.Host, rel)
	}
	return a.resolveWorkspacePath(logical, rel)
}
func (a *Agent) taskRoot(ctx context.Context, logical string) (string, error) {
	if logical == "" {
		logical = "workspace/project"
		if bound, ok := ctx.Value(workspaceKey{}).(preparedWorkspace); ok {
			logical = bound.Logical
		}
	}
	return a.taskPath(ctx, logical, ".")
}
func (a *Agent) processDir(ctx context.Context, cwd string) (string, error) {
	if cwd == "" {
		cwd = "workspace/project"
	}
	keys := []string{}
	for key := range a.cfg.Workspaces {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	for _, key := range keys {
		if cwd == key || strings.HasPrefix(cwd, key+"/") {
			return a.taskPath(ctx, key, strings.TrimPrefix(strings.TrimPrefix(cwd, key), "/"))
		}
	}
	return "", protocol.Err("PATH_OUTSIDE_WORKSPACE", "cwd is not a mapped workspace")
}
func (a *Agent) taskDir(id string) string {
	h := sha256.Sum256([]byte(id))
	return filepath.Join(a.dataDir, "runs", hex.EncodeToString(h[:]))
}

type cappedBuffer struct {
	bytes.Buffer
	Limit     int
	Truncated bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	left := b.Limit - b.Len()
	if left > 0 {
		if left > n {
			left = n
		}
		b.Buffer.Write(p[:left])
	}
	if n > left {
		b.Truncated = true
	}
	return n, nil
}
func gitOutput(ctx context.Context, root string, args ...string) (string, error) {
	all := append([]string{"--no-pager", "--no-optional-locks", "-c", "core.hooksPath=" + os.DevNull, "-c", "core.fsmonitor=false", "-C", root}, args...)
	cmd := exec.CommandContext(ctx, "git", all...)
	var out, stderr cappedBuffer
	out.Limit = 256 << 10
	stderr.Limit = 8192
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if e := cmd.Run(); e != nil {
		return "", fmt.Errorf("git failed: %s: %w", stderr.String(), e)
	}
	return out.String(), nil
}
func (a *Agent) prepareTask(ctx context.Context, t protocol.TaskEnvelope) (context.Context, error) {
	if len(t.InputArtifactBindings) > 0 {
		return ctx, protocol.Err("ARTIFACT_BINDING_FAILED", "unresolved artifact bindings")
	}
	w := t.WorkspaceContext
	if w == nil {
		if len(t.InputArtifacts) == 0 {
			return ctx, nil
		}
		dir := filepath.Join(a.taskDir(t.TaskID), "workspace")
		if e := os.RemoveAll(dir); e != nil {
			return ctx, e
		}
		if e := os.MkdirAll(dir, 0700); e != nil {
			return ctx, e
		}
		logical := a.artifactWorkspace(t)
		if _, err := a.resolveWorkspacePath(logical, "."); err != nil {
			return ctx, err
		}
		prepared := context.WithValue(ctx, workspaceKey{}, preparedWorkspace{logical, dir})
		if e := a.mountTaskArtifacts(prepared, t, dir); e != nil {
			return ctx, e
		}
		return prepared, nil
	}

	root, e := a.resolveWorkspacePath(w.RootDir, ".")
	if e != nil {
		return ctx, e
	}
	head, e := gitOutput(ctx, root, "rev-parse", "HEAD")
	if e != nil || strings.TrimSpace(head) != w.ExpectedGitHead {
		return ctx, protocol.Err("WORKSPACE_MISMATCH", "Git HEAD does not match expected_git_head")
	}
	status, e := gitOutput(ctx, root, "status", "--porcelain", "--untracked-files=all")
	if e != nil || (status != "" && w.PatchArtifactID == "") {
		return ctx, protocol.Err("WORKSPACE_MISMATCH", "working tree must be clean including untracked files")
	}
	dir := filepath.Join(a.taskDir(t.TaskID), "workspace")
	if e = os.MkdirAll(filepath.Dir(dir), 0700); e != nil {
		return ctx, e
	}
	if _, err := os.Stat(dir); err == nil {
		if e = a.cleanupTaskWorkspace(t); e != nil {
			return ctx, e
		}
	}
	if _, e = gitOutput(ctx, root, "worktree", "add", "--detach", dir, w.ExpectedGitHead); e != nil {
		return ctx, protocol.Err("WORKSPACE_MISMATCH", e.Error())
	}
	if w.PatchArtifactID != "" {
		patch, err := a.downloadArtifact(ctx, t, w.PatchArtifactID, "")
		if err != nil {
			return ctx, err
		}
		if _, err = gitOutput(ctx, dir, "apply", "--binary", "--index", patch); err != nil {
			return ctx, protocol.Err("WORKSPACE_MISMATCH", err.Error())
		}
	}
	prepared := context.WithValue(ctx, workspaceKey{}, preparedWorkspace{w.RootDir, dir})
	if e = a.mountTaskArtifacts(prepared, t, dir); e != nil {
		return ctx, e
	}
	return prepared, nil
}
func (a *Agent) allowedProgram(program string) (string, error) {
	key := program
	if runtime.GOOS == "windows" {
		key = strings.TrimSuffix(strings.ToLower(key), ".exe")
	}
	if resolved, ok := a.programs[key]; ok {
		return resolved, nil
	}
	if filepath.IsAbs(program) {
		for _, resolved := range a.programs {
			if program == resolved {
				return resolved, nil
			}
		}
	}
	return "", protocol.Err("PROGRAM_NOT_ALLOWED", "program is not in the configured allowlist")
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.r.Read(p)
}
func hashFile(ctx context.Context, f *os.File) (string, error) {
	if _, e := f.Seek(0, io.SeekStart); e != nil {
		return "", e
	}
	h := sha256.New()
	if _, e := io.Copy(h, contextReader{ctx, f}); e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func nowUTC() time.Time                            { return time.Now().UTC() }
func marshalResult(v any) (json.RawMessage, error) { b, e := json.Marshal(v); return b, e }

// Input mount paths are relative to the same directory used by process.exec.
func (a *Agent) mountTaskArtifacts(ctx context.Context, t protocol.TaskEnvelope, root string) error {
	if t.Capability.ID == "process.exec" {
		var in struct {
			CWD string `json:"cwd"`
		}
		if e := json.Unmarshal(t.Input, &in); e != nil {
			return e
		}
		dir, e := a.processDir(ctx, in.CWD)
		if e != nil {
			return e
		}
		if e = os.MkdirAll(dir, 0700); e != nil {
			return e
		}
		root = dir
	}
	return a.mountArtifacts(ctx, t, root)
}
