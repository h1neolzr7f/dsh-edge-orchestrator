package edgelink

import (
	"context"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestControllerAndClaimNeverFollowRedirects(t *testing.T) {
	var reached atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Add(1)
		io.Copy(io.Discard, r.Body)
		w.Write([]byte(`[]`))
	}))
	defer target.Close()
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	c := NewClient(source.URL, "controller", source.Client())
	if _, e := c.Devices(context.Background()); e == nil {
		t.Error("controller redirected")
	}
	c.Token = ""
	if _, e := c.ClaimPairing(context.Background(), protocol.PairingClaim{PairingCode: "private code", DeviceID: "w"}); e == nil {
		t.Error("claim redirected")
	}
	if conn, e := Dial(context.Background(), source.URL+"/v1/link", "worker", source.Client()); e == nil {
		conn.Close()
		t.Error("worker link redirected")
	}
	if reached.Load() != 0 {
		t.Fatalf("target received %d requests", reached.Load())
	}
}
