package coordinator

import (
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const authoritySchema = `
CREATE TABLE IF NOT EXISTS approvals(id TEXT PRIMARY KEY,plan_id TEXT NOT NULL,task_id TEXT UNIQUE NOT NULL,device_id TEXT,scope TEXT NOT NULL,reason TEXT NOT NULL,status TEXT NOT NULL,decision_reason TEXT,created_at TEXT NOT NULL,resolved_at TEXT);
CREATE TABLE IF NOT EXISTS pairings(code TEXT PRIMARY KEY,device_hint TEXT,expires_at INTEGER NOT NULL,used INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS device_credentials(device_id TEXT PRIMARY KEY,token_hash TEXT NOT NULL,created_at TEXT NOT NULL);`

func scanApproval(row interface{ Scan(...any) error }) (protocol.Approval, error) {
	var a protocol.Approval
	var created string
	var resolved, reason, device sql.NullString
	e := row.Scan(&a.ID, &a.PlanID, &a.TaskID, &device, &a.Scope, &a.Reason, &a.Status, &reason, &created, &resolved)
	if e != nil {
		return a, e
	}
	a.DeviceID = device.String
	a.DecisionReason = reason.String
	a.CreatedAt, e = time.Parse(time.RFC3339Nano, created)
	if e != nil {
		return a, e
	}
	if resolved.Valid {
		v, err := time.Parse(time.RFC3339Nano, resolved.String)
		if err != nil {
			return a, err
		}
		a.ResolvedAt = &v
	}
	return a, nil
}
func (s *Server) Approvals() ([]protocol.Approval, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []protocol.Approval{}
	rows, e := s.db.Query(`SELECT * FROM approvals ORDER BY created_at,id`)
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		a, e := scanApproval(rows)
		if e != nil {
			return out, e
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
func approvalEvent(tx *sql.Tx, a protocol.Approval, kind string) error {
	_, e := emit(tx, protocol.TaskEvent{EventID: protocol.NewID("evt"), PlanID: a.PlanID, TaskID: a.TaskID, Type: kind, Timestamp: time.Now().UTC(), Payload: json.RawMessage(encode(a))})
	return e
}
func requestApproval(tx *sql.Tx, t protocol.TaskEnvelope) (bool, error) {
	var status string
	e := tx.QueryRow(`SELECT status FROM approvals WHERE task_id=?`, t.TaskID).Scan(&status)
	if e == nil {
		return status != "approved", nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return false, e
	}
	a := protocol.Approval{ID: protocol.NewID("approval"), PlanID: t.PlanID, TaskID: t.TaskID, DeviceID: t.TargetDeviceID, Scope: t.Capability.ID, Reason: t.ApprovalReason, Status: "pending", CreatedAt: time.Now().UTC()}
	_, e = tx.Exec(`INSERT INTO approvals(id,plan_id,task_id,device_id,scope,reason,status,created_at) VALUES(?,?,?,?,?,?,?,?)`, a.ID, a.PlanID, a.TaskID, a.DeviceID, a.Scope, a.Reason, a.Status, a.CreatedAt.Format(time.RFC3339Nano))
	if e != nil {
		return false, e
	}
	if _, e = tx.Exec(`UPDATE tasks SET status='waiting_approval',pending_reason='' WHERE id=? AND status='queued'`, t.TaskID); e != nil {
		return false, e
	}
	if e = approvalEvent(tx, a, "approval.requested"); e != nil {
		return false, e
	}
	return true, refreshPlan(tx, t.PlanID)
}

// invalidateApproval runs in the same transaction that makes a waiting task terminal.
func invalidateApproval(tx *sql.Tx, taskID, status string) error {
	a, e := scanApproval(tx.QueryRow(`SELECT * FROM approvals WHERE task_id=? AND status='pending'`, taskID))
	if errors.Is(e, sql.ErrNoRows) {
		return nil
	}
	if e != nil {
		return e
	}
	now := time.Now().UTC()
	a.Status = status
	a.ResolvedAt = &now
	if _, e = tx.Exec(`UPDATE approvals SET status=?,resolved_at=? WHERE id=?`, status, now.Format(time.RFC3339Nano), a.ID); e != nil {
		return e
	}
	return approvalEvent(tx, a, "approval.resolved")
}
func (s *Server) ResolveApproval(id string, d protocol.ApprovalDecision) (protocol.Approval, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var a protocol.Approval
	if d.Decision != "approved" && d.Decision != "denied" {
		return a, protocol.Err("INVALID_PAYLOAD", "decision must be approved or denied")
	}
	tx, e := s.db.Begin()
	if e != nil {
		return a, e
	}
	defer tx.Rollback()
	a, e = scanApproval(tx.QueryRow(`SELECT * FROM approvals WHERE id=?`, id))
	if errors.Is(e, sql.ErrNoRows) {
		return a, protocol.Err("APPROVAL_NOT_FOUND", id)
	}
	if e != nil {
		return a, e
	}
	if a.Status != "pending" {
		if a.Status == d.Decision {
			return a, nil
		}
		return a, protocol.Err("APPROVAL_CONFLICT", "approval already resolved")
	}
	p, e := loadPlan(tx, a.PlanID)
	if e != nil {
		return a, e
	}
	var task protocol.TaskSnapshot
	for _, t := range p.Tasks {
		if t.Task.TaskID == a.TaskID {
			task = t
			break
		}
	}
	if p.CancelRequested || protocol.Terminal(p.Status) || task.Status != "waiting_approval" || task.Task.Deadline != nil && !time.Now().Before(*task.Task.Deadline) {
		return a, protocol.Err("APPROVAL_INACTIVE", "task no longer awaits approval")
	}
	now := time.Now().UTC()
	a.Status = d.Decision
	a.DecisionReason = d.Reason
	a.ResolvedAt = &now
	if _, e = tx.Exec(`UPDATE approvals SET status=?,decision_reason=?,resolved_at=? WHERE id=?`, a.Status, a.DecisionReason, now.Format(time.RFC3339Nano), id); e != nil {
		return a, e
	}
	if d.Decision == "approved" {
		_, e = tx.Exec(`UPDATE tasks SET status='queued' WHERE id=? AND status='waiting_approval'`, a.TaskID)
	} else {
		_, e = tx.Exec(`UPDATE tasks SET status='rejected',error=? WHERE id=? AND status='waiting_approval'`, encode(protocol.Err("APPROVAL_DENIED", d.Reason)), a.TaskID)
	}
	if e != nil {
		return a, e
	}
	if e = approvalEvent(tx, a, "approval.resolved"); e != nil {
		return a, e
	}
	if e = refreshPlan(tx, a.PlanID); e != nil {
		return a, e
	}
	return a, tx.Commit()
}
func decodeAuthority(w http.ResponseWriter, r *http.Request, out any) error {
	b, e := ioReadAuthority(w, r)
	if e != nil {
		return protocol.Err("INVALID_PAYLOAD", "invalid request body")
	}
	if e = protocol.Decode(b, out); e != nil {
		return protocol.Err("INVALID_PAYLOAD", e.Error())
	}
	return nil
}
func (s *Server) authorityHTTP(w http.ResponseWriter, r *http.Request) {
	var out any
	var e error
	switch {
	case r.URL.Path == "/v1/approvals" && r.Method == "GET":
		out, e = s.Approvals()
	case strings.HasPrefix(r.URL.Path, "/v1/approvals/") && strings.HasSuffix(r.URL.Path, "/resolve") && r.Method == "POST":
		var d protocol.ApprovalDecision
		if e = decodeAuthority(w, r, &d); e == nil {
			out, e = s.ResolveApproval(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/approvals/"), "/resolve"), d)
		}
	case r.URL.Path == "/v1/pairings" && r.Method == "POST":
		var in protocol.PairingRequest
		if e = decodeAuthority(w, r, &in); e == nil {
			if in.CoordinatorURL == "" {
				in.CoordinatorURL = "https://" + r.Host
			}
			u, err := url.Parse(in.CoordinatorURL)
			if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
				e = protocol.Err("INVALID_PAYLOAD", "valid HTTPS coordinator URL required")
			} else {
				out, e = s.CreatePairing(in)
			}
		}
	default:
		http.NotFound(w, r)
		return
	}
	if e != nil {
		writeError(w, e)
		return
	}
	writeJSON(w, 200, out)
}
