package coordinator

import (
	"encoding/json"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	"path/filepath"
	"testing"
	"time"
)

func setup(t *testing.T) *Server {
	t.Helper()
	s, e := Open(Config{DBPath: filepath.Join(t.TempDir(), "test.db"), ControllerToken: "controller", WorkerTokens: map[string]string{"w": "worker"}})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func samplePlan() protocol.PlanEnvelope {
	return protocol.PlanEnvelope{PlanID: "p", IdempotencyKey: "chat:1", Goal: "inspect", ConcurrencyLimit: 2, CreatedAt: time.Now().UTC(), Tasks: []protocol.TaskEnvelope{{TaskID: "t", TargetDeviceID: "w", Capability: protocol.CapabilityRef{ID: "device.inspect", Version: ">=1 <2"}, Input: json.RawMessage(`{}`), DependsOn: []string{}}}}
}
func TestSubmitAtomicIdempotency(t *testing.T) {
	s := setup(t)
	p := samplePlan()
	a, e := s.Submit(p)
	if e != nil {
		t.Fatal(e)
	}
	b, e := s.Submit(p)
	if e != nil || a.PlanID != b.PlanID {
		t.Fatal(e)
	}
	p.Goal = "changed"
	if _, e = s.Submit(p); e == nil {
		t.Fatal("conflict accepted")
	}
	bad := samplePlan()
	bad.PlanID = "bad"
	bad.IdempotencyKey = "other"
	bad.Tasks[0].DependsOn = []string{"absent"}
	if _, e = s.Submit(bad); e == nil {
		t.Fatal("bad DAG accepted")
	}
	snap, e := s.Snapshot()
	if e != nil || len(snap.Plans) != 1 {
		t.Fatalf("partial insert %+v %v", snap, e)
	}
}
func TestOrderedEventsAndReplay(t *testing.T) {
	s := setup(t)
	p := samplePlan()
	if _, e := s.Submit(p); e != nil {
		t.Fatal(e)
	}
	s.mu.Lock()
	_, e := s.db.Exec(`UPDATE tasks SET status='accepted' WHERE id='t'`)
	s.mu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	ev := func(n int64, kind string) protocol.TaskEvent {
		return protocol.TaskEvent{EventID: protocol.NewID("evt"), PlanID: "p", TaskID: "t", DeviceID: "w", WorkerSeq: n, TaskSeq: n, Type: kind, Timestamp: time.Now().UTC(), Payload: json.RawMessage(`{}`)}
	}
	done := ev(2, "task.succeeded")
	ack, e := s.AppendEvent("w", done)
	if e != nil || ack != 0 {
		t.Fatalf("gap ack=%d %v", ack, e)
	}
	state, _ := s.Plan("p")
	if state.Status == "succeeded" {
		t.Fatal("gap unlocked state")
	}
	ack, e = s.AppendEvent("w", ev(1, "task.started"))
	if e != nil || ack != 2 {
		t.Fatalf("ack=%d %v", ack, e)
	}
	state, _ = s.Plan("p")
	if state.Status != "succeeded" {
		t.Fatal(state.Status)
	}
	before, _ := s.Events(0, 0, 500)
	ack, e = s.AppendEvent("w", done)
	after, _ := s.Events(0, 0, 500)
	if e != nil || ack != 2 || len(before.Events) != len(after.Events) {
		t.Fatal("duplicate changed ledger")
	}
	if _, e = s.Events(99999, 0, 10); e == nil {
		t.Fatal("future cursor accepted")
	}
}
func TestUnassignedQueuedTaskRejectsWorkerEvents(t *testing.T) {
	s := setup(t)
	p := samplePlan()
	p.Tasks[0].TargetDeviceID = ""
	if _, e := s.Submit(p); e != nil {
		t.Fatal(e)
	}
	s.Control(p.PlanID, "pause")
	_, e := s.AppendEvent("w", protocol.TaskEvent{EventID: "evt_forged", PlanID: "p", TaskID: "t", DeviceID: "w", WorkerSeq: 1, TaskSeq: 1, Type: "task.failed", Timestamp: time.Now().UTC(), Payload: json.RawMessage(`{}`)})
	if e == nil {
		t.Fatal("worker mutated an unassigned task")
	}
	snap, e := s.Plan("p")
	if e != nil || snap.Status != "queued" || snap.Tasks[0].Status != "queued" {
		t.Fatal("rejected event changed task state")
	}
}

func TestPlanSnapshotHydratesRecordedLogTail(t *testing.T) {
	s := setup(t)
	s.Submit(samplePlan())
	s.mu.Lock()
	s.db.Exec(`UPDATE tasks SET status='running' WHERE id='t'`)
	s.mu.Unlock()
	ev := protocol.TaskEvent{EventID: "cold_log", PlanID: "p", TaskID: "t", DeviceID: "w", WorkerSeq: 1, TaskSeq: 1, Type: "task.log", Timestamp: time.Now().UTC(), Payload: json.RawMessage(`{"text":"before snapshot\n"}`)}
	if _, e := s.AppendEvent("w", ev); e != nil {
		t.Fatal(e)
	}
	snap, e := s.Plan("p")
	if e != nil {
		t.Fatal(e)
	}
	if snap.Tasks[0].LogTail != "before snapshot\n" {
		t.Fatalf("lost pre-barrier log: %q", snap.Tasks[0].LogTail)
	}
}
