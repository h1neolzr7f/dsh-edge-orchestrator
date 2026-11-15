package tests

import (
	"context"
	"encoding/json"
	coordinator "github.com/h1neolzr7f/dsh-edge-orchestrator/apps/coordinator"
	agent "github.com/h1neolzr7f/dsh-edge-orchestrator/apps/edge-agent"
	edgelink "github.com/h1neolzr7f/dsh-edge-orchestrator/packages/edge-link"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

// Written before the runtime: this test must exercise actual TLS sockets and SQLite.
func TestPhase0Phase1OfflineDAG(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s, err := coordinator.Open(coordinator.Config{DBPath: filepath.Join(t.TempDir(), "coord.db"), ControllerToken: "controller", WorkerTokens: map[string]string{"worker-win-sim": "win-secret", "worker-linux-sim": "linux-secret"}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ts := httptest.NewTLSServer(s.Handler())
	defer ts.Close()
	started := make(chan string, 4)
	release := make(chan struct{})
	finished := make(chan string, 4)
	execute := func(ctx context.Context, task protocol.TaskEnvelope) (json.RawMessage, error) {
		started <- task.TaskID
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		finished <- task.TaskID
		return json.RawMessage(`{"checked":true}`), nil
	}
	var workers []*agent.Agent
	for _, d := range []struct{ id, token string }{{"worker-win-sim", "win-secret"}, {"worker-linux-sim", "linux-secret"}} {
		a, e := agent.Open(agent.Config{DBPath: filepath.Join(t.TempDir(), "agent.db"), DeviceID: d.id, Token: d.token, CoordinatorURL: ts.URL, HTTPClient: ts.Client(), Executor: execute})
		if e != nil {
			t.Fatal(e)
		}
		workers = append(workers, a)
		defer a.Close()
		go a.Run(ctx)
	}
	c := edgelink.NewClient(ts.URL, "controller", ts.Client())
	await(t, ctx, func() bool { ds, e := c.Devices(ctx); return e == nil && len(ds) == 2 && ds[0].Online && ds[1].Online })
	plan := protocol.PlanEnvelope{PlanID: "plan-e2e", IdempotencyKey: "chat:turn1", Goal: "parallel inspect then aggregate", ConcurrencyLimit: 2, CreatedAt: time.Now().UTC(), Tasks: []protocol.TaskEnvelope{
		{TaskID: "inspect-win", TargetDeviceID: "worker-win-sim", Capability: protocol.CapabilityRef{ID: "device.inspect", Version: ">=1.0 <2.0"}, Input: json.RawMessage(`{}`), DependsOn: []string{}},
		{TaskID: "inspect-linux", TargetDeviceID: "worker-linux-sim", Capability: protocol.CapabilityRef{ID: "device.inspect", Version: ">=1.0 <2.0"}, Input: json.RawMessage(`{}`), DependsOn: []string{}},
		{TaskID: "summary", TargetDeviceID: "worker-linux-sim", Capability: protocol.CapabilityRef{ID: "report.aggregate", Version: ">=1.0 <2.0"}, Input: json.RawMessage(`{}`), DependsOn: []string{"inspect-win", "inspect-linux"}},
	}}
	accepted, e := c.Submit(ctx, plan)
	if e != nil {
		t.Fatal(e)
	}
	cursor := accepted.CoordSeq
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("front tasks did not overlap")
		}
	}
	// Force physical reconnection and repeat the same task envelopes while work is in flight.
	snapshot, e := c.Plan(ctx, plan.PlanID)
	if e != nil {
		t.Fatal(e)
	}
	for _, w := range workers {
		w.Reconnect()
		for _, task := range snapshot.Tasks {
			if task.Status == "queued" {
				continue
			}
			if task.Task.TargetDeviceID == w.Registration().Manifest.DeviceID {
				_, fresh, e := w.Accept(task.Task)
				if e != nil || fresh {
					t.Fatalf("duplicate receipt %v fresh=%v", e, fresh)
				}
			}
		}
	}
	// No controller calls between release and the third task starting.
	close(release)
	for i := 0; i < 2; i++ {
		select {
		case <-finished:
		case <-ctx.Done():
			t.Fatal("front tasks stalled")
		}
	}
	select {
	case id := <-started:
		if id != "summary" {
			t.Fatalf("downstream=%s", id)
		}
	case <-ctx.Done():
		t.Fatal("DAG depended on controller")
	}
	await(t, ctx, func() bool { p, e := c.Plan(ctx, plan.PlanID); return e == nil && p.Status == "succeeded" })
	same, e := c.Submit(ctx, plan)
	if e != nil || same.PlanID != accepted.PlanID {
		t.Fatalf("plan dedup: %+v %v", same, e)
	}
	for _, id := range []string{"inspect-win", "inspect-linux", "summary"} {
		total := 0
		for _, w := range workers {
			n, e := w.ExecutionCount(id)
			if e != nil {
				t.Fatal(e)
			}
			total += n
		}
		if total != 1 {
			t.Fatalf("execution count %s=%d", id, total)
		}
	}
	// Paginate one event at a time after the saved Controller cursor.
	var got []protocol.TaskEvent
	through := int64(0)
	for {
		page, e := c.Events(ctx, cursor, through, 1)
		if e != nil {
			t.Fatal(e)
		}
		if through == 0 {
			through = page.HighWatermark
		}
		for _, ev := range page.Events {
			if ev.CoordSeq <= cursor {
				t.Fatal("duplicate/out of order event")
			}
			got = append(got, ev)
			cursor = ev.CoordSeq
		}
		if !page.HasMore {
			break
		}
	}
	all, e := c.Events(ctx, accepted.CoordSeq, through, 500)
	if e != nil {
		t.Fatal(e)
	}
	if len(got) != len(all.Events) || len(got) < 7 {
		t.Fatalf("replay mismatch %d/%d", len(got), len(all.Events))
	}
}
func await(t *testing.T, ctx context.Context, f func() bool) {
	t.Helper()
	for !f() {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}
