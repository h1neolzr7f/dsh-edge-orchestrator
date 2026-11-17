package agent

import (
	coordinator "github.com/h1neolzr7f/dsh-edge-orchestrator/apps/coordinator"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPairedIdentitySecureReload(t *testing.T) {
	s, e := coordinator.Open(coordinator.Config{DBPath: filepath.Join(t.TempDir(), "c.db"), ControllerToken: "controller"})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ts := httptest.NewTLSServer(s.Handler())
	defer ts.Close()
	pair, e := s.CreatePairing(protocol.PairingRequest{DeviceHint: "w", CoordinatorURL: ts.URL})
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(t.TempDir(), "worker.db")
	a, e := Open(Config{DBPath: path, DeviceID: "w", CoordinatorURL: ts.URL, PairingCode: pair.PairingCode, HTTPClient: ts.Client()})
	if e != nil {
		t.Fatal(e)
	}
	if len(a.cfg.Token) < 40 {
		t.Fatal("missing paired token")
	}
	token := a.cfg.Token
	// WAL/SHM are present while SQLite is open. All must restrict plaintext access.
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if st, e := os.Stat(path + suffix); e != nil || runtime.GOOS != "windows" && st.Mode().Perm()&0077 != 0 {
			t.Fatalf("unsafe %s: %v %v", suffix, st, e)
		}
	}
	a.Close()
	a, e = Open(Config{DBPath: path, CoordinatorURL: ts.URL, HTTPClient: ts.Client()})
	if e != nil {
		t.Fatal(e)
	}
	if a.cfg.Token != token || a.cfg.DeviceID != "w" {
		t.Fatal("credential did not reload")
	}
	a.Close()
	if bad, e := Open(Config{DBPath: path, CoordinatorURL: "https://other.example"}); e == nil {
		bad.Close()
		t.Fatal("credential sent to different endpoint")
	}
	if _, e = s.ClaimPairing(protocol.PairingClaim{PairingCode: pair.PairingCode, DeviceID: "w"}); e == nil {
		t.Fatal("reused code")
	}
}

type lossTransport struct {
	base http.RoundTripper
	drop bool
}

func (x *lossTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	res, e := x.base.RoundTrip(r)
	if e == nil && x.drop && r.URL.Path == "/v1/pairings/claim" {
		x.drop = false
		io.Copy(io.Discard, res.Body)
		res.Body.Close()
		return nil, io.ErrUnexpectedEOF
	}
	return res, e
}
func TestLostEnrollmentResponseRecoversSameAgentIdentity(t *testing.T) {
	s, e := coordinator.Open(coordinator.Config{DBPath: filepath.Join(t.TempDir(), "c.db"), ControllerToken: "controller"})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ts := httptest.NewTLSServer(s.Handler())
	defer ts.Close()
	pair, e := s.CreatePairing(protocol.PairingRequest{DeviceHint: "lost-worker", CoordinatorURL: ts.URL})
	if e != nil {
		t.Fatal(e)
	}
	lossy := *ts.Client()
	lossy.Transport = &lossTransport{base: ts.Client().Transport, drop: true}
	cfg := Config{DBPath: filepath.Join(t.TempDir(), "a.db"), DeviceID: "lost-worker", PairingCode: pair.PairingCode, CoordinatorURL: ts.URL, HTTPClient: &lossy}
	if a, e := Open(cfg); e == nil {
		a.Close()
		t.Fatal("response loss not injected")
	}
	recovery, e := s.CreatePairing(protocol.PairingRequest{DeviceHint: "lost-worker", CoordinatorURL: ts.URL, RecoverExisting: true})
	if e != nil {
		t.Fatal(e)
	}
	cfg.PairingCode = recovery.PairingCode
	cfg.HTTPClient = ts.Client()
	a, e := Open(cfg)
	if e != nil {
		t.Fatal(e)
	}
	token := a.cfg.Token
	a.Close()
	cfg.PairingCode = ""
	cfg.DeviceID = ""
	a, e = Open(cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	if a.cfg.DeviceID != "lost-worker" || a.cfg.Token != token {
		t.Fatal("recovered identity did not persist")
	}
	if _, e = s.ClaimPairing(protocol.PairingClaim{PairingCode: recovery.PairingCode, DeviceID: "lost-worker"}); e == nil {
		t.Fatal("independent claimant reused code")
	}
}
