package agent

import (
	coordinator "github.com/h1neolzr7f/dsh-edge-orchestrator/apps/coordinator"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
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
