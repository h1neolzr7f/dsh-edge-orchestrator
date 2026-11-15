package edgelink

import (
	"context"
	"github.com/coder/websocket"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTLSFramesAndReconnectReset(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, e := websocket.Accept(w, r, nil)
		if e != nil {
			return
		}
		c := New(ws)
		defer c.Close()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		m, e := c.Receive(ctx)
		if e == nil {
			_ = c.Send(ctx, "reply", m.CorrelationID, map[string]bool{"ok": true})
		}
	}))
	defer ts.Close()
	for i := 0; i < 2; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		c, e := Dial(ctx, ts.URL+"/link", "token", ts.Client())
		if e != nil {
			t.Fatal(e)
		}
		if e = c.Send(ctx, "request", "x", map[string]int{"n": 1}); e != nil {
			t.Fatal(e)
		}
		m, e := c.Receive(ctx)
		if e != nil {
			t.Fatal(e)
		}
		if m.Sequence != 1 || m.Ack != 1 {
			t.Fatalf("frame not reset: %+v", m)
		}
		c.Close()
		cancel()
	}
}
