package tests

import (
	"context"
	"encoding/json"
	coordinator "github.com/h1neolzr7f/dsh-edge-orchestrator/apps/coordinator"
	agent "github.com/h1neolzr7f/dsh-edge-orchestrator/apps/edge-agent"
	edgelink "github.com/h1neolzr7f/dsh-edge-orchestrator/packages/edge-link"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestCoordinatorRestartContinuesDAG(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	cfg := coordinator.Config{DBPath: filepath.Join(t.TempDir(), "c.db"), ControllerToken: "c", WorkerTokens: map[string]string{"w": "w-secret"}}
	s, e := coordinator.Open(cfg)
	if e != nil {
		t.Fatal(e)
	}
	var current atomic.Pointer[coordinator.Server]
	current.Store(s)
	defer func() { current.Load().Close() }()
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { current.Load().Handler().ServeHTTP(w, r) }))
	defer ts.Close()
	started := make(chan string, 4)
	release := make(chan struct{})
	w, e := agent.Open(agent.Config{DBPath: filepath.Join(t.TempDir(), "w.db"), DeviceID: "w", Token: "w-secret", CoordinatorURL: ts.URL, HTTPClient: ts.Client(), Executor: func(ctx context.Context, task protocol.TaskEnvelope) (json.RawMessage, error) {
		started <- task.TaskID
		if task.TaskID == "first" {
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return json.RawMessage(`{"ok":true}`), nil
	}})
	if e != nil {
		t.Fatal(e)
	}
	defer w.Close()
	go w.Run(ctx)
	c := edgelink.NewClient(ts.URL, "c", ts.Client())
	await(t, ctx, func() bool { d, e := c.Devices(ctx); return e == nil && len(d) == 1 && d[0].Online })
	p := protocol.PlanEnvelope{PlanID: "restart", IdempotencyKey: "restart:1", Goal: "recover", ConcurrencyLimit: 1, CreatedAt: time.Now().UTC(), Tasks: []protocol.TaskEnvelope{{TaskID: "first", TargetDeviceID: "w", Capability: protocol.CapabilityRef{ID: "device.inspect", Version: "1.x"}, DependsOn: []string{}, Input: json.RawMessage(`{}`)}, {TaskID: "last", TargetDeviceID: "w", Capability: protocol.CapabilityRef{ID: "report.aggregate", Version: "1.x"}, DependsOn: []string{"first"}, Input: json.RawMessage(`{}`)}}}
	if _, e = c.Submit(ctx, p); e != nil {
		t.Fatal(e)
	}
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if e = s.Close(); e != nil {
		t.Fatal(e)
	}
	replacement, e := coordinator.Open(cfg)
	if e != nil {
		t.Fatal(e)
	}
	current.Store(replacement)
	close(release)
	await(t, ctx, func() bool { p, e := c.Plan(ctx, "restart"); return e == nil && p.Status == "succeeded" })
	for _, id := range []string{"first", "last"} {
		n, e := w.ExecutionCount(id)
		if e != nil || n != 1 {
			t.Fatalf("%s executions=%d %v", id, n, e)
		}
	}
}
func TestBuiltinAggregateAndPersistedSnapshot(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	s, e := coordinator.Open(coordinator.Config{DBPath: filepath.Join(t.TempDir(), "c.db"), ControllerToken: "c", WorkerTokens: map[string]string{"w": "w-token"}})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ts := httptest.NewTLSServer(s.Handler())
	defer ts.Close()
	w, e := agent.Open(agent.Config{DBPath: filepath.Join(t.TempDir(), "w.db"), DeviceID: "w", Token: "w-token", CoordinatorURL: ts.URL, HTTPClient: ts.Client()})
	if e != nil {
		t.Fatal(e)
	}
	defer w.Close()
	go w.Run(ctx)
	c := edgelink.NewClient(ts.URL, "c", ts.Client())
	await(t, ctx, func() bool { d, e := c.Devices(ctx); return e == nil && len(d) == 1 && d[0].Online })
	p := protocol.PlanEnvelope{PlanID: "builtin", IdempotencyKey: "builtin:1", Goal: "inspect aggregate", ConcurrencyLimit: 1, CreatedAt: time.Now().UTC(), Tasks: []protocol.TaskEnvelope{{TaskID: "inspect", TargetDeviceID: "w", Capability: protocol.CapabilityRef{ID: "device.inspect", Version: "1.x"}, DependsOn: []string{}, Input: json.RawMessage(`{}`)}, {TaskID: "aggregate", TargetDeviceID: "w", Capability: protocol.CapabilityRef{ID: "report.aggregate", Version: "1.x"}, DependsOn: []string{"inspect"}, Input: json.RawMessage(`{}`)}}}
	if _, e = c.Submit(ctx, p); e != nil {
		t.Fatal(e)
	}
	await(t, ctx, func() bool { p, e := c.Plan(ctx, "builtin"); return e == nil && p.Status == "succeeded" })
	snapshot, e := c.Snapshot(ctx)
	if e != nil {
		t.Fatal(e)
	}
	var found bool
	for _, task := range snapshot.Plans[0].Tasks {
		if task.Task.TaskID == "aggregate" {
			var report struct {
				Count   int `json:"count"`
				Reports []struct {
					TaskID string                  `json:"task_id"`
					Result protocol.DeviceManifest `json:"result"`
				} `json:"reports"`
			}
			if e = json.Unmarshal(task.Result, &report); e != nil {
				t.Fatal(e)
			}
			if report.Count != 1 || len(report.Reports) != 1 || report.Reports[0].Result.DeviceID != "w" || report.Reports[0].Result.Resources.CPUThreads < 1 {
				t.Fatalf("invalid summary %s", task.Result)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("summary missing")
	}
	empty, e := c.Events(ctx, snapshot.SnapshotCoordSeq, 0, 500)
	if e != nil || len(empty.Events) != 0 {
		t.Fatal("snapshot barrier replayed old events")
	}
}
func TestPauseResumeAndCancel(t *testing.T) {
	s, e := coordinator.Open(coordinator.Config{DBPath: filepath.Join(t.TempDir(), "c.db"), ControllerToken: "c", WorkerTokens: map[string]string{"offline": "token"}})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	p := protocol.PlanEnvelope{PlanID: "p", IdempotencyKey: "p:1", Goal: "pause", ConcurrencyLimit: 1, CreatedAt: time.Now().UTC(), Tasks: []protocol.TaskEnvelope{{TaskID: "task", TargetDeviceID: "offline", Capability: protocol.CapabilityRef{ID: "device.inspect", Version: "1.x"}, DependsOn: []string{}, Input: json.RawMessage(`{}`)}}}
	if _, e = s.Submit(p); e != nil {
		t.Fatal(e)
	}
	snap, e := s.Control("p", "pause")
	if e != nil || !snap.SchedulingPaused {
		t.Fatal("pause not persisted")
	}
	snap, e = s.Control("p", "resume")
	if e != nil || snap.SchedulingPaused {
		t.Fatal("resume failed")
	}
	snap, e = s.Control("p", "cancel")
	if e != nil || snap.Status != "cancelled" {
		t.Fatalf("cancel %+v %v", snap, e)
	}
	page, e := s.Events(0, 0, 500)
	if e != nil {
		t.Fatal(e)
	}
	n := 0
	for _, ev := range page.Events {
		if ev.Type == "plan.completed" {
			n++
		}
	}
	s.Control("p", "cancel")
	again, _ := s.Events(0, 0, 500)
	if n != 1 || len(again.Events) != len(page.Events) {
		t.Fatal("duplicate terminal event")
	}
}
func TestCancelAfterLostSubmitDoesNotStartWork(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	s, e := coordinator.Open(coordinator.Config{DBPath: filepath.Join(t.TempDir(), "c.db"), ControllerToken: "c", WorkerTokens: map[string]string{"w": "w-secret"}})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ts := httptest.NewTLSServer(s.Handler())
	defer ts.Close()
	a, e := agent.Open(agent.Config{DBPath: filepath.Join(t.TempDir(), "w.db"), DeviceID: "w", Token: "w-secret", CoordinatorURL: ts.URL, HTTPClient: ts.Client()})
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	raw, e := edgelink.Dial(ctx, ts.URL+"/v1/link", "w-secret", ts.Client())
	if e != nil {
		t.Fatal(e)
	}
	defer raw.Close()
	if e = raw.Send(ctx, "client.hello", "w", protocol.ClientHello{Role: "worker", DeviceID: "w", AgentVersion: "0.1.0", ProtocolVersions: []string{protocol.Version}, PendingMessageIDs: []string{}}); e != nil {
		t.Fatal(e)
	}
	if _, e = raw.Receive(ctx); e != nil {
		t.Fatal(e)
	}
	raw.Send(ctx, "device.register", "w", a.Registration())
	if _, e = raw.Receive(ctx); e != nil {
		t.Fatal(e)
	}
	raw.Send(ctx, "link.ready", "w", map[string]bool{"ready": true})
	c := edgelink.NewClient(ts.URL, "c", ts.Client())
	p := protocol.PlanEnvelope{PlanID: "lost", IdempotencyKey: "lost:1", Goal: "cancel lost delivery", ConcurrencyLimit: 1, CreatedAt: time.Now().UTC(), Tasks: []protocol.TaskEnvelope{{TaskID: "never-execute", TargetDeviceID: "w", Capability: protocol.CapabilityRef{ID: "device.inspect", Version: "1.x"}, DependsOn: []string{}, Input: json.RawMessage(`{}`)}}}
	if _, e = c.Submit(ctx, p); e != nil {
		t.Fatal(e)
	}
	m, e := raw.Receive(ctx)
	if e != nil || m.Kind != "task.submit" {
		t.Fatalf("expected submit %+v %v", m, e)
	}
	raw.Close()
	if _, e = c.Control(ctx, "lost", "cancel"); e != nil {
		t.Fatal(e)
	}
	go a.Run(ctx)
	await(t, ctx, func() bool { p, e := c.Plan(ctx, "lost"); return e == nil && p.Status == "cancelled" })
	if n, e := a.ExecutionCount("never-execute"); e != nil || n != 0 {
		t.Fatalf("cancelled task ran: %d %v", n, e)
	}
}
