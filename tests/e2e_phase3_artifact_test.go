package tests

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	agent "github.com/h1neolzr7f/dsh-edge-orchestrator/apps/edge-agent"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func artifactRequest(t *testing.T, e *phase2Env, method, path, token string, body io.Reader) (int, []byte, http.Header) {
	t.Helper()
	r, err := http.NewRequestWithContext(e.ctx, method, e.c.URL+path, body)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Authorization", "Bearer "+token)
	res, err := e.c.HTTP.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, b, res.Header
}
func uploadFixture(t *testing.T, e *phase2Env, data []byte, taskID, name string) protocol.ArtifactRef {
	t.Helper()
	h := sha256.Sum256(data)
	meta := input(map[string]any{"plan_id": "future-plan", "task_id": taskID, "name": name, "media_type": "application/octet-stream", "size_bytes": len(data), "sha256": hex.EncodeToString(h[:])})
	status, b, _ := artifactRequest(t, e, "POST", "/v1/artifacts/uploads", "c", bytes.NewReader(meta))
	if status != 200 {
		t.Fatalf("create upload HTTP %d: %s", status, b)
	}
	var u struct {
		UploadID string `json:"upload_id"`
	}
	json.Unmarshal(b, &u)
	status, b, _ = artifactRequest(t, e, "PATCH", "/v1/artifacts/uploads/"+u.UploadID+"?offset=0", "c", bytes.NewReader(data))
	if status != 200 {
		t.Fatalf("chunk %d %s", status, b)
	}
	status, b, _ = artifactRequest(t, e, "POST", "/v1/artifacts/uploads/"+u.UploadID+"/complete", "c", nil)
	if status != 200 {
		t.Fatalf("complete %d %s", status, b)
	}
	var ref protocol.ArtifactRef
	if err := json.Unmarshal(b, &ref); err != nil {
		t.Fatal(err)
	}
	return ref
}

