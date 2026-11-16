package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func capabilitySchemas() map[string]string {
	return map[string]string{
		"artifact.upload": `{"type":"object","required":["workspace","path","name","media_type"]}`,
		"device.inspect":  `{"type":"object"}`, "report.aggregate": `{"type":"object"}`,
		"process.exec":     `{"type":"object","additionalProperties":false,"required":["program","args","cwd"],"properties":{"program":{"type":"string"},"args":{"type":"array","items":{"type":"string"}},"cwd":{"type":"string"},"timeout_ms":{"type":"integer","minimum":1},"output_artifacts":{"type":"array","items":{"type":"object","required":["path","name","media_type"]}}}}`,
		"process.cancel":   `{"type":"object","additionalProperties":false,"required":["task_id"],"properties":{"task_id":{"type":"string"}}}`,
		"filesystem.read":  `{"type":"object","additionalProperties":false,"required":["workspace","path"],"properties":{"workspace":{"type":"string"},"path":{"type":"string"},"offset":{"type":"integer","minimum":0},"max_bytes":{"type":"integer","minimum":1,"maximum":262144}}}`,
		"filesystem.write": `{"type":"object","additionalProperties":false,"required":["workspace","path","content"],"properties":{"workspace":{"type":"string"},"path":{"type":"string"},"content":{"type":"string"},"overwrite":{"type":"boolean"}}}`,
		"git.inspect":      `{"type":"object","additionalProperties":false,"required":["workspace","mode"],"properties":{"workspace":{"type":"string"},"mode":{"enum":["status","diff","log","summary"]},"max_count":{"type":"integer","minimum":1,"maximum":1000}}}`}
}
func (a *Agent) executeLocal(ctx context.Context, t protocol.TaskEnvelope) (json.RawMessage, error) {
	switch t.Capability.ID {
	case "filesystem.read":
		var in struct {
			Workspace, Path string
			Offset          int64
			MaxBytes        int `json:"max_bytes"`
		}
		if e := protocol.Decode(t.Input, &in); e != nil {
			return nil, e
		}
		if in.Workspace == "" || in.Path == "" {
			return nil, protocol.Err("INVALID_PAYLOAD", "workspace and path are required")
		}
		if in.Offset < 0 || in.MaxBytes < 0 {
			return nil, protocol.Err("INVALID_PAYLOAD", "negative read bounds")
		}
		if in.MaxBytes == 0 {
			in.MaxBytes = 256 << 10
		}
		if in.MaxBytes > 256<<10 {
			return nil, protocol.Err("INVALID_PAYLOAD", "read maximum is 256 KiB")
		}
		path, e := a.taskPath(ctx, in.Workspace, in.Path)
		if e != nil {
			return nil, e
		}
		f, e := openReadRegular(path)
		if errors.Is(e, os.ErrNotExist) {
			return nil, protocol.Err("FILE_NOT_FOUND", in.Path)
		}
		if e != nil {
			return nil, e
		}
		defer f.Close()
		info, e := f.Stat()
		if e != nil {
			return nil, e
		}
		digest, e := hashFile(ctx, f)
		if e != nil {
			return nil, e
		}
		if _, e = f.Seek(in.Offset, io.SeekStart); e != nil {
			return nil, e
		}
		content, e := io.ReadAll(io.LimitReader(f, int64(in.MaxBytes)))
		if e != nil {
			return nil, e
		}
		return marshalResult(map[string]any{"path": in.Path, "content": string(content), "truncated": in.Offset+int64(len(content)) < info.Size(), "size_bytes": info.Size(), "sha256": digest})
	case "filesystem.write":
		var in struct {
			Workspace, Path string
			Content         *string
			Overwrite       bool
		}
		if e := protocol.Decode(t.Input, &in); e != nil {
			return nil, e
		}
		if in.Workspace == "" || in.Path == "" || in.Content == nil {
			return nil, protocol.Err("INVALID_PAYLOAD", "workspace, path and content are required")
		}
		path, e := a.taskPath(ctx, in.Workspace, in.Path)
		if e != nil {
			return nil, e
		}
		if !in.Overwrite {
			if _, e = os.Lstat(path); e == nil {
				return nil, protocol.Err("FILE_ALREADY_EXISTS", in.Path)
			} else if !errors.Is(e, os.ErrNotExist) {
				return nil, e
			}
		}
		if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
			return nil, e
		}
		path, e = a.taskPath(ctx, in.Workspace, in.Path)
		if e != nil {
			return nil, e
		}
		f, e := os.CreateTemp(filepath.Dir(path), ".edge-write-*")
		if e != nil {
			return nil, e
		}
		temp := f.Name()
		defer os.Remove(temp)
		if _, e = f.WriteString(*in.Content); e == nil {
			e = f.Sync()
		}
		closeErr := f.Close()
		if e != nil {
			return nil, e
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if e = ctx.Err(); e != nil {
			return nil, e
		}
		if in.Overwrite {
			e = os.Rename(temp, path)
		} else {
			e = os.Link(temp, path)
		}
		if errors.Is(e, os.ErrExist) {
			return nil, protocol.Err("FILE_ALREADY_EXISTS", in.Path)
		}
		if e != nil {
			return nil, e
		}
		digest := sha256.Sum256([]byte(*in.Content))
		return marshalResult(map[string]any{"path": in.Path, "size_bytes": len([]byte(*in.Content)), "sha256": hex.EncodeToString(digest[:])})
	case "git.inspect":
		return a.inspectGit(ctx, t)
	case "process.exec":
		return a.runProcess(ctx, t)
	case "process.cancel":
		var in protocol.TaskRequest
		if e := protocol.Decode(t.Input, &in); e != nil {
			return nil, e
		}
		if !protocol.ValidID(in.TaskID) || in.TaskID == t.TaskID {
			return nil, protocol.Err("INVALID_TASK", "invalid process cancellation target")
		}
		a.mu.Lock()
		var raw string
		e := a.db.QueryRow(`SELECT payload FROM local_task_ledger WHERE id=?`, in.TaskID).Scan(&raw)
		a.mu.Unlock()
		if e != nil {
			return nil, protocol.Err("TASK_NOT_FOUND", in.TaskID)
		}
		var target protocol.TaskEnvelope
		if e = json.Unmarshal([]byte(raw), &target); e != nil {
			return nil, e
		}
		if target.Capability.ID != "process.exec" {
			return nil, protocol.Err("INVALID_TASK", "target is not a process task")
		}
		if e = a.cancelTask(in.TaskID); e != nil {
			return nil, e
		}
		return marshalResult(map[string]any{"task_id": in.TaskID, "cancel_requested": true})
	}
	return nil, protocol.Err("UNSUPPORTED_CAPABILITY", t.Capability.ID)
}
func (a *Agent) inspectGit(ctx context.Context, t protocol.TaskEnvelope) (json.RawMessage, error) {
	var in struct {
		Workspace, Mode string
		MaxCount        int `json:"max_count"`
	}
	if e := protocol.Decode(t.Input, &in); e != nil {
		return nil, e
	}
	if in.Mode != "summary" && in.Mode != "status" && in.Mode != "diff" && in.Mode != "log" {
		return nil, protocol.Err("INVALID_PAYLOAD", "invalid Git mode")
	}
	if in.MaxCount == 0 {
		in.MaxCount = 10
	}
	if in.MaxCount < 1 || in.MaxCount > 1000 {
		return nil, protocol.Err("INVALID_PAYLOAD", "max_count must be 1..1000")
	}
	root, e := a.taskRoot(ctx, in.Workspace)
	if e != nil {
		return nil, e
	}
	started := nowUTC()
	head, e := gitOutput(ctx, root, "rev-parse", "HEAD")
	if e != nil {
		return nil, protocol.Err("NOT_A_GIT_REPO", e.Error())
	}
	branch, e := gitOutput(ctx, root, "rev-parse", "--abbrev-ref", "HEAD")
	if e != nil {
		return nil, e
	}
	status, e := gitOutput(ctx, root, "status", "--porcelain", "--untracked-files=all")
	if e != nil {
		return nil, e
	}
	stat, e := gitOutput(ctx, root, "diff", "--no-ext-diff", "--no-textconv", "--stat", "HEAD")
	if e != nil {
		return nil, e
	}
	result := map[string]any{"head": strings.TrimSpace(head), "branch": strings.TrimSpace(branch), "status": status, "diff_stat": stat, "started_at": started}
	if in.Mode == "diff" || in.Mode == "summary" {
		diff, e := gitOutput(ctx, root, "diff", "--no-ext-diff", "--no-textconv", "HEAD")
		if e != nil {
			return nil, e
		}
		truncated := len(diff) > 65536
		if truncated {
			diff = diff[:65536]
		}
		result["diff"] = diff
		result["diff_truncated"] = truncated
	}
	if in.Mode == "log" || in.Mode == "summary" {
		log, e := gitOutput(ctx, root, "log", "--no-show-signature", "--max-count="+strconv.Itoa(in.MaxCount), "--format=%H%x09%s")
		if e != nil {
			return nil, e
		}
		commits := []map[string]string{}
		for _, line := range strings.Split(strings.TrimSpace(log), "\n") {
			parts := strings.SplitN(line, "\t", 2)
			if len(parts) == 2 {
				commits = append(commits, map[string]string{"sha": parts[0], "subject": parts[1]})
			}
		}
		result["commits"] = commits
	}
	result["finished_at"] = nowUTC()
	result["duration_ms"] = time.Since(started).Milliseconds()
	return marshalResult(result)
}
