package coordinator

import (
	"database/sql"
	"encoding/hex"
	"errors"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func (s *Server) artifactsHTTP(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	controller := sameToken(token, s.cfg.ControllerToken)
	device := ""
	for id, t := range s.cfg.WorkerTokens {
		if sameToken(token, t) {
			device = id
		}
	}
	if !controller && device == "" {
		writeJSON(w, 401, protocol.Error{Code: "UNAUTHORIZED", Message: "artifact credential required"})
		return
	}
	var body []byte
	if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/uploads") {
		var err error
		body, err = io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
		if err != nil {
			writeError(w, protocol.Err("INVALID_ARTIFACT", "invalid metadata body"))
			return
		}
	}
	if r.Method == "PATCH" {
		var err error
		body, err = io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<20))
		if err != nil {
			writeError(w, protocol.Err("INVALID_ARTIFACT", "invalid chunk body"))
			return
		}
	}
	// File I/O is serialized only with writes to the same upload. The scheduler
	// and live cancellation never wait behind an artifact's hash or disk flush.
	path := strings.TrimPrefix(r.URL.Path, "/v1/artifacts/")
	key := ""
	if path == "uploads" {
		var in protocol.ArtifactUploadRequest
		if protocol.Decode(body, &in) == nil {
			key = "task:" + in.TaskID + "\x00" + in.Name
		}
	} else if strings.HasPrefix(path, "uploads/") {
		key = "upload:" + strings.Split(path, "/")[1]
	}
	if key != "" {
		defer s.lockArtifact(key)()
	}
	deny := func() {
		writeJSON(w, 403, protocol.Error{Code: "FORBIDDEN", Message: "artifact is not assigned or required"})
	}
	fail := func(e error) { writeError(w, e) }
	if path == "uploads" && r.Method == "POST" {
		var in protocol.ArtifactUploadRequest
		e := protocol.Decode(body, &in)
		hash, he := hex.DecodeString(in.SHA256)
		if e != nil || !protocol.ValidID(in.PlanID) || !protocol.ValidID(in.TaskID) || in.Name == "" || in.MediaType == "" || in.SizeBytes < 0 || he != nil || len(hash) != 32 || strings.ToLower(in.SHA256) != in.SHA256 {
			fail(protocol.Err("INVALID_ARTIFACT", "invalid upload metadata"))
			return
		}
		if !controller && !s.assigned(device, in.PlanID, in.TaskID) {
			deny()
			return
		}
		u, e := s.uploadBy("task_id=? AND name=?", in.TaskID, in.Name)
		if e == nil {
			if u.Hash != in.SHA256 || u.Size != in.SizeBytes || u.Plan != in.PlanID {
				fail(protocol.Err("IDEMPOTENCY_CONFLICT", "artifact content changed"))
				return
			}
			writeJSON(w, 200, protocol.ArtifactUploadSession{UploadID: u.UploadID, ArtifactID: u.ArtifactID, ChunkSize: u.ChunkSize, ConfirmedOffset: u.Offset})
			return
		}
		if !errors.Is(e, sql.ErrNoRows) {
			fail(e)
			return
		}
		u = artifactUpload{UploadID: protocol.NewID("up"), ArtifactID: protocol.NewID("art"), ChunkSize: 4 << 20, Plan: in.PlanID, Task: in.TaskID, Device: device, Name: in.Name, Media: in.MediaType, Hash: in.SHA256, Size: in.SizeBytes}
		dir := s.artifactDir
		if e = os.MkdirAll(dir, 0700); e != nil {
			fail(e)
			return
		}
		u.Path = filepath.Join(dir, u.ArtifactID+".part")
		f, e := os.OpenFile(u.Path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			fail(e)
			return
		}
		e = f.Close()
		if e == nil {
			_, e = s.db.Exec(`INSERT INTO artifact_uploads(upload_id,artifact_id,plan_id,task_id,device_id,name,media_type,total_size,expected_sha256,chunk_size,temp_path) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, u.UploadID, u.ArtifactID, u.Plan, u.Task, u.Device, u.Name, u.Media, u.Size, u.Hash, u.ChunkSize, u.Path)
		}
		if e != nil {
			os.Remove(u.Path)
			fail(e)
			return
		}
		writeJSON(w, 200, protocol.ArtifactUploadSession{UploadID: u.UploadID, ArtifactID: u.ArtifactID, ChunkSize: u.ChunkSize, ConfirmedOffset: u.Offset})
		return
	}
	if strings.HasPrefix(path, "uploads/") {
		parts := strings.Split(path, "/")
		if len(parts) < 2 || len(parts) > 3 {
			http.NotFound(w, r)
			return
		}
		u, e := s.uploadBy("upload_id=?", parts[1])
		if e != nil {
			fail(protocol.Err("ARTIFACT_NOT_FOUND", "upload not found"))
			return
		}
		if !controller && !s.assigned(device, u.Plan, u.Task) {
			deny()
			return
		}
		if len(parts) == 2 && r.Method == "PATCH" {
			offset, e := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
			if e != nil || offset != u.Offset {
				writeJSON(w, 409, map[string]any{"code": "OFFSET_MISMATCH", "message": "offset differs", "confirmed_offset": u.Offset})
				return
			}
			if u.Completed {
				fail(protocol.Err("IDEMPOTENCY_CONFLICT", "ready artifact is immutable"))
				return
			}
			b := body
			if int64(len(b))+u.Offset > u.Size {
				fail(protocol.Err("INVALID_ARTIFACT", "invalid chunk length"))
				return
			}
			f, e := os.OpenFile(u.Path, os.O_WRONLY, 0600)
			if e == nil {
				e = f.Truncate(u.Offset)
				if e == nil {
					_, e = f.WriteAt(b, u.Offset)
				}
				if e == nil {
					e = f.Sync()
				}
				ce := f.Close()
				if e == nil {
					e = ce
				}
			}
			if e != nil {
				fail(e)
				return
			}
			u.Offset += int64(len(b))
			if _, e = s.db.Exec(`UPDATE artifact_uploads SET confirmed_offset=? WHERE upload_id=?`, u.Offset, u.UploadID); e != nil {
				fail(e)
				return
			}
			writeJSON(w, 200, map[string]any{"upload_id": u.UploadID, "confirmed_offset": u.Offset})
			return
		}
		if len(parts) == 3 && parts[2] == "complete" && r.Method == "POST" {
			ref, e := s.completeArtifact(u)
			if e != nil {
				fail(e)
				return
			}
			writeJSON(w, 200, ref)
			return
		}
		http.NotFound(w, r)
		return
	}
	if r.Method == "GET" && !strings.Contains(path, "/") {
		if !controller && !s.required(device, path) {
			deny()
			return
		}
		var file, hash, media string
		e := s.db.QueryRow(`SELECT storage_path,sha256,media_type FROM artifacts WHERE id=?`, path).Scan(&file, &hash, &media)
		if e != nil {
			fail(protocol.Err("ARTIFACT_NOT_FOUND", path))
			return
		}
		f, e := os.Open(file)
		if e != nil {
			fail(e)
			return
		}
		defer f.Close()
		w.Header().Set("ETag", `"`+hash+`"`)
		w.Header().Set("Content-Type", media)
		http.ServeContent(w, r, path, time.Time{}, f)
		return
	}
	http.NotFound(w, r)
}