func TestArtifactChunkedUploadResumeAndRangeDownload(t *testing.T) {
	e := newPhase2(t)
	data := make([]byte, 10<<20)
	for i := range data {
		data[i] = byte(i * 31)
	}
	h := sha256.Sum256(data)
	meta := input(map[string]any{"plan_id": "future-plan", "task_id": "staged", "name": "binary", "media_type": "application/octet-stream", "size_bytes": len(data), "sha256": hex.EncodeToString(h[:])})
	create := func() (string, int64) {
		status, b, _ := artifactRequest(t, e, "POST", "/v1/artifacts/uploads", "c", bytes.NewReader(meta))
		if status != 200 {
			t.Fatalf("create upload HTTP %d: %s", status, b)
		}
		var u struct {
			UploadID string `json:"upload_id"`
			Offset   int64  `json:"confirmed_offset"`
		}
		json.Unmarshal(b, &u)
		return u.UploadID, u.Offset
	}
	id, _ := create()
	status, b, _ := artifactRequest(t, e, "PATCH", "/v1/artifacts/uploads/"+id+"?offset=0", "c", bytes.NewReader(data[:4<<20]))
	if status != 200 {
		t.Fatalf("first chunk %d %s", status, b)
	}
	// Real TLS request advertises a full chunk, but closes the stream halfway.
	pr, pw := io.Pipe()
	r, _ := http.NewRequestWithContext(e.ctx, "PATCH", e.c.URL+"/v1/artifacts/uploads/"+id+"?offset=4194304", pr)
	r.ContentLength = 4 << 20
	r.Header.Set("Authorization", "Bearer c")
	go func() { pw.Write(data[4<<20 : 6<<20]); pw.CloseWithError(io.ErrUnexpectedEOF) }()
	res, _ := e.c.HTTP.Do(r)
	if res != nil {
		res.Body.Close()
	}
	id, offset := create()
	if offset != 4<<20 {
		t.Fatalf("durable offset %d", offset)
	}
	for offset < int64(len(data)) {
		end := offset + 4<<20
		if end > int64(len(data)) {
			end = int64(len(data))
		}
		status, b, _ = artifactRequest(t, e, "PATCH", "/v1/artifacts/uploads/"+id+"?offset="+fmtInt(offset), "c", bytes.NewReader(data[offset:end]))
		if status != 200 {
			t.Fatalf("resume %d %s", status, b)
		}
		offset = end
	}
	status, b, _ = artifactRequest(t, e, "POST", "/v1/artifacts/uploads/"+id+"/complete", "c", nil)
	if status != 200 {
		t.Fatalf("complete %d %s", status, b)
	}
	var ref protocol.ArtifactRef
	json.Unmarshal(b, &ref)
	status, b, _ = artifactRequest(t, e, "POST", "/v1/artifacts/uploads/"+id+"/complete", "c", nil)
	if status != 200 {
		t.Fatalf("repeat complete %d %s", status, b)
	}
	r, _ = http.NewRequestWithContext(e.ctx, "GET", e.c.URL+ref.DownloadURI, nil)
	r.Header.Set("Authorization", "Bearer c")
	r.Header.Set("Range", "bytes=4000000-5000000")
	res, err := e.c.HTTP.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ = io.ReadAll(res.Body)
	if res.StatusCode != 206 || !bytes.Equal(b, data[4000000:5000001]) || res.Header.Get("ETag") == "" || res.Header.Get("Content-Range") != "bytes 4000000-5000000/10485760" {
		t.Fatal("inaccurate range")
	}
	status, b, _ = artifactRequest(t, e, "GET", ref.DownloadURI, "c", nil)
	if status != 200 || sha256.Sum256(b) != h {
		t.Fatal("full hash mismatch")
	}
}
func fmtInt(n int64) string { return fmt.Sprintf("%d", n) }
func TestDAGArtifactBindingAndDirtyPatchWorkflow(t *testing.T) {
	e := newPhase2(t)
	os.WriteFile(filepath.Join(e.root, "README.md"), []byte("patched fixture\n"), 0600)
	cmd := exec.Command("git", "-C", e.root, "diff", "--binary")
	patch, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	ref := uploadFixture(t, e, patch, "patch-stage", "patch")
	exe, _ := os.Executable()
	a := task("patch_exec", "process.exec", map[string]any{"program": exe, "args": []string{"--phase2-workload", "artifact-report"}, "cwd": "workspace/project"})
	a.OutputArtifacts = []protocol.OutputArtifact{{Path: "junit.xml", Name: "junit.xml", MediaType: "application/xml"}}
	b := task("patch_review", "git.inspect", map[string]any{"workspace": "workspace/project", "mode": "diff"})
	b.TargetDeviceID = "worker-b"
	c := task("consume", "filesystem.read", map[string]any{"workspace": "workspace/project", "path": "mounted/junit.xml"})
	c.DependsOn = []string{a.TaskID, b.TaskID}
	c.InputArtifactBindings = []protocol.ArtifactBinding{{FromTaskID: a.TaskID, Name: "junit.xml", MountPath: "mounted/junit.xml"}}
	for _, v := range []*protocol.TaskEnvelope{&a, &b, &c} {
		v.WorkspaceContext = &protocol.WorkspaceContext{RootDir: "workspace/project", ExpectedGitHead: e.head, PatchArtifactID: ref.ArtifactID, Policy: "require_clean_or_match"}
	}
	snap := e.run(t, a, b, c)
	if snap.Status != "succeeded" {
		t.Fatalf("workflow: %s", input(snap))
	}
	for _, v := range snap.Tasks {
		if v.Task.TaskID == a.TaskID && len(v.Artifacts) != 1 {
			t.Fatal("missing output")
		}
		if v.Task.TaskID == b.TaskID && !strings.Contains(string(v.Result), "patched fixture") {
			t.Fatalf("staged patch absent from inspect: %s", v.Result)
		}
		if v.Task.TaskID == c.TaskID && !strings.Contains(string(v.Result), "testsuite") {
			t.Fatalf("mounted read: %s", v.Result)
		}
	}
	host, _ := os.ReadFile(filepath.Join(e.root, "README.md"))
	if string(host) != "patched fixture\n" {
		t.Fatal("host modified")
	}
}

