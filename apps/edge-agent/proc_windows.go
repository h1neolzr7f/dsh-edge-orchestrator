package agent

import (
	"encoding/hex"
	"fmt"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	"golang.org/x/sys/windows"
	"os"
	"os/exec"
	"syscall"
	"time"
	"unsafe"
)

func configureRunner(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW}
}
func bootID() (string, error) {
	// NtQuerySystemInformation requires an eight-byte-aligned buffer. A byte
	// array has alignment one, which can trigger STATUS_DATATYPE_MISALIGNMENT
	// when race instrumentation changes its allocation layout.
	var aligned [8]uint64
	b := unsafe.Slice((*byte)(unsafe.Pointer(&aligned[0])), int(unsafe.Sizeof(aligned)))
	var n uint32
	e := windows.NtQuerySystemInformation(windows.SystemBootEnvironmentInformation, unsafe.Pointer(&b[0]), uint32(len(b)), &n)
	if e == nil {
		return "boot-guid:" + hex.EncodeToString(b[:16]), nil
	}
	e = windows.NtQuerySystemInformation(windows.SystemTimeOfDayInformation, unsafe.Pointer(&b[0]), uint32(len(b)), &n)
	if e != nil {
		return "", e
	}
	return "boot-time:" + hex.EncodeToString(b[:8]), nil
}
func processIdentity(pid int) (processID, error) {
	boot, e := bootID()
	if e != nil {
		return processID{}, e
	}
	h, e := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if e != nil {
		return processID{}, e
	}
	defer windows.CloseHandle(h)
	var code uint32
	if e = windows.GetExitCodeProcess(h, &code); e != nil || code != 259 {
		return processID{}, os.ErrProcessDone
	}
	var created, exited, kernel, user windows.Filetime
	if e = windows.GetProcessTimes(h, &created, &exited, &kernel, &user); e != nil {
		return processID{}, e
	}
	return processID{PID: pid, StartTimeNS: created.Nanoseconds(), BootID: boot}, nil
}
func sameProcess(id processID) bool {
	current, e := processIdentity(id.PID)
	return e == nil && current == id
}
func openJob(name string) (windows.Handle, error) {
	p, e := windows.UTF16PtrFromString(name)
	if e != nil {
		return 0, e
	}
	h, _, e := windows.NewLazySystemDLL("kernel32.dll").NewProc("OpenJobObjectW").Call(0x0004|0x0008, 0, uintptr(unsafe.Pointer(p)))
	if h == 0 {
		return 0, e
	}
	return windows.Handle(h), nil
}
func treeBusy(id processID, job string) bool {
	h, e := openJob(job)
	if e != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var account struct {
		Times                                                            [4]int64
		PageFaults, TotalProcesses, ActiveProcesses, TerminatedProcesses uint32
	}
	e = windows.QueryInformationJobObject(h, windows.JobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(&account)), uint32(unsafe.Sizeof(account)), nil)
	return e != nil || account.ActiveProcesses > 0
}
func terminateOwned(id processID, job string, grace time.Duration) error {
	if !sameProcess(id) {
		if !treeBusy(id, job) {
			return nil
		}
		return fmt.Errorf("process identity no longer matches")
	}
	h, e := openJob(job)
	if e != nil {
		return nil
	}
	defer windows.CloseHandle(h)
	return windows.TerminateJobObject(h, 1)
}
func runnerSignals() func() { return func() {} }
func startManaged(spec runnerSpec, out, stderr *os.File) (*exec.Cmd, func(), error) {
	name, e := windows.UTF16PtrFromString(spec.JobName)
	if e != nil {
		return nil, nil, e
	}
	job, e := windows.CreateJobObject(nil, name)
	if e != nil {
		return nil, nil, e
	}
	cleanup := func() { windows.CloseHandle(job) }
	var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, e = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); e != nil {
		cleanup()
		return nil, nil, e
	}
	cmd := exec.Command(spec.Program, spec.Args...)
	cmd.Dir = spec.CWD
	cmd.Env = spec.Env
	cmd.Stdout = out
	cmd.Stderr = stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_SUSPENDED | windows.CREATE_NO_WINDOW}
	if e = cmd.Start(); e != nil {
		cleanup()
		return nil, nil, e
	}
	fail := func(err error) (*exec.Cmd, func(), error) {
		cmd.Process.Kill()
		cmd.Wait()
		cleanup()
		return nil, nil, err
	}
	process, e := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if e != nil {
		return fail(e)
	}
	e = windows.AssignProcessToJobObject(job, process)
	windows.CloseHandle(process)
	if e != nil {
		return fail(e)
	}
	snapshot, e := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if e != nil {
		return fail(e)
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	for e = windows.Thread32First(snapshot, &entry); e == nil; e = windows.Thread32Next(snapshot, &entry) {
		if entry.OwnerProcessID == uint32(cmd.Process.Pid) {
			thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
			if err != nil {
				return fail(err)
			}
			_, err = windows.ResumeThread(thread)
			windows.CloseHandle(thread)
			if err != nil {
				return fail(err)
			}
			return cmd, cleanup, nil
		}
	}
	return fail(fmt.Errorf("suspended primary thread not found"))
}

func becomeReaper() error { return nil }
func reapDescendants()    {}

func openReadRegular(path string) (*os.File, error) {
	info, e := os.Stat(path)
	if e != nil {
		return nil, e
	}
	if !info.Mode().IsRegular() {
		return nil, protocol.Err("FILE_NOT_REGULAR", "only regular files can be read")
	}
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	info, e = f.Stat()
	if e != nil || !info.Mode().IsRegular() {
		f.Close()
		if e != nil {
			return nil, e
		}
		return nil, protocol.Err("FILE_NOT_REGULAR", "only regular files can be read")
	}
	return f, nil
}
