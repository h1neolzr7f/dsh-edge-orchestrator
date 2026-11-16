package coordinator

import (
	"encoding/json"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/orchestrator"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	"time"
)

func (s *Server) scheduleLoop() {
	defer s.wg.Done()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.mu.Lock()
			if !s.closing {
				s.lastError = s.schedule()
			}
			s.mu.Unlock()
		}
	}
}
func (s *Server) queue(device, kind, id string, v any) {
	if sess := s.sessions[device]; sess != nil {
		select {
		case sess.queue <- wire{kind, id, v}:
		default:
			sess.conn.Close()
		}
	}
}
func (s *Server) schedule() error {
	devices, e := s.devicesLocked(s.db)
	if e != nil {
		return e
	}
	rows, e := s.db.Query(`SELECT id FROM plans WHERE status NOT IN ('succeeded','failed','cancelled') ORDER BY id`)
	if e != nil {
		return e
	}
	var ids []string
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, id := range ids {
		tx, e := s.db.Begin()
		if e != nil {
			return e
		}
		err := func() error {
			defer tx.Rollback()
			p, e := loadPlan(tx, id)
			if e != nil {
				return e
			}
			active := 0
			deviceLoad := map[string]int{}
			capLoad := map[string]int{}
			loadRows, e := tx.Query(`SELECT payload FROM tasks WHERE status IN ('dispatched','accepted','running','waiting_approval')`)
			if e != nil {
				return e
			}
			for loadRows.Next() {
				var b string
				if e = loadRows.Scan(&b); e != nil {
					loadRows.Close()
					return e
				}
				var t protocol.TaskEnvelope
				if e = json.Unmarshal([]byte(b), &t); e != nil {
					loadRows.Close()
					return e
				}
				deviceLoad[t.TargetDeviceID]++
				capLoad[t.TargetDeviceID+":"+t.Capability.ID]++
			}
			e = loadRows.Err()
			loadRows.Close()
			if e != nil {
				return e
			}
			for _, t := range p.Tasks {
				if !protocol.Terminal(t.Status) && t.Status != "queued" {
					active++
				}
			}
			type send struct {
				device, kind, id string
				payload          any
			}
			var sends []send
			for _, snap := range p.Tasks {
				t := snap.Task
				if protocol.Terminal(snap.Status) {
					continue
				}
				if t.Deadline != nil && !time.Now().Before(*t.Deadline) && snap.Status == "queued" {
					if _, e = tx.Exec(`UPDATE tasks SET status='failed',error=? WHERE id=?`, encode(&protocol.Error{Code: "DEADLINE_EXCEEDED", Message: "deadline passed before dispatch"}), t.TaskID); e != nil {
						return e
					}
					if e = refreshPlan(tx, id); e != nil {
						return e
					}
					continue
				}
				if p.CancelRequested {
					if snap.Status != "queued" && s.sessions[t.TargetDeviceID] != nil {
						var last int64
						if e = tx.QueryRow(`SELECT last_dispatch FROM tasks WHERE id=?`, t.TaskID).Scan(&last); e != nil {
							return e
						}
						if time.Since(time.UnixMilli(last)) > 200*time.Millisecond {
							if _, e = tx.Exec(`UPDATE tasks SET last_dispatch=? WHERE id=?`, time.Now().UnixMilli(), t.TaskID); e != nil {
								return e
							}
							sends = append(sends, send{t.TargetDeviceID, "task.cancel", t.TaskID, protocol.TaskRequest{TaskID: t.TaskID}}, send{t.TargetDeviceID, "task.reconcile", t.TaskID, protocol.TaskRequest{TaskID: t.TaskID}})
						}
					}
					continue
				}
				// Retransmit on a lost receipt or a re-established worker session; Worker ledger deduplicates.
				if snap.Status == "dispatched" {
					var last int64
					if e = tx.QueryRow(`SELECT last_dispatch FROM tasks WHERE id=?`, t.TaskID).Scan(&last); e != nil {
						return e
					}
					if s.sessions[t.TargetDeviceID] != nil && time.Since(time.UnixMilli(last)) > 500*time.Millisecond {
						if _, e = tx.Exec(`UPDATE tasks SET last_dispatch=? WHERE id=?`, time.Now().UnixMilli(), t.TaskID); e != nil {
							return e
						}
						sends = append(sends, send{t.TargetDeviceID, "task.submit", t.TaskID, t})
					}
					continue
				}
				if snap.Status != "queued" {
					continue
				}
				reason := ""
				switch {
				case !isReady(t, p.Tasks):
					reason = "waiting_dependencies"
				case p.SchedulingPaused:
					reason = "scheduling_paused"
				case active >= p.Plan.ConcurrencyLimit:
					reason = "concurrency_limit"
				}
				pending := func(reason string) error {
					if snap.PendingReason == reason {
						return nil
					}
					if _, err := tx.Exec(`UPDATE tasks SET pending_reason=? WHERE id=?`, reason, t.TaskID); err != nil {
						return err
					}
					_, err := planEvent(tx, id, "plan.updated", p.Status)
					return err
				}
				if reason != "" {
					if e = pending(reason); e != nil {
						return e
					}
					continue
				}
				chosen := ""
				online, capable := false, false
				for _, d := range devices {
					if !d.Online || (t.TargetDeviceID != "" && t.TargetDeviceID != d.DeviceID) {
						continue
					}
					online = true
					if !orchestrator.Supports(d.Capabilities, t.Capability) {
						continue
					}
					capable = true
					capacity := 0
					for _, cap := range d.Capabilities {
						if cap.CapabilityID == t.Capability.ID {
							capacity = cap.MaxConcurrency
						}
					}
					if capLoad[d.DeviceID+":"+t.Capability.ID] >= capacity {
						continue
					}
					if chosen == "" || deviceLoad[d.DeviceID] < deviceLoad[chosen] {
						chosen = d.DeviceID
					}
				}
				if chosen == "" {
					reason = "device_busy"
					if !online {
						reason = "device_offline"
					} else if !capable {
						reason = "capability_unavailable"
					}
					if e = pending(reason); e != nil {
						return e
					}
					continue
				}
				t.TargetDeviceID = chosen
				if t.Capability.ID == "report.aggregate" {
					type report struct {
						TaskID string          `json:"task_id"`
						Result json.RawMessage `json:"result"`
					}
					results := []report{}
					for _, dep := range t.DependsOn {
						for _, x := range p.Tasks {
							if x.Task.TaskID == dep {
								result := x.Result
								if result == nil {
									result = json.RawMessage(`null`)
								}
								results = append(results, report{dep, result})
							}
						}
					}
					t.Input = json.RawMessage(encode(map[string]any{"results": results}))
				}
				if _, e = tx.Exec(`UPDATE tasks SET pending_reason='',status='dispatched',payload=?,last_dispatch=? WHERE id=?`, encode(t), time.Now().UnixMilli(), t.TaskID); e != nil {
					return e
				}
				if _, e = planEvent(tx, id, "plan.updated", p.Status); e != nil {
					return e
				}
				active++
				deviceLoad[chosen]++
				capLoad[chosen+":"+t.Capability.ID]++
				sends = append(sends, send{chosen, "task.submit", t.TaskID, t})
			}
			if e = tx.Commit(); e != nil {
				return e
			}
			for _, m := range sends {
				s.queue(m.device, m.kind, m.id, m.payload)
			}
			return nil
		}()
		if err != nil {
			return err
		}
	}
	return nil
}
func isReady(t protocol.TaskEnvelope, tasks []protocol.TaskSnapshot) bool {
	for _, d := range t.DependsOn {
		ok := false
		for _, s := range tasks {
			if s.Task.TaskID == d && s.Status == "succeeded" {
				ok = true
			}
		}
		if !ok {
			return false
		}
	}
	return true
}
