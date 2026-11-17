package tests

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	coordinator "github.com/h1neolzr7f/dsh-edge-orchestrator/apps/coordinator"
	agent "github.com/h1neolzr7f/dsh-edge-orchestrator/apps/edge-agent"
	edgelink "github.com/h1neolzr7f/dsh-edge-orchestrator/packages/edge-link"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Real TLS link, real worker and child process: approval blocks execution while
// independent work still uses the one available Plan/device execution slot.
func TestPhase6ApprovalGateAndPairedWorkerArtifacts(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	path := filepath.Join(t.TempDir(), "c.db")
	s, e := coordinator.Open(coordinator.Config{DBPath: path, ControllerToken: "controller"})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	ts := httptest.NewTLSServer(s.Handler())
	defer ts.Close()
	c := edgelink.NewClient(ts.URL, "controller", ts.Client())
	pair, e := c.CreatePairing(ctx, protocol.PairingRequest{DeviceHint: "worker-a", CoordinatorURL: ts.URL})
	if e != nil {
		t.Fatal(e)
	}
	root := t.TempDir()
	exe, _ := os.Executable()
	db := filepath.Join(t.TempDir(), "a.db")
	cfg := agent.Config{DBPath: db, DeviceID: "worker-a", CoordinatorURL: ts.URL, PairingCode: pair.PairingCode, HTTPClient: ts.Client(), Workspaces: map[string]string{"workspace/project": root}, AllowedPrograms: []string{exe}}
	a, e := agent.Open(cfg)
	if e != nil {
		t.Fatal(e)
	}
	a.Close()
	// A fresh Open has no injected token/pairing code; it must reuse saved identity.
	cfg.PairingCode = ""
	cfg.DeviceID = ""
	a, e = agent.Open(cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	go a.Run(ctx)
	await(t, ctx, func() bool { d, e := c.Devices(ctx); return e == nil && len(d) == 1 && d[0].Online })
	gated := task("a_gated", "process.exec", map[string]any{"program": exe, "args": []string{"--phase2-workload", "log"}, "cwd": "workspace/project", "timeout_ms": 5000})
	gated.RequiresApproval = true
	gated.ApprovalReason = "Run real local tests"
	gated.OutputArtifacts = []protocol.OutputArtifact{{Name: "report", Path: "test-report.txt", MediaType: "text/plain"}}
	ordinary := task("b_inspect", "device.inspect", map[string]any{})
	consumer := task("c_consume", "filesystem.read", map[string]any{"workspace": "workspace/project", "path": "mounted/report.txt"})
	consumer.DependsOn = []string{gated.TaskID}
	consumer.InputArtifactBindings = []protocol.ArtifactBinding{{FromTaskID: gated.TaskID, Name: "report", MountPath: "mounted/report.txt"}}
	p := protocol.PlanEnvelope{PlanID: "auth_plan", IdempotencyKey: "auth_key", Goal: "approval gate", ConcurrencyLimit: 1, CreatedAt: time.Now().UTC(), Tasks: []protocol.TaskEnvelope{gated, ordinary, consumer}}
	if _, e = c.Submit(ctx, p); e != nil {
		t.Fatal(e)
	}
	var approval protocol.Approval
	await(t, ctx, func() bool {
		list, e := c.Approvals(ctx)
		if e != nil || len(list) != 1 {
			return false
		}
		approval = list[0]
		snap, e := c.Plan(ctx, p.PlanID)
		return e == nil && snap.Tasks[0].Status == "waiting_approval" && snap.Tasks[1].Status == "succeeded"
	})
	if _, e = os.Stat(filepath.Join(root, "test-report.txt")); !os.IsNotExist(e) {
		t.Fatal("unapproved process executed")
	}
	if _, e = c.ResolveApproval(ctx, approval.ID, protocol.ApprovalDecision{Decision: "approved", Reason: "operator checked task"}); e != nil {
		t.Fatal(e)
	}
	var final protocol.PlanSnapshot
	await(t, ctx, func() bool { final, e = c.Plan(ctx, p.PlanID); return e == nil && protocol.Terminal(final.Status) })
	if final.Status != "succeeded" {
		t.Fatalf("%+v", final)
	}
	if len(final.Tasks[0].Artifacts) != 1 {
		t.Fatal("paired worker could not upload artifact")
	}
	ref := final.Tasks[0].Artifacts[0]
	download := filepath.Join(t.TempDir(), "report.txt")
	if e = c.DownloadArtifactFile(ctx, ref.ArtifactID, download, ref.SHA256); e != nil {
		t.Fatal(e)
	}
	data, e := os.ReadFile(download)
	if e != nil {
		t.Fatal(e)
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != ref.SHA256 {
		t.Fatal("artifact corrupt")
	}
	if _, e = c.ResolveApproval(ctx, approval.ID, protocol.ApprovalDecision{Decision: "approved"}); e != nil {
		t.Fatal(e)
	}
	after, e := c.Plan(ctx, p.PlanID)
	if e != nil || after.Tasks[0].Status != "succeeded" {
		t.Fatal("repeat approval reset execution", e)
	}
	// Hash-only server credential survives a Coordinator restart.
	a.Close()
	ts.Close()
	s.Close()
	s, e = coordinator.Open(coordinator.Config{DBPath: path, ControllerToken: "controller"})
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
}
