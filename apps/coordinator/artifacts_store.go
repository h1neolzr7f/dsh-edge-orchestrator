package coordinator

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	"io"
	"os"
	"strings"
	"time"
)

const artifactSchema = `CREATE TABLE IF NOT EXISTS artifacts(id TEXT PRIMARY KEY,plan_id TEXT NOT NULL,task_id TEXT NOT NULL,device_id TEXT NOT NULL,name TEXT NOT NULL,media_type TEXT NOT NULL,size_bytes INTEGER NOT NULL,sha256 TEXT NOT NULL,storage_path TEXT NOT NULL,created_at TEXT NOT NULL,UNIQUE(task_id,name));
CREATE TABLE IF NOT EXISTS artifact_uploads(upload_id TEXT PRIMARY KEY,artifact_id TEXT NOT NULL,plan_id TEXT NOT NULL,task_id TEXT NOT NULL,device_id TEXT NOT NULL,name TEXT NOT NULL,media_type TEXT NOT NULL,total_size INTEGER NOT NULL,expected_sha256 TEXT NOT NULL,chunk_size INTEGER NOT NULL,confirmed_offset INTEGER NOT NULL DEFAULT 0,temp_path TEXT NOT NULL,completed INTEGER NOT NULL DEFAULT 0,UNIQUE(task_id,name));`

type artifactUpload struct {
	UploadID                                    string `json:"upload_id"`
	ArtifactID                                  string `json:"artifact_id"`
	ChunkSize                                   int    `json:"chunk_size"`
	Offset                                      int64  `json:"confirmed_offset"`
	Plan, Task, Device, Name, Media, Hash, Path string
	Size                                        int64
	Completed                                   bool
}

func (s *Server) uploadBy(where string, args ...any) (u artifactUpload, e error) {
	e = s.db.QueryRow(`SELECT upload_id,artifact_id,chunk_size,confirmed_offset,plan_id,task_id,device_id,name,media_type,expected_sha256,temp_path,total_size,completed FROM artifact_uploads WHERE `+where, args...).Scan(&u.UploadID, &u.ArtifactID, &u.ChunkSize, &u.Offset, &u.Plan, &u.Task, &u.Device, &u.Name, &u.Media, &u.Hash, &u.Path, &u.Size, &u.Completed)
	return
}
func artifactRef(u artifactUpload) protocol.ArtifactRef {
	return protocol.ArtifactRef{ArtifactID: u.ArtifactID, TaskID: u.Task, Name: u.Name, MediaType: u.Media, SizeBytes: u.Size, SHA256: u.Hash, DownloadURI: "/v1/artifacts/" + u.ArtifactID}
}
func (s *Server) assigned(device, plan, task string) bool {
	var raw string
	if s.db.QueryRow(`SELECT payload FROM tasks WHERE id=? AND plan_id=?`, task, plan).Scan(&raw) != nil {
		return false
	}
	var t protocol.TaskEnvelope
	return json.Unmarshal([]byte(raw), &t) == nil && t.TargetDeviceID == device
}
func (s *Server) required(device, id string) bool {
	rows, e := s.db.Query(`SELECT payload FROM tasks WHERE status IN ('dispatched','accepted','running','waiting_approval')`)
	if e != nil {
		return false
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if rows.Scan(&raw) != nil {
			return false
		}
		var t protocol.TaskEnvelope
		if json.Unmarshal([]byte(raw), &t) != nil || t.TargetDeviceID != device {
			continue
		}
		if t.WorkspaceContext != nil && t.WorkspaceContext.PatchArtifactID == id {
			return true
		}
		for _, a := range t.InputArtifacts {
			if a.ArtifactID == id {
				return true
			}
		}
	}
	return false
}
func (s *Server) completeArtifact(u artifactUpload) (protocol.ArtifactRef, error) {
	ref := artifactRef(u)
	if u.Completed {
		return ref, nil
	}
	if u.Offset != u.Size {
		return ref, protocol.Err("ARTIFACT_HASH_MISMATCH", "upload incomplete")
	}
	ready := strings.TrimSuffix(u.Path, ".part") + ".ready"
	f, e := os.Open(u.Path)
	if errors.Is(e, os.ErrNotExist) {
		f, e = os.Open(ready)
	}
	if e != nil {
		return ref, e
	}
	h := sha256.New()
	n, e := io.Copy(h, f)
	f.Close()
	if e != nil {
		return ref, e
	}
	if n != u.Size || hex.EncodeToString(h.Sum(nil)) != u.Hash {
		return ref, protocol.Err("ARTIFACT_HASH_MISMATCH", "artifact hash differs")
	}
	if _, e = os.Stat(u.Path); e == nil {
		if e = os.Rename(u.Path, ready); e != nil {
			return ref, e
		}
	}
	tx, e := s.db.Begin()
	if e != nil {
		return ref, e
	}
	defer tx.Rollback()
	_, e = tx.Exec(`INSERT INTO artifacts(id,plan_id,task_id,device_id,name,media_type,size_bytes,sha256,storage_path,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, u.ArtifactID, u.Plan, u.Task, u.Device, u.Name, u.Media, u.Size, u.Hash, ready, time.Now().UTC().Format(time.RFC3339Nano))
	if e != nil {
		return ref, e
	}
	if _, e = tx.Exec(`UPDATE artifact_uploads SET completed=1 WHERE upload_id=?`, u.UploadID); e != nil {
		return ref, e
	}
	_, e = emit(tx, protocol.TaskEvent{EventID: protocol.NewID("evt"), PlanID: u.Plan, TaskID: u.Task, Type: "artifact.ready", Timestamp: time.Now().UTC(), Payload: json.RawMessage(encode(ref))})
	if e != nil {
		return ref, e
	}
	return ref, tx.Commit()
}
