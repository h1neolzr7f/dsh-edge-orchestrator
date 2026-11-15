package agent

import (
	"encoding/json"
	"fmt"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	"os"
	"path/filepath"
	"time"
)

type processID struct {
	PID         int    `json:"pid"`
	StartTimeNS int64  `json:"process_start_time_ns"`
	BootID      string `json:"os_boot_id"`
}
type runnerSpec struct {
	TaskID, Fingerprint, Nonce, Program, CWD, JobName, Dir string
	Args, Env                                              []string
	Deadline                                               time.Time
	GraceMS                                                int64
}
type runnerReady struct {
	Identity processID
	Nonce    string
}
type runnerExit struct {
	Identity              processID
	Nonce                 string
	ExitCode              int
	ErrorCode, Message    string
	StartedAt, FinishedAt time.Time
}

// The same executable owns its detached runner mode, including when embedded in
// integration-test binaries; no fourth binary or language runtime is required.
func init() {
	if len(os.Args) == 3 && os.Args[1] == "--dsh-internal-runner" {
		if e := runRunner(os.Args[2]); e != nil {
			fmt.Fprintln(os.Stderr, e)
			var spec runnerSpec
			if readJSON(os.Args[2], &spec) == nil {
				id, _ := processIdentity(os.Getpid())
				_ = atomicJSON(filepath.Join(spec.Dir, "exit.json"), runnerExit{Identity: id, Nonce: spec.Nonce, ExitCode: -1, ErrorCode: "PROCESS_START_FAILED", Message: e.Error(), FinishedAt: nowUTC()})
			}
			os.Exit(1)
		}
		os.Exit(0)
	}
}
func atomicJSON(path string, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".state-*")
	if e != nil {
		return e
	}
	temp := f.Name()
	defer os.Remove(temp)
	if _, e = f.Write(b); e == nil {
		e = f.Sync()
	}
	if ce := f.Close(); e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	return os.Rename(temp, path)
}
func readJSON(path string, v any) error {
	b, e := os.ReadFile(path)
	if e != nil {
		return e
	}
	return protocol.Decode(b, v)
}
func exists(path string) bool { _, e := os.Stat(path); return e == nil }
func runRunner(path string) error {
	var spec runnerSpec
	if e := readJSON(path, &spec); e != nil {
		return e
	}
	claim, e := os.OpenFile(filepath.Join(spec.Dir, "runner.claim"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	claim.Close()
	if e = becomeReaper(); e != nil {
		return e
	}
	stopSignals := runnerSignals()
	defer stopSignals()
	id, e := processIdentity(os.Getpid())
	if e != nil {
		return e
	}
	ready := runnerReady{Identity: id, Nonce: spec.Nonce}
	if e = atomicJSON(filepath.Join(spec.Dir, "ready.json"), ready); e != nil {
		return e
	}
	result := runnerExit{Identity: id, Nonce: spec.Nonce, ExitCode: -1}
	exitPath := filepath.Join(spec.Dir, "exit.json")
	writeExit := func() error { result.FinishedAt = nowUTC(); return atomicJSON(exitPath, result) }
	// The Agent persists identity before releasing the user's command. The runner
	// survives Agent loss and can be resumed from the same durable handshake.
	startupDeadline := time.Now().Add(30 * time.Second)
	for !exists(filepath.Join(spec.Dir, "go")) {
		if exists(filepath.Join(spec.Dir, "cancel")) {
			result.ErrorCode = "TASK_CANCELLED"
			return writeExit()
		}
		if time.Now().After(startupDeadline) || !spec.Deadline.IsZero() && !time.Now().Before(spec.Deadline) {
			result.ErrorCode = "DEADLINE_EXCEEDED"
			return writeExit()
		}
		time.Sleep(10 * time.Millisecond)
	}
	if exists(filepath.Join(spec.Dir, "cancel")) {
		result.ErrorCode = "TASK_CANCELLED"
		return writeExit()
	}
	out, e := os.OpenFile(filepath.Join(spec.Dir, "stdout.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if e != nil {
		return e
	}
	defer out.Close()
	stderr, e := os.OpenFile(filepath.Join(spec.Dir, "stderr.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if e != nil {
		return e
	}
	defer stderr.Close()
	result.StartedAt = nowUTC()
	cmd, cleanup, e := startManaged(spec, out, stderr)
	if e != nil {
		result.ErrorCode = "PROCESS_START_FAILED"
		result.Message = e.Error()
		return writeExit()
	}
	defer cleanup()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	finished := false
	var waitErr error
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case waitErr = <-done:
			finished = true
			done = nil
		case <-ticker.C:
		}
		cancelled := exists(filepath.Join(spec.Dir, "cancel"))
		expired := !spec.Deadline.IsZero() && !time.Now().Before(spec.Deadline)
		if cancelled || expired {
			result.ErrorCode = "DEADLINE_EXCEEDED"
			if cancelled {
				result.ErrorCode = "TASK_CANCELLED"
			}
			if e = writeExit(); e != nil {
				return e
			}
			if e = terminateOwned(id, spec.JobName, time.Duration(spec.GraceMS)*time.Millisecond); e != nil {
				return e
			}
			if !finished && done != nil {
				select {
				case waitErr = <-done:
					finished = true
				case <-time.After(time.Second):
				}
			}
			reapDescendants()
			return writeExit()
		}
		if finished && !treeBusy(id, spec.JobName) {
			reapDescendants()
			result.ExitCode = cmd.ProcessState.ExitCode()
			if waitErr != nil {
				result.ErrorCode = "PROCESS_EXIT_NONZERO"
				result.Message = waitErr.Error()
			}
			return writeExit()
		}
	}
}
