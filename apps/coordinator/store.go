package coordinator

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/orchestrator"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	_ "modernc.org/sqlite"
	"time"
	"unicode/utf8"
)

const schema = `PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000; PRAGMA foreign_keys=ON;
CREATE TABLE IF NOT EXISTS devices(id TEXT PRIMARY KEY,registration TEXT NOT NULL,last_worker_seq INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS plans(id TEXT PRIMARY KEY,ikey TEXT UNIQUE NOT NULL,hash TEXT NOT NULL,payload TEXT NOT NULL,status TEXT NOT NULL,paused INTEGER NOT NULL DEFAULT 0,cancelled INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS tasks(id TEXT PRIMARY KEY,plan_id TEXT NOT NULL REFERENCES plans(id),ikey TEXT UNIQUE NOT NULL,payload TEXT NOT NULL,status TEXT NOT NULL,task_seq INTEGER NOT NULL DEFAULT 0,result TEXT,error TEXT,last_dispatch INTEGER NOT NULL DEFAULT 0,pending_reason TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS worker_event_inbox(device_id TEXT NOT NULL,worker_seq INTEGER NOT NULL,payload TEXT NOT NULL,hash TEXT NOT NULL,PRIMARY KEY(device_id,worker_seq));
CREATE TABLE IF NOT EXISTS task_events(coord_seq INTEGER PRIMARY KEY AUTOINCREMENT,device_id TEXT,worker_seq INTEGER,payload TEXT NOT NULL,UNIQUE(device_id,worker_seq));
CREATE INDEX IF NOT EXISTS tasks_by_plan ON tasks(plan_id);
CREATE INDEX IF NOT EXISTS task_log_tail ON task_events(json_extract(payload,'$.plan_id'),json_extract(payload,'$.task_id'),json_extract(payload,'$.type'),coord_seq);`

func encode(v any) string {
	b, e := json.Marshal(v)
	if e != nil {
		panic(e)
	}
	return string(b)
}
func dbError(e error) error {
	if e == nil {
		return nil
	}
	return protocol.Err("STORE_ERROR", e.Error())
}
func emit(tx *sql.Tx, ev protocol.TaskEvent) (int64, error) {
	r, e := tx.Exec(`INSERT INTO task_events(device_id,worker_seq,payload) VALUES(?,?,?)`, nullable(ev.DeviceID), nullableSeq(ev.WorkerSeq), encode(ev))
	if e != nil {
		return 0, e
	}
	return r.LastInsertId()
}
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
func nullableSeq(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}
func planEvent(tx *sql.Tx, id, kind, status string) (int64, error) {
	return emit(tx, protocol.TaskEvent{EventID: protocol.NewID("evt"), PlanID: id, Type: kind, Timestamp: time.Now().UTC(), Payload: json.RawMessage(encode(protocol.EventPayload{Status: status}))})
}

type dbQueryer interface {
	Query(string, ...any) (*sql.Rows, error)
	QueryRow(string, ...any) *sql.Row
}

