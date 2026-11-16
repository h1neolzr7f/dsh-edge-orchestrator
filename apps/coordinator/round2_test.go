package coordinator

import (
	"encoding/json"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	"testing"
	"time"
)

func event(n, taskseq int64, kind string) protocol.TaskEvent {
	return protocol.TaskEvent{EventID: protocol.NewID("evt"), PlanID: "p", TaskID: "t", DeviceID: "w", WorkerSeq: n, TaskSeq: taskseq, Type: kind, Timestamp: time.Now().UTC(), Payload: json.RawMessage(`{}`)}
}
func acceptedPlan(t *testing.T) *Server {
	t.Helper()
	s := setup(t)
	if _, e := s.Submit(samplePlan()); e != nil {
		t.Fatal(e)
	}
	if _, e := s.db.Exec(`UPDATE tasks SET status='accepted' WHERE id='t'`); e != nil {
		t.Fatal(e)
	}
	return s
}
func TestOutOfOrderPoisonEventDoesNotRollbackPriorValidEvent(t *testing.T) {
	s := acceptedPlan(t)
	if _, e := s.AppendEvent("w", event(2, 99, "task.succeeded")); e != nil {
		t.Fatal(e)
	}
	ack, e := s.AppendEvent("w", event(1, 1, "task.started"))
	if e == nil {
		t.Fatal("poison event accepted")
	}
	var water int64
	s.db.QueryRow(`SELECT last_worker_seq FROM devices WHERE id='w'`).Scan(&water)
	snap, err := s.Plan("p")
	if err != nil || water != 1 || ack != 1 || snap.Tasks[0].Status != "running" {
		t.Fatalf("valid prefix rolled back water=%d ack=%d status=%s err=%v", water, ack, snap.Tasks[0].Status, e)
	}
}
func TestWorkerEventInboxPayloadCompacted(t *testing.T) {
	s := acceptedPlan(t)
	ev := event(1, 1, "task.started")
	if _, e := s.AppendEvent("w", ev); e != nil {
		t.Fatal(e)
	}
	var payload string
	s.db.QueryRow(`SELECT payload FROM worker_event_inbox WHERE device_id='w' AND worker_seq=1`).Scan(&payload)
	if payload != "" {
		t.Fatal("applied payload duplicated in inbox")
	}
	if ack, e := s.AppendEvent("w", ev); e != nil || ack != 1 {
		t.Fatalf("historical duplicate lost %d %v", ack, e)
	}
	ev.Payload = json.RawMessage(`{"status":"other"}`)
	if _, e := s.AppendEvent("w", ev); e == nil {
		t.Fatal("compacted event no longer detects conflict")
	}
}
func TestQueuedTaskExposesPendingReason(t *testing.T) {
	s := setup(t)
	p := samplePlan()
	if _, e := s.Submit(p); e != nil {
		t.Fatal(e)
	}
	s.Control("p", "pause")
	s.mu.Lock()
	e := s.schedule()
	s.mu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	snap, e := s.Plan("p")
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(snap.Tasks[0])
	var fields map[string]any
	json.Unmarshal(b, &fields)
	if fields["pending_reason"] != "scheduling_paused" {
		t.Fatalf("missing scheduling reason: %s", b)
	}
}
