package coordinator

import (
	"database/sql"
	"encoding/json"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
)

func ensurePendingReason(db *sql.DB) error {
	rows, e := db.Query(`PRAGMA table_info(tasks)`)
	if e != nil {
		return e
	}
	found := false
	for rows.Next() {
		var cid, notnull, pk int
		var name, kind string
		var value any
		if e = rows.Scan(&cid, &name, &kind, &notnull, &value, &pk); e != nil {
			rows.Close()
			return e
		}
		found = found || name == "pending_reason"
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	if !found {
		_, e = db.Exec(`ALTER TABLE tasks ADD COLUMN pending_reason TEXT NOT NULL DEFAULT ''`)
	}
	return e
}
func (s *Server) RecordTaskAccepted(device string, rec protocol.TaskReceipt) error {
	if e := rec.Validate(); e != nil {
		return e
	}
	if rec.DeviceID != device {
		return protocol.Err("INVALID_RECEIPT", "wrong worker")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var raw, status string
	if e = tx.QueryRow(`SELECT payload,status FROM tasks WHERE id=?`, rec.TaskID).Scan(&raw, &status); e != nil {
		return e
	}
	var task protocol.TaskEnvelope
	if e = json.Unmarshal([]byte(raw), &task); e != nil {
		return e
	}
	if task.TargetDeviceID != device {
		return protocol.Err("INVALID_RECEIPT", "unassigned worker")
	}
	if status != "dispatched" {
		return nil
	}
	if _, e = tx.Exec(`UPDATE tasks SET status='accepted',pending_reason='' WHERE id=?`, task.TaskID); e != nil {
		return e
	}
	var planStatus string
	if e = tx.QueryRow(`SELECT status FROM plans WHERE id=?`, task.PlanID).Scan(&planStatus); e != nil {
		return e
	}
	if _, e = planEvent(tx, task.PlanID, "plan.updated", planStatus); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *Server) queueReconcileLocked(device string) error {
	rows, e := s.db.Query(`SELECT id,payload FROM tasks WHERE status IN ('accepted','running')`)
	if e != nil {
		return e
	}
	var ids []string
	for rows.Next() {
		var id, raw string
		if e = rows.Scan(&id, &raw); e != nil {
			rows.Close()
			return e
		}
		var t protocol.TaskEnvelope
		if e = json.Unmarshal([]byte(raw), &t); e != nil {
			rows.Close()
			return e
		}
		if t.TargetDeviceID == device {
			ids = append(ids, id)
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, id := range ids {
		s.queue(device, "task.reconcile", id, protocol.TaskRequest{TaskID: id})
	}
	return nil
}
