package coordinator

import (
	"context"
	edgelink "github.com/h1neolzr7f/dsh-edge-orchestrator/packages/edge-link"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestApprovalDecisionLifecycle(t *testing.T) {
	for _, action := range []string{"approved", "denied", "cancel", "expire"} {
		t.Run(action, func(t *testing.T) {
			s := setup(t)
			p := samplePlan()
			p.Tasks[0].RequiresApproval = true
			p.Tasks[0].ApprovalReason = "operator permission"
			if action == "expire" {
				d := time.Now().Add(250 * time.Millisecond)
				p.Tasks[0].Deadline = &d
			}
			if _, e := s.Submit(p); e != nil {
				t.Fatal(e)
			}
			deadline := time.Now().Add(2 * time.Second)
			var a []protocol.Approval
			for time.Now().Before(deadline) {
				a, _ = s.Approvals()
				if len(a) > 0 {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if len(a) != 1 {
				t.Fatal("missing approval")
			}
			snap, _ := s.Plan("p")
			if snap.Status != "waiting_approval" {
				t.Fatal(snap.Status)
			}
			if action == "cancel" {
				if _, e := s.Control("p", "cancel"); e != nil {
					t.Fatal(e)
				}
			} else if action == "expire" {
				time.Sleep(350 * time.Millisecond)
			} else {
				if _, e := s.ResolveApproval(a[0].ID, protocol.ApprovalDecision{Decision: action}); e != nil {
					t.Fatal(e)
				}
				if _, e := s.ResolveApproval(a[0].ID, protocol.ApprovalDecision{Decision: action}); e != nil {
					t.Fatal(e)
				}
				opposite := "denied"
				if action == "denied" {
					opposite = "approved"
				}
				if _, e := s.ResolveApproval(a[0].ID, protocol.ApprovalDecision{Decision: opposite}); e == nil {
					t.Fatal("conflicting resolution accepted")
				}
				if action == "approved" {
					s.mu.Lock()
					_, e := s.db.Exec(`UPDATE tasks SET status='dispatched' WHERE id='t'`)
					s.mu.Unlock()
					if e != nil {
						t.Fatal(e)
					}
					s.ResolveApproval(a[0].ID, protocol.ApprovalDecision{Decision: action})
					snap, _ = s.Plan("p")
					if snap.Tasks[0].Status != "dispatched" {
						t.Fatal("repeat approval requeued task")
					}
				}
			}
			if action == "cancel" || action == "expire" {
				if _, e := s.ResolveApproval(a[0].ID, protocol.ApprovalDecision{Decision: "approved"}); e == nil {
					t.Fatal("terminal approval resurrected")
				}
				snap, _ = s.Plan("p")
				if !protocol.Terminal(snap.Tasks[0].Status) {
					t.Fatal(snap.Tasks[0].Status)
				}
			}
		})
	}
}
func TestPairingTLSAtomicClaim(t *testing.T) {
	s, e := Open(Config{DBPath: t.TempDir() + "/c.db", ControllerToken: "controller"})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ts := httptest.NewTLSServer(s.Handler())
	defer ts.Close()
	c := edgelink.NewClient(ts.URL, "controller", ts.Client())
	p, e := c.CreatePairing(context.Background(), protocol.PairingRequest{DeviceHint: "w", CoordinatorURL: ts.URL})
	if e != nil {
		t.Fatal(e)
	}
	var successes atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, e := c.ClaimPairing(context.Background(), protocol.PairingClaim{PairingCode: p.PairingCode, DeviceID: "w"})
			if e == nil {
				if len(out.WorkerToken) < 40 {
					t.Error("weak token")
				}
				successes.Add(1)
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 1 {
		t.Fatal(successes.Load())
	}
}

func TestPairingExpiryBindingHashAndRestart(t *testing.T) {
	path := t.TempDir() + "/authority.db"
	cfg := Config{DBPath: path, ControllerToken: "controller", WorkerTokens: map[string]string{"static": "static-secret"}}
	s, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.CreatePairing(protocol.PairingRequest{DeviceHint: "paired", CoordinatorURL: "https://localhost"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimPairing(protocol.PairingClaim{PairingCode: p.PairingCode, DeviceID: "wrong"}); err == nil {
		t.Fatal("hint not enforced")
	}
	credential, err := s.ClaimPairing(protocol.PairingClaim{PairingCode: p.PairingCode, DeviceID: "paired"})
	if err != nil {
		t.Fatal(err)
	}
	var stored string
	s.mu.Lock()
	err = s.db.QueryRow(`SELECT token_hash FROM device_credentials WHERE device_id='paired'`).Scan(&stored)
	s.mu.Unlock()
	if err != nil || stored == credential.WorkerToken || stored != tokenHash(credential.WorkerToken) {
		t.Fatal("server must store only hash", err)
	}
	expired, err := s.CreatePairing(protocol.PairingRequest{CoordinatorURL: "https://localhost"})
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	_, err = s.db.Exec(`UPDATE pairings SET expires_at=0 WHERE code=?`, expired.PairingCode)
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimPairing(protocol.PairingClaim{PairingCode: expired.PairingCode, DeviceID: "new"}); err == nil {
		t.Fatal("expired code accepted")
	}
	s.Close()
	s, err = Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.workerDevice(credential.WorkerToken) != "paired" || s.workerDevice("static-secret") != "static" || s.workerDevice("bad") != "" {
		t.Fatal("credential authentication after restart")
	}
	ts := httptest.NewTLSServer(s.Handler())
	defer ts.Close()
	for _, token := range []string{"", credential.WorkerToken} {
		c := edgelink.NewClient(ts.URL, token, ts.Client())
		if _, err = c.Approvals(context.Background()); err == nil {
			t.Fatal("controller route exposed to worker")
		}
		if _, err = c.CreatePairing(context.Background(), protocol.PairingRequest{}); err == nil {
			t.Fatal("worker created pairing")
		}
	}
}

func TestDeniedApprovalRejectsDependent(t *testing.T) {
	s := setup(t)
	p := samplePlan()
	p.Tasks[0].RequiresApproval = true
	next := p.Tasks[0]
	next.TaskID = "dependent"
	next.RequiresApproval = false
	next.DependsOn = []string{p.Tasks[0].TaskID}
	p.Tasks = append(p.Tasks, next)
	if _, e := s.Submit(p); e != nil {
		t.Fatal(e)
	}
	var list []protocol.Approval
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		list, _ = s.Approvals()
		if len(list) == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(list) != 1 {
		t.Fatal("approval must follow dependencies")
	}
	if _, e := s.ResolveApproval(list[0].ID, protocol.ApprovalDecision{Decision: "denied"}); e != nil {
		t.Fatal(e)
	}
	snap, e := s.Plan(p.PlanID)
	if e != nil {
		t.Fatal(e)
	}
	codes := map[string]string{}
	for _, task := range snap.Tasks {
		if task.Error != nil {
			codes[task.Task.TaskID] = task.Error.Code
		}
	}
	if codes["t"] != "APPROVAL_DENIED" || codes["dependent"] != "DEPENDENCY_FAILED" || snap.Status != "failed" {
		t.Fatalf("%+v", snap)
	}
}
