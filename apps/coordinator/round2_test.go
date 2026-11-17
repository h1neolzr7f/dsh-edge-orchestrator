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
	if err != nil || water != 2 || ack != 2 || snap.Tasks[0].Status != "failed" || snap.Tasks[0].Error == nil || snap.Tasks[0].Error.Code != "PROTOCOL_VIOLATION" {
		t.Fatalf("valid prefix rolled back water=%d ack=%d status=%s err=%v", water, ack, snap.Tasks[0].Status, e)
	}
	page, err := s.Events(0, 0, 500)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ev := range page.Events {
		if ev.Type == "task.started" && ev.WorkerSeq == 1 {
			found = true
		}
	}
	if !found {
		t.Fatal("valid prefix event rolled back")
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

func TestPoisonBufferedEventDoesNotWedgeSubsequentWorkerReconnect(t *testing.T) {
	s := acceptedPlan(t)
	p := samplePlan()
	p.PlanID = "other"
	p.IdempotencyKey = "other"
	p.Tasks[0].TaskID = "other_task"
	if _, err := s.Submit(p); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE tasks SET status='accepted' WHERE id='other_task'`); err != nil {
		t.Fatal(err)
	}
	poison := event(2, 99, "task.succeeded")
	if ack, err := s.AppendEvent("w", poison); err != nil || ack != 0 {
		t.Fatalf("buffer poison ack=%d err=%v", ack, err)
	}
	if ack, err := s.AppendEvent("w", event(1, 1, "task.started")); ack < 1 {
		t.Fatalf("valid prefix not acknowledged: %d %v", ack, err)
	}
	next := event(3, 1, "task.started")
	next.PlanID = "other"
	next.TaskID = "other_task"
	if ack, err := s.AppendEvent("w", next); err != nil || ack != 3 {
		t.Fatalf("reconnect wedged by historical poison: ack=%d err=%v", ack, err)
	}
	if ack, err := s.AppendEvent("w", poison); err != nil || ack != 3 {
		t.Fatalf("quarantined replay ack=%d err=%v", ack, err)
	}
	poison.Payload = json.RawMessage(`{"status":"changed"}`)
	if _, err := s.AppendEvent("w", poison); err == nil {
		t.Fatal("quarantine discarded conflict hash")
	}
	snap, err := s.Plan("other")
	if err != nil || snap.Tasks[0].Status != "running" {
		t.Fatalf("later valid task not applied: %+v %v", snap, err)
	}
	var payload string
	if err := s.db.QueryRow(`SELECT payload FROM worker_event_inbox WHERE device_id='w' AND worker_seq=2`).Scan(&payload); err != nil || payload != "" {
		t.Fatalf("poison not quarantined: %q %v", payload, err)
	}
}