func highWater(q dbQueryer) (int64, error) {
	var n int64
	e := q.QueryRow(`SELECT COALESCE(MAX(coord_seq),0) FROM task_events`).Scan(&n)
	return n, e
}
func loadPlan(tx *sql.Tx, id string, includeLogs ...bool) (protocol.PlanSnapshot, error) {
	var p protocol.PlanSnapshot
	var b string
	e := tx.QueryRow(`SELECT payload,status,paused,cancelled FROM plans WHERE id=?`, id).Scan(&b, &p.Status, &p.SchedulingPaused, &p.CancelRequested)
	if errors.Is(e, sql.ErrNoRows) {
		return p, protocol.Err("PLAN_NOT_FOUND", id)
	}
	if e != nil {
		return p, e
	}
	if e = json.Unmarshal([]byte(b), &p.Plan); e != nil {
		return p, e
	}
	p.Tasks = []protocol.TaskSnapshot{}
	rows, e := tx.Query(`SELECT payload,status,task_seq,result,error,pending_reason FROM tasks WHERE plan_id=? ORDER BY id`, id)
	if e != nil {
		return p, e
	}
	for rows.Next() {
		var v protocol.TaskSnapshot
		var raw string
		var result, er sql.NullString
		if e = rows.Scan(&raw, &v.Status, &v.TaskSeq, &result, &er, &v.PendingReason); e != nil {
			rows.Close()
			return p, e
		}
		if e = json.Unmarshal([]byte(raw), &v.Task); e != nil {
			rows.Close()
			return p, e
		}
		if v.Status != "queued" {
			v.PendingReason = ""
		}
		v.Artifacts = []protocol.ArtifactRef{}
		if result.Valid {
			v.Result = json.RawMessage(result.String)
		}
		if er.Valid {
			if e = json.Unmarshal([]byte(er.String), &v.Error); e != nil {
				rows.Close()
				return p, e
			}
		}
		p.Tasks = append(p.Tasks, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return p, e
	}
	for i := range p.Tasks {
		if len(includeLogs) > 0 && includeLogs[0] {
			logs, err := tx.Query(`SELECT payload FROM task_events WHERE json_extract(payload,'$.plan_id')=? AND json_extract(payload,'$.task_id')=? AND json_extract(payload,'$.type')='task.log' ORDER BY coord_seq DESC LIMIT 8`, id, p.Tasks[i].Task.TaskID)
			if err != nil {
				return p, err
			}
			var chunks []string
			for logs.Next() {
				var raw string
				if err = logs.Scan(&raw); err != nil {
					break
				}
				var ev protocol.TaskEvent
				if err = json.Unmarshal([]byte(raw), &ev); err != nil {
					break
				}
				var payload protocol.EventPayload
				if err = json.Unmarshal(ev.Payload, &payload); err != nil {
					break
				}
				chunks = append(chunks, payload.Text)
			}
			if err == nil {
				err = logs.Err()
			}
			logs.Close()
			if err != nil {
				return p, err
			}
			for j := len(chunks) - 1; j >= 0; j-- {
				p.Tasks[i].LogTail += chunks[j]
			}
			if len(p.Tasks[i].LogTail) > 16384 {
				tail := p.Tasks[i].LogTail[len(p.Tasks[i].LogTail)-16384:]
				for !utf8.ValidString(tail) {
					tail = tail[1:]
				}
				p.Tasks[i].LogTail = tail
			}
		}
		ar, err := tx.Query(`SELECT id,name,media_type,size_bytes,sha256 FROM artifacts WHERE plan_id=? AND task_id=? ORDER BY name`, id, p.Tasks[i].Task.TaskID)
		if err != nil {
			return p, err
		}
		for ar.Next() {
			v := protocol.ArtifactRef{TaskID: p.Tasks[i].Task.TaskID}
			if err = ar.Scan(&v.ArtifactID, &v.Name, &v.MediaType, &v.SizeBytes, &v.SHA256); err != nil {
				ar.Close()
				return p, err
			}
			v.DownloadURI = "/v1/artifacts/" + v.ArtifactID
			p.Tasks[i].Artifacts = append(p.Tasks[i].Artifacts, v)
		}
		err = ar.Err()
		ar.Close()
		if err != nil {
			return p, err
		}
	}
	p.SnapshotCoordSeq, e = highWater(tx)
	return p, e
}
func (s *Server) Submit(p protocol.PlanEnvelope) (protocol.PlanAccepted, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out protocol.PlanAccepted
	if e := p.Validate(); e != nil {
		return out, e
	}
	for _, t := range p.Tasks {
		if !supportedCapability(t.Capability.ID) {
			return out, protocol.Err("UNSUPPORTED_CAPABILITY", t.Capability.ID)
		}

	}
	tx, e := s.db.Begin()
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	hash := protocol.Fingerprint(p)
	var oldHash string
	e = tx.QueryRow(`SELECT id,hash,status FROM plans WHERE ikey=?`, p.IdempotencyKey).Scan(&out.PlanID, &oldHash, &out.Status)
	if e == nil {
		if hash != oldHash {
			return out, protocol.Err("IDEMPOTENCY_CONFLICT", "plan content changed")
		}
		out.CoordSeq, e = highWater(tx)
		return out, e
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return out, e
	}
	for _, t := range p.Tasks {
		if t.TargetDeviceID != "" {
			var raw string
			err := tx.QueryRow(`SELECT registration FROM devices WHERE id=?`, t.TargetDeviceID).Scan(&raw)
			if err == nil {
				var reg protocol.DeviceRegistration
				if err = json.Unmarshal([]byte(raw), &reg); err != nil {
					return out, err
				}
				if !orchestrator.Supports(reg.Capabilities, t.Capability) {
					return out, protocol.Err("CAPABILITY_UNAVAILABLE", "target does not authorize the requested capability/version")
				}
			} else if !errors.Is(err, sql.ErrNoRows) {
				return out, err
			}
		}
	}
	var exists int
	e = tx.QueryRow(`SELECT COUNT(*) FROM plans WHERE id=?`, p.PlanID).Scan(&exists)
	if e != nil {
		return out, e
	}
	if exists > 0 {
		return out, protocol.Err("IDEMPOTENCY_CONFLICT", "plan_id reused")
	}
	if _, e = tx.Exec(`INSERT INTO plans(id,ikey,hash,payload,status) VALUES(?,?,?,?,'queued')`, p.PlanID, p.IdempotencyKey, hash, encode(p)); e != nil {
		return out, e
	}
	for _, t := range p.Tasks {
		t.PlanID = p.PlanID
		if t.IdempotencyKey == "" {
			t.IdempotencyKey = p.PlanID + ":" + t.TaskID
		}
		if t.ReplayPolicy == "" {
			t.ReplayPolicy = "never"
		}
		if _, e = tx.Exec(`INSERT INTO tasks(id,plan_id,ikey,payload,status) VALUES(?,?,?,?,'queued')`, t.TaskID, p.PlanID, t.IdempotencyKey, encode(t)); e != nil {
			return out, protocol.Err("IDEMPOTENCY_CONFLICT", "task ID/key reused")
		}
	}
	n, e := planEvent(tx, p.PlanID, "plan.created", "queued")
	if e != nil {
		return out, e
	}
	if e = tx.Commit(); e != nil {
		return out, e
	}
	return protocol.PlanAccepted{PlanID: p.PlanID, Status: "queued", CoordSeq: n}, nil
}
func (s *Server) Plan(id string) (protocol.PlanSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, e := s.db.Begin()
	if e != nil {
		return protocol.PlanSnapshot{}, e
	}
	defer tx.Rollback()
	return loadPlan(tx, id, true)
}
func (s *Server) devicesLocked(q dbQueryer) ([]protocol.DeviceSnapshot, error) {
	out := []protocol.DeviceSnapshot{}
	rows, e := q.Query(`SELECT registration FROM devices ORDER BY id`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var b string
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		var r protocol.DeviceRegistration
		if e = json.Unmarshal([]byte(b), &r); e != nil {
			return nil, e
		}
		out = append(out, protocol.DeviceSnapshot{DeviceManifest: r.Manifest, Capabilities: r.Capabilities, Online: s.sessions[r.Manifest.DeviceID] != nil})
	}
	return out, rows.Err()
}
func (s *Server) Devices() ([]protocol.DeviceSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.devicesLocked(s.db)
}
func (s *Server) Snapshot() (protocol.FleetSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out protocol.FleetSnapshot
	var e error
	tx, e := s.db.Begin()
	if e != nil {
		return out, e
	}
	defer tx.Rollback()
	out.Devices, e = s.devicesLocked(tx)
	if e != nil {
		return out, e
	}
	rows, e := tx.Query(`SELECT id FROM plans ORDER BY id`)
	if e != nil {
		return out, e
	}
	var ids []string
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return out, e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return out, e
	}
	out.Plans = []protocol.PlanSnapshot{}
	for _, id := range ids {
		p, e := loadPlan(tx, id, true)
		if e != nil {
			return out, e
		}
		out.Plans = append(out.Plans, p)
	}
	out.SnapshotCoordSeq, e = highWater(tx)
	return out, e
}
func (s *Server) Events(after, through int64, limit int) (protocol.EventPage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := protocol.EventPage{Events: []protocol.TaskEvent{}, NextCoordSeq: after}
	high, e := highWater(s.db)
	if e != nil {
		return out, e
	}
	if after < 0 || after > high || through < 0 || through > high || (through > 0 && after > through) {
		return out, protocol.Err("CURSOR_INVALID", "cursor outside event history")
	}
	if through == 0 {
		through = high
	}
	if limit < 1 || limit > 500 {
		return out, protocol.Err("INVALID_PAYLOAD", "limit must be 1..500")
	}
	out.HighWatermark = through
	rows, e := s.db.Query(`SELECT coord_seq,payload FROM task_events WHERE coord_seq>? AND coord_seq<=? ORDER BY coord_seq LIMIT ?`, after, through, limit+1)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var ev protocol.TaskEvent
		var b string
		var n int64
		if e = rows.Scan(&n, &b); e != nil {
			return out, e
		}
		if len(out.Events) == limit {
			out.HasMore = true
			break
		}
		if e = json.Unmarshal([]byte(b), &ev); e != nil {
			return out, e
		}
		ev.CoordSeq = n
		out.Events = append(out.Events, ev)
		out.NextCoordSeq = n
	}
	if !out.HasMore {
		out.NextCoordSeq = through
	}
	return out, rows.Err()
}
func refreshPlan(tx *sql.Tx, id string) error {
	p, e := loadPlan(tx, id)
	if e != nil {
		return e
	}
	if protocol.Terminal(p.Status) {
		return nil
	}
	for changed := true; changed; {
		changed = false
		for i := range p.Tasks {
			t := &p.Tasks[i]
			if t.Status == "queued" && orchestrator.DependencyFailed(t.Task, p.Tasks) {
				t.Status = "rejected"
				t.Error = &protocol.Error{Code: "DEPENDENCY_FAILED", Message: "prerequisite did not succeed"}
				if _, e = tx.Exec(`UPDATE tasks SET pending_reason='',status='rejected',error=? WHERE id=?`, encode(t.Error), t.Task.TaskID); e != nil {
					return e
				}
				changed = true
			}
		}
	}
	status := orchestrator.Aggregate(p.Tasks, p.CancelRequested)
	if _, e = tx.Exec(`UPDATE plans SET status=? WHERE id=?`, status, id); e != nil {
		return e
	}
	if _, e = planEvent(tx, id, "plan.updated", status); e != nil {
		return e
	}
	if protocol.Terminal(status) {
		_, e = planEvent(tx, id, "plan.completed", status)
	}
	return e
}
func (s *Server) AppendEvent(device string, ev protocol.TaskEvent) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ev.DeviceID != device || ev.WorkerSeq < 1 || ev.WorkerSeq > protocol.MaxSequence || ev.CoordSeq != 0 || ev.TaskSeq < 1 || ev.TaskSeq > protocol.MaxSequence || !protocol.ValidID(ev.EventID) || ev.Timestamp.IsZero() || !json.Valid(ev.Payload) {
		return 0, protocol.Err("INVALID_EVENT", "invalid worker event identity")
	}
	tx, e := s.db.Begin()
	if e != nil {
		return 0, e
	}
	defer tx.Rollback()
	var watermark int64
	e = tx.QueryRow(`SELECT last_worker_seq FROM devices WHERE id=?`, device).Scan(&watermark)
	if errors.Is(e, sql.ErrNoRows) {
		if _, e = tx.Exec(`INSERT INTO devices(id,registration) VALUES(?,?)`, device, encode(protocol.DeviceRegistration{Manifest: protocol.DeviceManifest{DeviceID: device}})); e != nil {
			return 0, e
		}
	} else if e != nil {
		return 0, e
	}
	// Validate the task binding even for a gap; a worker cannot write another worker's task.
	var taskRaw string
	e = tx.QueryRow(`SELECT payload FROM tasks WHERE id=? AND plan_id=?`, ev.TaskID, ev.PlanID).Scan(&taskRaw)
	if e != nil {
		return watermark, protocol.Err("INVALID_EVENT", "unknown task")
	}
	var binding protocol.TaskEnvelope
	if e = json.Unmarshal([]byte(taskRaw), &binding); e != nil {
		return watermark, e
	}
	if binding.TargetDeviceID != device {
		return watermark, protocol.Err("INVALID_EVENT", "wrong assigned worker")
	}
	hash := protocol.Fingerprint(ev)
	var old string
	e = tx.QueryRow(`SELECT hash FROM worker_event_inbox WHERE device_id=? AND worker_seq=?`, device, ev.WorkerSeq).Scan(&old)
	if e == nil {
		if old != hash {
			return watermark, protocol.Err("EVENT_CONFLICT", "worker_seq reused")
		}
		// A pending duplicate must retry draining: a prior call may have
		// committed a quarantined predecessor before disconnecting the link.
		var pending string
		if e = tx.QueryRow(`SELECT payload FROM worker_event_inbox WHERE device_id=? AND worker_seq=?`, device, ev.WorkerSeq).Scan(&pending); e != nil {
			return watermark, e
		}
		if pending == "" {
			return watermark, nil
		}
	}
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return watermark, e
	}
	if errors.Is(e, sql.ErrNoRows) {
		if _, e = tx.Exec(`INSERT INTO worker_event_inbox(device_id,worker_seq,payload,hash) VALUES(?,?,?,?)`, device, ev.WorkerSeq, encode(ev), hash); e != nil {
			return watermark, e
		}
	}
	var next protocol.TaskEvent
	commitPrefix := func(cause error) (int64, error) {
		if _, err := tx.Exec(`ROLLBACK TO advance_event`); err != nil {
			return watermark, err
		}
		if _, err := tx.Exec(`RELEASE advance_event`); err != nil {
			return watermark, err
		}
		// Only business violations are poison. Storage failures retain the
		// pending event so they can be retried without losing valid history.
		var violation *protocol.Error
		if errors.As(cause, &violation) && violation.Code == "INVALID_EVENT" {
			if _, err := tx.Exec(`UPDATE worker_event_inbox SET payload='' WHERE device_id=? AND worker_seq=?`, device, watermark+1); err != nil {
				return watermark, err
			}
			watermark++
			if _, err := tx.Exec(`UPDATE tasks SET status='failed',error=? WHERE id=? AND status IN ('dispatched','accepted','running')`, encode(&protocol.Error{Code: "PROTOCOL_VIOLATION", Message: violation.Message}), next.TaskID); err != nil {
				return watermark, err
			}
			if err := refreshPlan(tx, next.PlanID); err != nil {
				return watermark, err
			}
		}
		if _, err := tx.Exec(`UPDATE devices SET last_worker_seq=? WHERE id=?`, watermark, device); err != nil {
			return watermark, err
		}
		if err := tx.Commit(); err != nil {
			return watermark, err
		}
		return watermark, cause
	}
	for {
		var b string
		e = tx.QueryRow(`SELECT payload FROM worker_event_inbox WHERE device_id=? AND worker_seq=?`, device, watermark+1).Scan(&b)
		if errors.Is(e, sql.ErrNoRows) {
			break
		}
		if e != nil {
			return watermark, e
		}
		if _, e = tx.Exec(`SAVEPOINT advance_event`); e != nil {
			return watermark, e
		}
		if e = json.Unmarshal([]byte(b), &next); e != nil {
			return commitPrefix(e)
		}
		var status string
		var seq int64
		if e = tx.QueryRow(`SELECT status,task_seq FROM tasks WHERE id=?`, next.TaskID).Scan(&status, &seq); e != nil {
			return commitPrefix(e)
		}
		if next.TaskSeq != seq+1 {
			return commitPrefix(protocol.Err("INVALID_EVENT", "non-contiguous task_seq"))
		}
		var payload protocol.EventPayload
		if e = protocol.Decode(next.Payload, &payload); e != nil {
			return commitPrefix(protocol.Err("INVALID_EVENT", e.Error()))
		}
		target := status
		switch next.Type {
		case "task.started":
			if status != "accepted" && status != "dispatched" {
				return commitPrefix(protocol.Err("INVALID_EVENT", "invalid start transition"))
			}
			target = "running"
		case "task.succeeded":
			if status != "running" {
				return commitPrefix(protocol.Err("INVALID_EVENT", "invalid success transition"))
			}
			target = "succeeded"
		case "task.failed":
			if status != "dispatched" && status != "accepted" && status != "running" {
				return commitPrefix(protocol.Err("INVALID_EVENT", "task already terminal"))
			}
			target = "failed"
		case "task.cancelled":
			if status != "dispatched" && status != "accepted" && status != "running" {
				return commitPrefix(protocol.Err("INVALID_EVENT", "task already terminal"))
			}
			target = "cancelled"
		case "task.progress", "task.log":
			if status != "running" {
				return commitPrefix(protocol.Err("INVALID_EVENT", "task not running"))
			}
		default:
			return commitPrefix(protocol.Err("INVALID_EVENT", "unsupported event type"))
		}
		if _, e = emit(tx, next); e != nil {
			return commitPrefix(e)
		}
		var result, er any
		if payload.Result != nil {
			result = string(payload.Result)
		}
		if payload.Error != nil {
			er = encode(payload.Error)
		}
		if _, e = tx.Exec(`UPDATE tasks SET pending_reason='',status=?,task_seq=?,result=COALESCE(?,result),error=COALESCE(?,error) WHERE id=?`, target, next.TaskSeq, result, er, next.TaskID); e != nil {
			return commitPrefix(e)
		}
		if e = refreshPlan(tx, next.PlanID); e != nil {
			return commitPrefix(e)
		}
		if _, e = tx.Exec(`UPDATE worker_event_inbox SET payload='' WHERE device_id=? AND worker_seq=?`, device, next.WorkerSeq); e != nil {
			return commitPrefix(e)
		}
		if _, e = tx.Exec(`RELEASE advance_event`); e != nil {
			return watermark, e
		}
		watermark++
	}
	if _, e = tx.Exec(`UPDATE devices SET last_worker_seq=? WHERE id=?`, watermark, device); e != nil {
		return watermark, e
	}
	e = tx.Commit()
	return watermark, e
}
func (s *Server) Control(id, action string) (protocol.PlanSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, e := s.db.Begin()
	if e != nil {
		return protocol.PlanSnapshot{}, e
	}
	defer tx.Rollback()
	p, e := loadPlan(tx, id)
	if e != nil {
		return p, e
	}
	if protocol.Terminal(p.Status) {
		if action == "cancel" {
			return p, nil
		}
		return p, protocol.Err("PLAN_TERMINAL", "cannot resume or pause terminal plan")
	}
	switch action {
	case "cancel":
		if !p.CancelRequested {
			if _, e = tx.Exec(`UPDATE plans SET cancelled=1 WHERE id=?`, id); e != nil {
				return p, e
			}
			if _, e = tx.Exec(`UPDATE tasks SET pending_reason='',status='cancelled' WHERE plan_id=? AND status IN ('queued','waiting_approval')`, id); e != nil {
				return p, e
			}
			for _, t := range p.Tasks {
				if e = invalidateApproval(tx, t.Task.TaskID, "cancelled"); e != nil {
					return p, e
				}
			}
			e = refreshPlan(tx, id)
		}
	case "pause", "resume":
		paused := action == "pause"
		if p.CancelRequested {
			return p, protocol.Err("PLAN_CANCELLING", "cancellation takes precedence")
		}
		if p.SchedulingPaused != paused {
			_, e = tx.Exec(`UPDATE plans SET paused=? WHERE id=?`, paused, id)
			if e == nil {
				_, e = planEvent(tx, id, "plan.updated", p.Status)
			}
		}
	default:
		return p, fmt.Errorf("invalid action")
	}
	if e != nil {
		return p, e
	}
	p, e = loadPlan(tx, id, true)
	if e != nil {
		return p, e
	}
	if e = tx.Commit(); e != nil {
		return p, e
	}
	return p, nil
}

