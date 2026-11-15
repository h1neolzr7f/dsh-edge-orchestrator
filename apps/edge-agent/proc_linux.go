package agent

import (
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	"golang.org/x/sys/unix"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func configureRunner(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} }
func bootID() (string, error) {
	b, e := os.ReadFile("/proc/sys/kernel/random/boot_id")
	return strings.TrimSpace(string(b)), e
}
func procStat(pid int) (state byte, group int, start int64, err error) {
	b, e := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if e != nil {
		err = e
		return
	}
	i := strings.LastIndex(string(b), ") ")
	if i < 0 {
		err = fmt.Errorf("invalid proc stat")
		return
	}
	fields := strings.Fields(string(b[i+2:]))
	if len(fields) < 20 {
		err = fmt.Errorf("short proc stat")
		return
	}
	state = fields[0][0]
	group, err = strconv.Atoi(fields[2])
	if err != nil {
		return
	}
	start, err = strconv.ParseInt(fields[19], 10, 64)
	return
}
func clockTicks() int64 {
	b, e := os.ReadFile("/proc/self/auxv")
	if e == nil {
		for len(b) >= 16 {
			tag := binary.NativeEndian.Uint64(b[:8])
			value := binary.NativeEndian.Uint64(b[8:16])
			if tag == 17 && value > 0 {
				return int64(value)
			}
			b = b[16:]
		}
	}
	return 100
}
func processIdentity(pid int) (processID, error) {
	boot, e := bootID()
	if e != nil {
		return processID{}, e
	}
	state, _, ticks, e := procStat(pid)
	if e != nil {
		return processID{}, e
	}
	if state == 'Z' {
		return processID{}, os.ErrProcessDone
	}
	return processID{PID: pid, StartTimeNS: ticks * (int64(time.Second) / clockTicks()), BootID: boot}, nil
}
func sameProcess(id processID) bool {
	current, e := processIdentity(id.PID)
	return e == nil && current == id
}
func treeBusy(id processID, job string) bool {
	entries, e := os.ReadDir("/proc")
	if e != nil {
		return true
	}
	for _, entry := range entries {
		pid, e := strconv.Atoi(entry.Name())
		if e != nil || pid == id.PID {
			continue
		}
		state, group, _, e := procStat(pid)
		if e == nil && group == id.PID && state != 'Z' {
			return true
		}
	}
	return false
}
func terminateOwned(id processID, job string, grace time.Duration) error {
	if !sameProcess(id) {
		if !treeBusy(id, job) {
			return nil
		}
		return fmt.Errorf("process identity no longer matches")
	}
	if e := syscall.Kill(-id.PID, syscall.SIGTERM); e != nil && !errors.Is(e, syscall.ESRCH) {
		return e
	}
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) {
		if !treeBusy(id, job) {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	if sameProcess(id) {
		if e := syscall.Kill(-id.PID, syscall.SIGKILL); e != nil && !errors.Is(e, syscall.ESRCH) {
			return e
		}
	}
	return nil
}
func runnerSignals() func() {
	ch := make(chan os.Signal, 8)
	signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT)
	return func() { signal.Stop(ch) }
}
func startManaged(spec runnerSpec, out, stderr *os.File) (*exec.Cmd, func(), error) {
	cmd := exec.Command(spec.Program, spec.Args...)
	cmd.Dir = spec.CWD
	cmd.Env = spec.Env
	cmd.Stdout = out
	cmd.Stderr = stderr
	e := cmd.Start()
	return cmd, func() {}, e
}

func becomeReaper() error { return unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0) }
func reapDescendants() {
	for {
		var status syscall.WaitStatus
		pid, e := syscall.Wait4(-1, &status, syscall.WNOHANG, nil)
		if e != nil || pid <= 0 {
			return
		}
	}
}

func openReadRegular(path string) (*os.File, error) {
	fd, e := unix.Open(path, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(fd), path)
	info, e := f.Stat()
	if e != nil {
		f.Close()
		return nil, e
	}
	if !info.Mode().IsRegular() {
		f.Close()
		return nil, protocol.Err("FILE_NOT_REGULAR", "only regular files can be read")
	}
	return f, nil
}