func TestArtifactPermissionsConflictAndMissingBinding(t *testing.T) {
	e := newPhase2(t)
	ref := uploadFixture(t, e, []byte("content"), "private-stage", "private")
	for _, token := range []string{"a-secret", "b-secret"} {
		status, _, _ := artifactRequest(t, e, "GET", ref.DownloadURI, token, nil)
		if status != 403 {
			t.Fatalf("unrequired download status %d", status)
		}
	}
	status, _, _ := artifactRequest(t, e, "GET", ref.DownloadURI, "wrong", nil)
	if status != 401 {
		t.Fatal("anonymous artifact allowed")
	}
	meta := input(protocol.ArtifactUploadRequest{PlanID: "future-plan", TaskID: "private-stage", Name: "private", MediaType: ref.MediaType, SizeBytes: 7, SHA256: strings.Repeat("0", 64)})
	status, b, _ := artifactRequest(t, e, "POST", "/v1/artifacts/uploads", "c", bytes.NewReader(meta))
	if status != 409 || !bytes.Contains(b, []byte("IDEMPOTENCY_CONFLICT")) {
		t.Fatalf("conflict: %d %s", status, b)
	}
	status, b, _ = artifactRequest(t, e, "POST", "/v1/artifacts/uploads", "a-secret", bytes.NewReader(meta))
	if status != 403 {
		t.Fatalf("unassigned upload: %d %s", status, b)
	}
	a := task("no_output", "device.inspect", map[string]any{})
	c := task("missing_binding", "filesystem.read", map[string]any{"workspace": "workspace/project", "path": "result"})
	c.DependsOn = []string{a.TaskID}
	c.InputArtifactBindings = []protocol.ArtifactBinding{{FromTaskID: a.TaskID, Name: "absent", MountPath: "result"}}
	snap := e.run(t, a, c)
	if snap.Status != "failed" {
		t.Fatalf("missing binding plan %s", input(snap))
	}
	for _, v := range snap.Tasks {
		if v.Task.TaskID == c.TaskID && (v.Status != "rejected" || v.Error == nil || v.Error.Code != "ARTIFACT_BINDING_FAILED") {
			t.Fatalf("binding error: %s", input(v))
		}
	}
}
func TestArtifactMountPreflightFailuresAndScratchIsolation(t *testing.T) {
	e := newPhase2(t)
	ref := uploadFixture(t, e, []byte("mounted value"), "mount-stage", "input")
	for _, tc := range []struct {
		name, id, hash, mount, code string
		git                         bool
	}{{"hash", ref.ArtifactID, strings.Repeat("0", 64), "input.txt", "ARTIFACT_HASH_MISMATCH", true}, {"missing", "missing-artifact", ref.SHA256, "input.txt", "ARTIFACT_NOT_FOUND", true}, {"conflict", ref.ArtifactID, ref.SHA256, "README.md", "ARTIFACT_MOUNT_CONFLICT", true}, {"scratch", ref.ArtifactID, ref.SHA256, "input.txt", "", false}} {
		t.Run(tc.name, func(t *testing.T) {
			job := task("mount_"+tc.name, "filesystem.read", map[string]any{"workspace": "workspace/project", "path": tc.mount})
			job.InputArtifacts = []protocol.InputArtifact{{ArtifactID: tc.id, SHA256: tc.hash, MountPath: tc.mount}}
			if tc.git {
				job.WorkspaceContext = &protocol.WorkspaceContext{RootDir: "workspace/project", ExpectedGitHead: e.head, Policy: "require_clean_or_match"}
			}
			snap := e.run(t, job)
			v := snap.Tasks[0]
			if tc.code == "" {
				if v.Status != "succeeded" || !strings.Contains(string(v.Result), "mounted value") {
					t.Fatalf("scratch read %s", input(v))
				}
			} else if v.Status != "failed" || v.Error == nil || v.Error.Code != tc.code {
				t.Fatalf("preflight %s", input(v))
			}
			page, err := e.c.Events(e.ctx, 0, 0, 500)
			if err != nil {
				t.Fatal(err)
			}
			for _, ev := range page.Events {
				if tc.code != "" && ev.TaskID == job.TaskID && ev.Type == "task.started" {
					t.Fatal("preflight failure entered running")
				}
			}
			if _, err = os.Stat(filepath.Join(e.root, "input.txt")); !os.IsNotExist(err) {
				t.Fatal("host mount mutation")
			}
		})
	}
}
func TestArtifactMissingDeclaredOutputFailsBeforeSuccess(t *testing.T) {
	e := newPhase2(t)
	exe, _ := os.Executable()
	job := task("missing_output", "process.exec", map[string]any{"program": exe, "args": []string{"--phase2-workload", "marker"}, "cwd": "workspace/project", "output_artifacts": []protocol.OutputArtifact{{Path: "missing.xml", Name: "junit.xml", MediaType: "application/xml"}}})
	job.WorkspaceContext = &protocol.WorkspaceContext{RootDir: "workspace/project", ExpectedGitHead: e.head, Policy: "require_clean_or_match"}
	snap := e.run(t, job)
	v := snap.Tasks[0]
	if v.Status != "failed" || v.Error == nil || v.Error.Code != "ARTIFACT_UPLOAD_FAILED" {
		t.Fatalf("missing output succeeded: %s", input(v))
	}
}

