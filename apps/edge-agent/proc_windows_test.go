package agent

import (
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	"golang.org/x/sys/windows"
)

func TestWindowsOwnedRunnerHelper(t *testing.T) {
	if os.Getenv("DSH_TEST_IDLE_RUNNER") != "1" {
		return
	}
	time.Sleep(30 * time.Second)
}

func idleWindowsRunner(t *testing.T) processID {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestWindowsOwnedRunnerHelper$")
	cmd.Env = append(os.Environ(), "DSH_TEST_IDLE_RUNNER=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	id, err := processIdentity(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestWindowsTerminateOwnedReportsMissingLiveJob(t *testing.T) {
	id := idleWindowsRunner(t)
	if err := terminateOwned(id, "Local\\"+protocol.NewID("missing_job"), time.Millisecond); err == nil {
		t.Fatal("missing job silently accepted while runner lives")
	}
	if !sameProcess(id) {
		t.Fatal("missing job error unexpectedly killed runner")
	}
}

func TestWindowsTerminateOwnedKillsRunnerOutsideJob(t *testing.T) {
	id := idleWindowsRunner(t)
	name := "Local\\" + protocol.NewID("empty_job")
	ptr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		t.Fatal(err)
	}
	job, err := windows.CreateJobObject(nil, ptr)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(job)
	start := time.Now()
	if err := terminateOwned(id, name, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for sameProcess(id) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if sameProcess(id) {
		t.Fatal("runner outside payload job survived termination")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("termination grace was not capped: %s", elapsed)
	}
}
