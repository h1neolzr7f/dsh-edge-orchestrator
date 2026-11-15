package agent

import (
	"encoding/json"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	"path/filepath"
	"testing"
)

func TestReceiptPersistsAndRejectsConflict(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.db")
	a, e := Open(Config{DBPath: path, DeviceID: "w", Token: "secret", CoordinatorURL: "https://localhost"})
	if e != nil {
		t.Fatal(e)
	}
	task := protocol.TaskEnvelope{TaskID: "t", PlanID: "p", IdempotencyKey: "p:t", TargetDeviceID: "w", Capability: protocol.CapabilityRef{ID: "device.inspect", Version: "1.x"}, DependsOn: []string{}, Input: json.RawMessage(`{}`)}
	first, fresh, e := a.Accept(task)
	if e != nil || !fresh {
		t.Fatal(e)
	}
	a.Close()
	a, e = Open(Config{DBPath: path, DeviceID: "w", Token: "secret", CoordinatorURL: "https://localhost"})
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	next, fresh, e := a.Accept(task)
	if e != nil || fresh || !first.AcceptedAt.Equal(next.AcceptedAt) {
		t.Fatalf("receipt lost %+v %v", next, e)
	}
	task.Input = json.RawMessage(`{"different":true}`)
	if _, _, e = a.Accept(task); e == nil {
		t.Fatal("conflict accepted")
	}
}
func TestCrashRecoveryNeverReexecutesClaimedTask(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.db")
	cfg := Config{DBPath: path, DeviceID: "w", Token: "secret", CoordinatorURL: "https://localhost"}
	a, e := Open(cfg)
	if e != nil {
		t.Fatal(e)
	}
	task := protocol.TaskEnvelope{TaskID: "t", PlanID: "p", IdempotencyKey: "p:t", TargetDeviceID: "w", Capability: protocol.CapabilityRef{ID: "device.inspect", Version: "1.x"}, Input: json.RawMessage(`{}`), DependsOn: []string{}}
	if _, _, e = a.Accept(task); e != nil {
		t.Fatal(e)
	}
	if ok, e := a.claim(task); e != nil || !ok {
		t.Fatal(e)
	}
	a.Close()
	a, e = Open(cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	pending, e := a.recoverTasks()
	if e != nil || len(pending) != 0 {
		t.Fatal("claimed task rescheduled")
	}
	out, e := a.outbox()
	if e != nil || len(out) != 2 || out[1].Type != "task.failed" {
		t.Fatalf("outbox=%+v %v", out, e)
	}
	var p protocol.EventPayload
	if e = json.Unmarshal(out[1].Payload, &p); e != nil || p.Error == nil || p.Error.Code != "interrupted_by_restart" {
		t.Fatal("missing restart status")
	}
	if n, _ := a.ExecutionCount("t"); n != 1 {
		t.Fatalf("executions=%d", n)
	}
}
func TestCancellationTombstoneSurvivesRestart(t *testing.T) {
	cfg := Config{DBPath: filepath.Join(t.TempDir(), "a.db"), DeviceID: "w", Token: "secret", CoordinatorURL: "https://localhost"}
	a, e := Open(cfg)
	if e != nil {
		t.Fatal(e)
	}
	if e = a.cancelTask("late"); e != nil {
		t.Fatal(e)
	}
	a.Close()
	a, e = Open(cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	task := protocol.TaskEnvelope{TaskID: "late", PlanID: "p", IdempotencyKey: "p:late", TargetDeviceID: "w", Capability: protocol.CapabilityRef{ID: "device.inspect", Version: "1.x"}, DependsOn: []string{}, Input: json.RawMessage(`{}`)}
	if _, _, e = a.Accept(task); e != nil {
		t.Fatal(e)
	}
	if claimed, e := a.claim(task); e != nil || claimed {
		t.Fatalf("cancelled task claimed: %v %v", claimed, e)
	}
	if n, _ := a.ExecutionCount("late"); n != 0 {
		t.Fatal("cancelled task executed")
	}
	events, e := a.outbox()
	if e != nil || len(events) != 1 || events[0].Type != "task.cancelled" {
		t.Fatal("missing cancellation event")
	}
}