// A missing-task reply after a durable Worker cancellation tombstone closes the
// ambiguous dispatched-without-receipt window without executing cancelled work.
func (s *Server) resolveAbsentCancellation(device, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var raw, planID, status string
	var cancelled bool
	e = tx.QueryRow(`SELECT t.payload,t.plan_id,t.status,p.cancelled FROM tasks t JOIN plans p ON p.id=t.plan_id WHERE t.id=?`, id).Scan(&raw, &planID, &status, &cancelled)
	if e != nil {
		return e
	}
	var task protocol.TaskEnvelope
	if e = json.Unmarshal([]byte(raw), &task); e != nil {
		return e
	}
	if task.TargetDeviceID != device {
		return protocol.Err("INVALID_EVENT", "snapshot from wrong device")
	}
	if !cancelled || protocol.Terminal(status) {
		return nil
	}
	if _, e = tx.Exec(`UPDATE tasks SET pending_reason='',status='cancelled' WHERE id=?`, id); e != nil {
		return e
	}
	if e = refreshPlan(tx, planID); e != nil {
		return e
	}
	return tx.Commit()
}

func supportedCapability(id string) bool {
	switch id {
	case "device.inspect", "report.aggregate", "process.exec", "process.cancel", "filesystem.read", "filesystem.write", "git.inspect", "artifact.upload":
		return true
	}
	return false
}
