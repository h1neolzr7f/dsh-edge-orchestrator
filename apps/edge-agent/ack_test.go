package agent

import (
	"context"
	"errors"
	edgelink "github.com/h1neolzr7f/dsh-edge-orchestrator/packages/edge-link"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestBatchedACKRejectsUnproducedEvents(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, e := edgelink.Accept(w, r)
		if e != nil {
			return
		}
		defer c.Close()
		if _, e = c.Receive(ctx); e != nil {
			return
		}
		c.Send(ctx, "server.welcome", "w", protocol.ServerWelcome{ConnectionID: "conn_test", ServerTime: time.Now().UTC(), SelectedProtocol: protocol.Version, HeartbeatInterval: 20})
		if _, e = c.Receive(ctx); e != nil {
			return
		}
		c.Send(ctx, "device.registered", "w", map[string]string{"device_id": "w"})
		if _, e = c.Receive(ctx); e != nil {
			return
		}
		c.Send(ctx, "event.ack", "w", protocol.EventAck{LastWorkerSeq: 999})
		c.Receive(ctx)
	}))
	defer ts.Close()
	a, e := Open(Config{DBPath: filepath.Join(t.TempDir(), "a.db"), DeviceID: "w", Token: "s", CoordinatorURL: ts.URL, HTTPClient: ts.Client()})
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	e = a.connect()
	var pe *protocol.Error
	if !errors.As(e, &pe) || pe.Code != "INVALID_EVENT_ACK" {
		t.Fatalf("invalid batched ACK did not propagate: %v", e)
	}
}
