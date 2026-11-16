package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	edgelink "github.com/h1neolzr7f/dsh-edge-orchestrator/packages/edge-link"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	"os"
	"path/filepath"
	"strings"
)

func (a *Agent) artifactClient() *edgelink.Client {
	return edgelink.NewClient(a.cfg.CoordinatorURL, a.cfg.Token, a.cfg.HTTPClient)
}
func (a *Agent) downloadArtifact(ctx context.Context, t protocol.TaskEnvelope, id, hash string) (string, error) {
	dir := filepath.Join(a.taskDir(t.TaskID), "downloads")
	if e := os.MkdirAll(dir, 0700); e != nil {
		return "", e
	}
	sum := sha256.Sum256([]byte(id))
	path := filepath.Join(dir, hex.EncodeToString(sum[:]))
	return path, a.artifactClient().DownloadArtifactFile(ctx, id, path, hash)
}
func (a *Agent) mountArtifacts(ctx context.Context, t protocol.TaskEnvelope, root string) error {
	for _, item := range t.InputArtifacts {
		target, e := confinedPath(root, item.MountPath)
		if e != nil {
			return e
		}
		if _, e = os.Lstat(target); e == nil {
			return protocol.Err("ARTIFACT_MOUNT_CONFLICT", "mount target already exists")
		} else if !errors.Is(e, os.ErrNotExist) {
			return e
		}
		source, e := a.downloadArtifact(ctx, t, item.ArtifactID, item.SHA256)
		if e != nil {
			return e
		}
		if e = os.MkdirAll(filepath.Dir(target), 0700); e != nil {
			return e
		}
		target, e = confinedPath(root, item.MountPath)
		if e != nil {
			return e
		} // Same-volume atomic publication; staging is retained until successful mount.
		if e = os.Rename(source, target); e != nil {
			return e
		}
	}
	return nil
}
func (a *Agent) outputArtifacts(t protocol.TaskEnvelope) ([]protocol.OutputArtifact, error) {
	out := append([]protocol.OutputArtifact{}, t.OutputArtifacts...)
	if t.Capability.ID == "process.exec" {
		var in struct {
			Outputs []protocol.OutputArtifact `json:"output_artifacts"`
		}
		if e := json.Unmarshal(t.Input, &in); e != nil {
			return nil, e
		}
		out = append(out, in.Outputs...)
	}
	return out, nil
}
func (a *Agent) uploadOutputs(ctx context.Context, t protocol.TaskEnvelope) error {
	outputs, e := a.outputArtifacts(t)
	if e != nil {
		return e
	}
	for _, out := range outputs {
		if !protocol.SafePath(out.Path) || out.Name == "" || out.MediaType == "" {
			return protocol.Err("ARTIFACT_UPLOAD_FAILED", "invalid output declaration")
		}
		root, e := a.taskRoot(ctx, "")
		if t.Capability.ID == "process.exec" {
			var in struct {
				CWD string `json:"cwd"`
			}
			if err := json.Unmarshal(t.Input, &in); err != nil {
				return err
			}
			root, e = a.processDir(ctx, in.CWD)
		}
		if e != nil {
			return protocol.Err("ARTIFACT_UPLOAD_FAILED", e.Error())
		}
		path, e := confinedPath(root, out.Path)
		if e != nil {
			return protocol.Err("ARTIFACT_UPLOAD_FAILED", e.Error())
		}
		f, e := openReadRegular(path)
		if e != nil {
			return protocol.Err("ARTIFACT_UPLOAD_FAILED", e.Error())
		}
		f.Close()
		_, e = a.artifactClient().UploadArtifactFile(ctx, protocol.ArtifactUploadRequest{PlanID: t.PlanID, TaskID: t.TaskID, Name: out.Name, MediaType: out.MediaType}, path)
		if e != nil {
			if a.ctx.Err() != nil {
				return errAgentDetached
			}
			return protocol.Err("ARTIFACT_UPLOAD_FAILED", e.Error())
		}
	}
	return nil
}
func (a *Agent) executeArtifactUpload(ctx context.Context, t protocol.TaskEnvelope) (json.RawMessage, error) {
	var in struct {
		Workspace string `json:"workspace"`
		Path      string `json:"path"`
		Name      string `json:"name"`
		Media     string `json:"media_type"`
	}
	if e := protocol.Decode(t.Input, &in); e != nil {
		return nil, e
	}
	if !protocol.SafePath(in.Path) || in.Name == "" || in.Media == "" {
		return nil, protocol.Err("INVALID_PAYLOAD", "path, name and media_type required")
	}
	path, e := a.taskPath(ctx, in.Workspace, in.Path)
	if e != nil {
		return nil, e
	}
	ref, e := a.artifactClient().UploadArtifactFile(ctx, protocol.ArtifactUploadRequest{PlanID: t.PlanID, TaskID: t.TaskID, Name: in.Name, MediaType: in.Media}, path)
	if e != nil {
		return nil, protocol.Err("ARTIFACT_UPLOAD_FAILED", e.Error())
	}
	return marshalResult(ref)
}

// workspaceForResume restores only the task's owned checkout mapping; it never
// repeats preflight or applies a patch again after user execution was claimed.
func (a *Agent) workspaceForResume(ctx context.Context, t protocol.TaskEnvelope) context.Context {
	logical := a.artifactWorkspace(t)
	if t.WorkspaceContext != nil || len(t.InputArtifacts) > 0 {
		return context.WithValue(ctx, workspaceKey{}, preparedWorkspace{logical, filepath.Join(a.taskDir(t.TaskID), "workspace")})
	}
	return ctx
}

func (a *Agent) artifactWorkspace(t protocol.TaskEnvelope) string {
	if t.WorkspaceContext != nil {
		return t.WorkspaceContext.RootDir
	}
	var in struct {
		Workspace string `json:"workspace"`
		CWD       string `json:"cwd"`
	}
	_ = json.Unmarshal(t.Input, &in)
	if in.Workspace != "" {
		return in.Workspace
	}
	logical := ""
	for key := range a.cfg.Workspaces {
		if (in.CWD == key || strings.HasPrefix(in.CWD, key+"/")) && len(key) > len(logical) {
			logical = key
		}
	}
	if _, ok := a.cfg.Workspaces[in.CWD]; ok {
		return in.CWD
	}
	if logical == "" {
		logical = "workspace/project"
	}
	return logical
}
