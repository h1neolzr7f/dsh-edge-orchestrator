package tests

import (
	"context"
	"database/sql"
	"encoding/json"
	agent "github.com/h1neolzr7f/dsh-edge-orchestrator/apps/edge-agent"
	edgelink "github.com/h1neolzr7f/dsh-edge-orchestrator/packages/edge-link"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The relay drops cumulative business ACKs 1..249 while forwarding every event
// into the real Coordinator. Physical connection ACKs and sequencing stay live.
func TestPhase2DelayedACKDrainsOver100Events(t *testing.T) {
	// Durable ACK/log integrity can require many SQLite transactions under race.
	// Live process cancellation has its own unchanged strict two-second bound.
	e := newPhase2(t, 45*time.Second)
	upstreamURL := e.cfgA.CoordinatorURL
	upstreamHTTP := e.cfgA.HTTPClient
	var held atomic.Bool
	var maxAck atomic.Int64
	relay := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		down, err := edgelink.Accept(w, r)
		if err != nil {
			return
		}
		defer down.Close()
		ctx, cancel := context.WithCancel(e.ctx)
		defer cancel()
		up, err := edgelink.Dial(ctx, upstreamURL+"/v1/link", strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), upstreamHTTP)
		if err != nil {
			t.Error(err)
			return
		}
		defer up.Close()
		done := make(chan struct{})
		go func() {
			defer close(done)
			for {
				m, err := down.Receive(ctx)
				if err != nil {
					up.Close()
					return
				}
				if err = up.Send(ctx, m.Kind, m.CorrelationID, m.Payload); err != nil {
					down.Close()
					return
				}
			}
		}()
		defer func() { cancel(); down.Close(); up.Close(); <-done }()
		for {
			m, err := up.Receive(ctx)
			if err != nil {
				return
			}
			if m.Kind == "event.ack" {
				var ack protocol.EventAck
				if json.Unmarshal(m.Payload, &ack) != nil {
					return
				}
				maxAck.Store(ack.LastWorkerSeq)
				if ack.LastWorkerSeq < 250 {
					held.Store(true)
					continue
				}
			}
			if err = down.Send(ctx, m.Kind, m.CorrelationID, m.Payload); err != nil {
				return
			}
		}
	}))
	t.Cleanup(relay.Close)
	e.a.Close()
	cfg := e.cfgA
	cfg.CoordinatorURL = relay.URL
	cfg.HTTPClient = relay.Client()
	a, err := agent.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	go a.Run(e.ctx)
	db, err := sql.Open("sqlite", cfg.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		// The fixture context may already be expired. Use a short independent
		// diagnostic context while the agent and its local ledger remain open.
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		status := "unknown"
		var generated, ack, pending int64
		err := db.QueryRowContext(ctx, `SELECT COALESCE((SELECT status FROM local_task_ledger WHERE id='flood'),'missing'),worker_seq,last_ack,(SELECT COUNT(*) FROM event_outbox) FROM counters WHERE id=1`).Scan(&status, &generated, &ack, &pending)
		t.Logf("delayed ACK diagnostics: local_status=%s generated=%d ack=%d pending=%d max_ack=%d ledger_error=%v", status, generated, ack, pending, maxAck.Load(), err)
	})
	exe, _ := os.Executable()
	snap := e.run(t, task("flood", "process.exec", map[string]any{"program": exe, "args": []string{"--phase2-workload", "flood"}, "cwd": "workspace/project", "timeout_ms": 5000}))
	if snap.Status != "succeeded" || !held.Load() || maxAck.Load() < 250 {
		t.Fatalf("ACK-delayed transport stalled: status=%s max_ack=%d", snap.Status, maxAck.Load())
	}
	await(t, e.ctx, func() bool {
		var n int
		err := db.QueryRow(`SELECT COUNT(*) FROM event_outbox`).Scan(&n)
		return err == nil && n == 0
	})
	page, err := e.c.Events(e.ctx, 0, 0, 500)
	if err != nil {
		t.Fatal(err)
	}
	logs := 0
	for _, ev := range page.Events {
		if ev.TaskID == "flood" && ev.Type == "task.log" {
			logs++
		}
	}
	if logs < 200 {
		t.Fatalf("real Coordinator missing log batch: %d", logs)
	}
}
