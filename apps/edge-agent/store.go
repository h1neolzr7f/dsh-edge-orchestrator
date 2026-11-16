package agent

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	_ "modernc.org/sqlite"
	"os"
	"path/filepath"
	"time"
)

const schema = `PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;
CREATE TABLE IF NOT EXISTS identity(id TEXT PRIMARY KEY);
CREATE TABLE IF NOT EXISTS counters(id INTEGER PRIMARY KEY CHECK(id=1),worker_seq INTEGER NOT NULL DEFAULT 0,last_ack INTEGER NOT NULL DEFAULT 0);
INSERT OR IGNORE INTO counters(id) VALUES(1);
CREATE TABLE IF NOT EXISTS local_task_ledger(id TEXT PRIMARY KEY,ikey TEXT UNIQUE NOT NULL,hash TEXT NOT NULL,payload TEXT NOT NULL,receipt TEXT NOT NULL,status TEXT NOT NULL,task_seq INTEGER NOT NULL DEFAULT 0,execution_count INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS cancellations(task_id TEXT PRIMARY KEY);
CREATE TABLE IF NOT EXISTS event_outbox(worker_seq INTEGER PRIMARY KEY,payload TEXT NOT NULL);`

func encode(v any) string {
	b, e := json.Marshal(v)
	if e != nil {
		panic(e)
	}
	return string(b)
}
func (a *Agent) Accept(t protocol.TaskEnvelope) (protocol.TaskReceipt, bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var rec protocol.TaskReceipt
	if e := t.Validate(); e != nil {
		return rec, false, e
	}
	if t.TargetDeviceID != a.cfg.DeviceID || t.PlanID == "" || t.IdempotencyKey == "" {
		return rec, false, protocol.Err("INVALID_TASK", "wrong target or missing plan/key")
	}
	tx, e := a.db.Begin()
	if e != nil {
		return rec, false, e
	}
	defer tx.Rollback()
	var hash, raw string
	e = tx.QueryRow(`SELECT hash,receipt FROM local_task_ledger WHERE ikey=? OR id=?`, t.IdempotencyKey, t.TaskID).Scan(&hash, &raw)
	if e == nil {
		if hash != protocol.Fingerprint(t) {
			return rec, false, protocol.Err("IDEMPOTENCY_CONFLICT", "task content changed")
		}
		e = json.Unmarshal([]byte(raw), &rec)
		return rec, false, e
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return rec, false, e
	}
	rec = protocol.TaskReceipt{TaskID: t.TaskID, DeviceID: a.cfg.DeviceID, Status: "accepted", AcceptedAt: time.Now().UTC(), TaskSeq: 0}
	if _, e = tx.Exec(`INSERT INTO local_task_ledger(id,ikey,hash,payload,receipt,status) VALUES(?,?,?,?,?,'accepted')`, t.TaskID, t.IdempotencyKey, protocol.Fingerprint(t), encode(t), encode(rec)); e != nil {
		return rec, false, e
	}
	if e = tx.Commit(); e != nil {
		return rec, false, e
	}
	return rec, true, nil
}
func (a *Agent) ExecutionCount(id string) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var n int
	e := a.db.QueryRow(`SELECT execution_count FROM local_task_ledger WHERE id=?`, id).Scan(&n)
	if errors.Is(e, sql.ErrNoRows) {
		return 0, nil
	}
	return n, e
}
func appendEvent(tx *sql.Tx, device string, t protocol.TaskEnvelope, kind string, p protocol.EventPayload, status string) error {
	var seq, taskseq int64
	if e := tx.QueryRow(`SELECT worker_seq FROM counters WHERE id=1`).Scan(&seq); e != nil {
		return e
	}
	if e := tx.QueryRow(`SELECT task_seq FROM local_task_ledger WHERE id=?`, t.TaskID).Scan(&taskseq); e != nil {
		return e
	}
	seq++
	taskseq++
	if seq > protocol.MaxSequence || taskseq > protocol.MaxSequence {
		return fmt.Errorf("event sequence exhausted")
	}
	ev := protocol.TaskEvent{EventID: protocol.NewID("evt"), PlanID: t.PlanID, TaskID: t.TaskID, DeviceID: device, WorkerSeq: seq, TaskSeq: taskseq, Type: kind, Timestamp: time.Now().UTC(), Payload: json.RawMessage(encode(p))}
	if _, e := tx.Exec(`INSERT INTO event_outbox(worker_seq,payload) VALUES(?,?)`, seq, encode(ev)); e != nil {
		return e
	}
	if _, e := tx.Exec(`UPDATE counters SET worker_seq=? WHERE id=1`, seq); e != nil {
		return e
	}
	_, e := tx.Exec(`UPDATE local_task_ledger SET task_seq=?,status=? WHERE id=?`, taskseq, status, t.TaskID)
	return e
}
func (a *Agent) claim(t protocol.TaskEnvelope) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	// Shutdown leaves accepted preflight recoverable; no started event is emitted.
	if a.closed || a.ctx.Err() != nil {
		return false, nil
	}
	tx, e := a.db.Begin()
	if e != nil {
		return false, e
	}
	defer tx.Rollback()
	var status string
	if e = tx.QueryRow(`SELECT status FROM local_task_ledger WHERE id=?`, t.TaskID).Scan(&status); e != nil {
		return false, e
	}
	if status != "accepted" {
		return false, nil
	}
	var cancelled int
	if e = tx.QueryRow(`SELECT COUNT(*) FROM cancellations WHERE task_id=?`, t.TaskID).Scan(&cancelled); e != nil {
		return false, e
	}
	if cancelled > 0 {
		if e = appendEvent(tx, a.cfg.DeviceID, t, "task.cancelled", protocol.EventPayload{}, "cancelled"); e != nil {
			return false, e
		}
		return false, tx.Commit()
	}
	if t.Deadline != nil && !time.Now().Before(*t.Deadline) {
		if e = appendEvent(tx, a.cfg.DeviceID, t, "task.failed", protocol.EventPayload{Error: &protocol.Error{Code: "DEADLINE_EXCEEDED", Message: "task deadline exceeded during preflight"}}, "failed"); e != nil {
			return false, e
		}
		return false, tx.Commit()
	}
	if _, e = tx.Exec(`UPDATE local_task_ledger SET execution_count=execution_count+1 WHERE id=?`, t.TaskID); e != nil {
		return false, e
	}
	if e = appendEvent(tx, a.cfg.DeviceID, t, "task.started", protocol.EventPayload{}, "running"); e != nil {
		return false, e
	}
	return true, tx.Commit()
}
func (a *Agent) finish(t protocol.TaskEnvelope, result json.RawMessage, runErr error, cancelled bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	cancelled = cancelled || a.cancelled[t.TaskID]
	// A verified persistent runner or completed exit record survives a parent
	// stop even if shutdown won after execution returned but before this commit.
	if t.Capability.ID == "process.exec" && a.processDetachedLocked(t) && a.canResumeProcess(t) {
		return errAgentDetached
	}
	tx, e := a.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var status string
	if e = tx.QueryRow(`SELECT status FROM local_task_ledger WHERE id=?`, t.TaskID).Scan(&status); e != nil {
		return e
	}
	if protocol.Terminal(status) {
		return nil
	}
	kind, next := "task.succeeded", "succeeded"
	payload := protocol.EventPayload{Result: result}
	if runErr != nil {
		kind, next = "task.failed", "failed"
		var pe *protocol.Error
		if !errors.As(runErr, &pe) {
			pe = &protocol.Error{Code: "EXECUTION_FAILED", Message: runErr.Error()}
		}
		payload = protocol.EventPayload{Error: pe, Result: result}
	}
	if cancelled {
		kind, next = "task.cancelled", "cancelled"
		payload = protocol.EventPayload{}
	}
	if e = appendEvent(tx, a.cfg.DeviceID, t, kind, payload, next); e != nil {
		return e
	}
	return tx.Commit()
}
func (a *Agent) outbox(afterSeq int64) ([]protocol.TaskEvent, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	rows, e := a.db.Query(`SELECT payload FROM event_outbox WHERE worker_seq > ? ORDER BY worker_seq LIMIT 100`, afterSeq)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []protocol.TaskEvent{}
	for rows.Next() {
		var b string
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		var ev protocol.TaskEvent
		if e = json.Unmarshal([]byte(b), &ev); e != nil {
			return nil, e
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}
func (a *Agent) ack(n int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	tx, e := a.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var generated, last int64
	if e = tx.QueryRow(`SELECT worker_seq,last_ack FROM counters WHERE id=1`).Scan(&generated, &last); e != nil {
		return e
	}
	if n < last {
		return protocol.Err("EVENT_HISTORY_LOST", "coordinator acknowledgment went backwards")
	}
	if n > generated {
		return protocol.Err("INVALID_EVENT_ACK", "ack exceeds worker history")
	}
	if _, e = tx.Exec(`UPDATE counters SET last_ack=? WHERE id=1`, n); e != nil {
		return e
	}
	if _, e = tx.Exec(`DELETE FROM event_outbox WHERE worker_seq<=?`, n); e != nil {
		return e
	}
	return tx.Commit()
}
func (a *Agent) lastAck() (int64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var n int64
	e := a.db.QueryRow(`SELECT last_ack FROM counters WHERE id=1`).Scan(&n)
	return n, e
}
func (a *Agent) recoverTasks() ([]protocol.TaskEnvelope, error) {
	a.mu.Lock()
	rows, e := a.db.Query(`SELECT payload,status FROM local_task_ledger WHERE status IN ('accepted','running') ORDER BY id`)
	if e != nil {
		a.mu.Unlock()
		return nil, e
	}
	var accepted, running []protocol.TaskEnvelope
	for rows.Next() {
		var b, status string
		if e = rows.Scan(&b, &status); e != nil {
			rows.Close()
			a.mu.Unlock()
			return nil, e
		}
		var t protocol.TaskEnvelope
		if e = json.Unmarshal([]byte(b), &t); e != nil {
			rows.Close()
			a.mu.Unlock()
			return nil, e
		}
		if status == "accepted" {
			accepted = append(accepted, t)
		} else {
			running = append(running, t)
		}
	}
	e = rows.Err()
	rows.Close()
	a.mu.Unlock()
	if e != nil {
		return nil, e
	}
	for _, t := range running {
		if t.Capability.ID == "process.exec" && a.canResumeProcess(t) {
			a.mu.Lock()
			a.resuming[t.TaskID] = true
			a.mu.Unlock()
			accepted = append(accepted, t)
			continue
		}
		if e = a.finish(t, nil, protocol.Err("interrupted_by_restart", "accepted task is never automatically re-executed"), false); e != nil {
			return nil, e
		}
	}
	return accepted, nil
}

func (a *Agent) cancelTask(id string) error {
	if !protocol.ValidID(id) {
		return protocol.Err("INVALID_TASK", "invalid cancellation ID")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	tx, e := a.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if _, e = tx.Exec(`INSERT OR IGNORE INTO cancellations(task_id) VALUES(?)`, id); e != nil {
		return e
	}
	var raw, status string
	e = tx.QueryRow(`SELECT payload,status FROM local_task_ledger WHERE id=?`, id).Scan(&raw, &status)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return e
	}
	if e == nil && status == "accepted" {
		var t protocol.TaskEnvelope
		if e = json.Unmarshal([]byte(raw), &t); e != nil {
			return e
		}
		if e = appendEvent(tx, a.cfg.DeviceID, t, "task.cancelled", protocol.EventPayload{}, "cancelled"); e != nil {
			return e
		}
	}
	if e = tx.Commit(); e != nil {
		return e
	}
	a.cancelled[id] = true
	if raw != "" && !protocol.Terminal(status) {
		var task protocol.TaskEnvelope
		if json.Unmarshal([]byte(raw), &task) == nil && task.Capability.ID == "process.exec" {
			dir := a.taskDir(id)
			if e = os.MkdirAll(dir, 0700); e != nil {
				return e
			}
			if e = os.WriteFile(filepath.Join(dir, "cancel"), []byte("cancel"), 0600); e != nil {
				return e
			}
		}
	}
	if cancel := a.active[id]; cancel != nil {
		cancel()
	}
	return nil
}
func (a *Agent) taskSnapshot(id string) (protocol.TaskReconcileSnapshot, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := protocol.TaskReconcileSnapshot{TaskID: id, DeviceID: a.cfg.DeviceID}
	e := a.db.QueryRow(`SELECT status,task_seq FROM local_task_ledger WHERE id=?`, id).Scan(&out.Status, &out.TaskSeq)
	if errors.Is(e, sql.ErrNoRows) {
		out.Error = &protocol.Error{Code: "TASK_NOT_FOUND", Message: "no task in local ledger"}
		return out, nil
	}
	return out, e
}
