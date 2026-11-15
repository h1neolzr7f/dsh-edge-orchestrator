package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var errAgentDetached = errors.New("agent detached from persistent process")

func (a *Agent) runProcess(ctx context.Context, t protocol.TaskEnvelope) (json.RawMessage, error) {
	dir := a.taskDir(t.TaskID)
	specPath := filepath.Join(dir, "spec.json")
	a.mu.Lock()
	resuming := a.resuming[t.TaskID]
	a.mu.Unlock()
	var spec runnerSpec
	if resuming {
		if e := readJSON(specPath, &spec); e != nil {
			return nil, e
		}
	} else {
		var in struct {
			Program   string   `json:"program"`
			Args      []string `json:"args"`
			CWD       string   `json:"cwd"`
			TimeoutMS int64    `json:"timeout_ms"`
		}
		if e := protocol.Decode(t.Input, &in); e != nil {
			return nil, e
		}
		if in.Args == nil || in.CWD == "" || in.Program == "" || in.TimeoutMS < 0 {
			return nil, protocol.Err("INVALID_PAYLOAD", "program, args, cwd and positive timeout required")
		}
		program, e := a.allowedProgram(in.Program)
		if e != nil {
			return nil, e
		}
		cwd, e := a.processDir(ctx, in.CWD)
		if e != nil {
			return nil, e
		}
		if in.TimeoutMS == 0 {
			in.TimeoutMS = 60000
		}
		if in.TimeoutMS > int64((365*24*time.Hour)/time.Millisecond) {
			return nil, protocol.Err("INVALID_PAYLOAD", "timeout is too large")
		}
		deadline := nowUTC().Add(time.Duration(in.TimeoutMS) * time.Millisecond)
		if t.Deadline != nil && t.Deadline.Before(deadline) {
			deadline = *t.Deadline
		}
		if !nowUTC().Before(deadline) || ctx.Err() != nil {
			return nil, protocol.Err("DEADLINE_EXCEEDED", "deadline passed before process start")
		}
		if e = os.MkdirAll(dir, 0700); e != nil {
			return nil, e
		}
		grace := a.cfg.TerminationGrace
		if grace <= 0 {
			grace = 500 * time.Millisecond
		}
		env := []string{}
		for _, v := range os.Environ() {
			if strings.HasPrefix(v, "DSH_WORKER_TOKEN=") || strings.HasPrefix(v, "DSH_WORKER_TOKENS=") || strings.HasPrefix(v, "DSH_CONTROLLER_TOKEN=") {
				continue
			}
			env = append(env, v)
		}
		nonce := protocol.NewID("run")
		spec = runnerSpec{TaskID: t.TaskID, Fingerprint: protocol.Fingerprint(t), Nonce: nonce, Program: program, Args: in.Args, CWD: cwd, Dir: dir, JobName: "Local\\dsh-edge-" + nonce, Deadline: deadline, GraceMS: grace.Milliseconds(), Env: env}
		if e = atomicJSON(specPath, spec); e != nil {
			return nil, e
		}
		exe, e := os.Executable()
		if e != nil {
			return nil, e
		}
		runner := exec.Command(exe, "--dsh-internal-runner", specPath)
		configureRunner(runner)
		sink, e := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
		if e != nil {
			return nil, e
		}
		runner.Stdout = sink
		runner.Stderr = sink
		if e = runner.Start(); e != nil {
			sink.Close()
			return nil, protocol.Err("PROCESS_START_FAILED", e.Error())
		}
		sink.Close()
		go func() { runner.Wait() }()

	}
	var ready runnerReady
	startupDeadline := time.Now().Add(5 * time.Second)
	for {
		if readJSON(filepath.Join(dir, "ready.json"), &ready) == nil {
			break
		}
		var early runnerExit
		if readJSON(filepath.Join(dir, "exit.json"), &early) == nil && early.Nonce == spec.Nonce {
			ready = runnerReady{Identity: early.Identity, Nonce: early.Nonce}
			break
		}
		if a.ctx.Err() != nil {
			return nil, errAgentDetached
		}
		if time.Now().After(startupDeadline) {
			return nil, protocol.Err("PROCESS_START_FAILED", "runner did not publish identity")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if ready.Nonce != spec.Nonce {
		return nil, protocol.Err("interrupted_by_restart", "runner nonce mismatch")
	}
	if e := a.saveProcess(t, ready, spec); e != nil {
		return nil, e
	}
	if !exists(filepath.Join(dir, "go")) && !exists(filepath.Join(dir, "exit.json")) {
		if !sameProcess(ready.Identity) {
			return nil, protocol.Err("PROCESS_START_FAILED", "runner exited before execution gate")
		}
		if e := os.WriteFile(filepath.Join(dir, "go"), []byte(spec.Nonce), 0600); e != nil {
			return nil, e
		}
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	terminating := false
	for {
		if a.ctx.Err() != nil {
			return nil, errAgentDetached
		}
		if e := a.tailLogs(t, dir); e != nil {
			os.WriteFile(filepath.Join(dir, "cancel"), []byte("cancel"), 0600)
			terminateOwned(ready.Identity, spec.JobName, time.Duration(spec.GraceMS)*time.Millisecond)
			return nil, e
		}
		var result runnerExit
		if readJSON(filepath.Join(dir, "exit.json"), &result) == nil {
			if result.Nonce != spec.Nonce || result.Identity != ready.Identity {
				return nil, protocol.Err("interrupted_by_restart", "exit status identity mismatch")
			}
			if !treeBusy(ready.Identity, spec.JobName) {
				if e := a.tailLogs(t, dir, true); e != nil {
					return nil, e
				}
				if !a.logsDrained(t, dir) {
					continue
				}
				b, e := a.processResult(spec, result)
				if e != nil {
					return nil, e
				}
				if e = a.processProgress(t); e != nil {
					return nil, e
				}
				return b, processError(result)
			}
		}
		if ctx.Err() != nil && !terminating {
			terminating = true
			if e := os.WriteFile(filepath.Join(dir, "cancel"), []byte("cancel"), 0600); e != nil {
				return nil, e
			}
			if e := terminateOwned(ready.Identity, spec.JobName, time.Duration(spec.GraceMS)*time.Millisecond); e != nil {
				return nil, protocol.Err("interrupted_by_restart", e.Error())
			}
			// A terminal state requires all logs and a stopped runner/payload.
			// If the runner was killed before writing exit.json, persist the
			// known cancellation result, then use the ordinary drain path.
			if !sameProcess(ready.Identity) && !treeBusy(ready.Identity, spec.JobName) && !exists(filepath.Join(dir, "exit.json")) {
				code := "TASK_CANCELLED"
				if errors.Is(ctx.Err(), context.DeadlineExceeded) {
					code = "DEADLINE_EXCEEDED"
				}
				result = runnerExit{Identity: ready.Identity, Nonce: spec.Nonce, ExitCode: -1, StartedAt: nowUTC(), FinishedAt: nowUTC(), ErrorCode: code}
				if e := atomicJSON(filepath.Join(dir, "exit.json"), result); e != nil {
					return nil, e
				}
			}

		}
		if !sameProcess(ready.Identity) && !exists(filepath.Join(dir, "exit.json")) {
			return nil, protocol.Err("interrupted_by_restart", fmt.Sprintf("runner identity %d no longer exists", ready.Identity.PID))
		}
		select {
		case <-ticker.C:
		case <-a.ctx.Done():
			return nil, errAgentDetached
		}
	}
}