func TestArtifactOutputUploadResumesCompletedRunnerAfterRestart(t *testing.T) {
	e := newPhase2(t)
	e.a.Close()
	blocked := make(chan struct{}, 1)
	proxy := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "PATCH" && r.URL.Query().Get("offset") == "4194304" {
			io.Copy(io.Discard, r.Body)
			select {
			case blocked <- struct{}{}:
			default:
			}
			<-r.Context().Done()
			return
		}
		e.s.Handler().ServeHTTP(w, r)
	}))
	defer proxy.Close()
	cfg := e.cfgA
	cfg.CoordinatorURL = proxy.URL
	cfg.HTTPClient = proxy.Client()
	first, err := agent.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	go first.Run(e.ctx)
	await(t, e.ctx, func() bool { d, err := e.c.Devices(e.ctx); return err == nil && len(d) == 2 && d[0].Online })
	exe, _ := os.Executable()
	job := task("upload_restart", "process.exec", map[string]any{"program": exe, "args": []string{"--phase2-workload", "artifact-large"}, "cwd": "workspace/project"})
	job.OutputArtifacts = []protocol.OutputArtifact{{Path: "large.bin", Name: "large", MediaType: "application/octet-stream"}}
	job.WorkspaceContext = &protocol.WorkspaceContext{RootDir: "workspace/project", ExpectedGitHead: e.head, Policy: "require_clean_or_match"}
	p := protocol.PlanEnvelope{PlanID: "output_restart", IdempotencyKey: "output_restart", Goal: "resume output", ConcurrencyLimit: 1, CreatedAt: time.Now().UTC(), Tasks: []protocol.TaskEnvelope{job}}
	if _, err = e.c.Submit(e.ctx, p); err != nil {
		t.Fatal(err)
	}
	select {
	case <-blocked:
	case <-e.ctx.Done():
		t.Fatal("second output chunk did not reach TLS server")
	}
	first.Close()
	sum := sha256.Sum256([]byte(job.TaskID))
	run := filepath.Join(filepath.Dir(cfg.DBPath), "runs", hex.EncodeToString(sum[:]))
	if _, err = os.Stat(filepath.Join(run, "workspace", "large.bin")); err != nil {
		t.Fatal("output checkout removed before upload complete", err)
	}
	restored, err := agent.Open(e.cfgA)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	go restored.Run(e.ctx)
	var snap protocol.PlanSnapshot
	await(t, e.ctx, func() bool {
		snap, err = e.c.Plan(e.ctx, p.PlanID)
		return err == nil && protocol.Terminal(snap.Status)
	})
	if snap.Status != "succeeded" || len(snap.Tasks[0].Artifacts) != 1 {
		t.Fatalf("resumed output %s", input(snap))
	}
	if n, err := restored.ExecutionCount(job.TaskID); err != nil || n != 1 {
		t.Fatalf("producer ran %d times: %v", n, err)
	}
	await(t, e.ctx, func() bool { _, err := os.Stat(filepath.Join(run, "workspace")); return os.IsNotExist(err) })
	ref := snap.Tasks[0].Artifacts[0]
	status, b, _ := artifactRequest(t, e, "GET", ref.DownloadURI, "c", nil)
	h := sha256.Sum256(b)
	if status != 200 || int64(len(b)) != ref.SizeBytes || hex.EncodeToString(h[:]) != ref.SHA256 {
		t.Fatal("resumed output differs")
	}
}
func TestArtifactUploadCapabilityAndAssignedWorkerPermissions(t *testing.T) {
	e := newPhase2(t)
	job := task("standalone_upload", "artifact.upload", map[string]any{"workspace": "workspace/project", "path": "README.md", "name": "readme", "media_type": "text/plain"})
	snap := e.run(t, job)
	if snap.Status != "succeeded" || len(snap.Tasks[0].Artifacts) != 1 {
		t.Fatalf("artifact.upload %s", input(snap))
	}
	ref := snap.Tasks[0].Artifacts[0]
	meta := input(protocol.ArtifactUploadRequest{PlanID: snap.Plan.PlanID, TaskID: job.TaskID, Name: ref.Name, MediaType: ref.MediaType, SizeBytes: ref.SizeBytes, SHA256: ref.SHA256})
	status, b, _ := artifactRequest(t, e, "POST", "/v1/artifacts/uploads", "b-secret", bytes.NewReader(meta))
	if status != 403 {
		t.Fatalf("other worker upload %d %s", status, b)
	}
	status, b, _ = artifactRequest(t, e, "POST", "/v1/artifacts/uploads", "a-secret", bytes.NewReader(meta))
	if status != 200 {
		t.Fatalf("assigned worker resume %d %s", status, b)
	}
}
func TestArtifactInvalidPatchFailsPreflightWithoutHostMutation(t *testing.T) {
	e := newPhase2(t)
	ref := uploadFixture(t, e, []byte("not a valid git patch\n"), "invalid_patch_stage", "patch")
	exe, _ := os.Executable()
	job := task("invalid_patch", "process.exec", map[string]any{"program": exe, "args": []string{"--phase2-workload", "marker"}, "cwd": "workspace/project"})
	job.WorkspaceContext = &protocol.WorkspaceContext{RootDir: "workspace/project", ExpectedGitHead: e.head, PatchArtifactID: ref.ArtifactID, Policy: "require_clean_or_match"}
	snap := e.run(t, job)
	v := snap.Tasks[0]
	if v.Status != "failed" || v.Error == nil || v.Error.Code != "WORKSPACE_MISMATCH" {
		t.Fatalf("invalid patch %s", input(v))
	}
	page, err := e.c.Events(e.ctx, 0, 0, 500)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range page.Events {
		if ev.TaskID == job.TaskID && ev.Type == "task.started" {
			t.Fatal("invalid patch started command")
		}
	}
	b, err := os.ReadFile(filepath.Join(e.root, "README.md"))
	if err != nil || string(b) != "fixture" {
		t.Fatal("invalid patch changed host")
	}
	if _, err = os.Stat(filepath.Join(e.root, "must-not-exist")); !os.IsNotExist(err) {
		t.Fatal("preflight executed command")
	}
}
