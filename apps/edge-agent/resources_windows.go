package agent

import (
	"fmt"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	"runtime"
	"syscall"
	"unsafe"
)

func hostResources(dir string) protocol.Resources {
	r := protocol.Resources{CPUThreads: runtime.NumCPU()}
	kernel := syscall.NewLazyDLL("kernel32.dll")
	var memory struct {
		Length, Load                                                                          uint32
		TotalPhys, AvailPhys, TotalPage, AvailPage, TotalVirtual, AvailVirtual, AvailExtended uint64
	}
	memory.Length = uint32(unsafe.Sizeof(memory))
	if ok, _, _ := kernel.NewProc("GlobalMemoryStatusEx").Call(uintptr(unsafe.Pointer(&memory))); ok != 0 {
		r.MemoryBytes = memory.TotalPhys
	}
	path, e := syscall.UTF16PtrFromString(dir)
	if e == nil {
		var available, total, free uint64
		if ok, _, _ := kernel.NewProc("GetDiskFreeSpaceExW").Call(uintptr(unsafe.Pointer(path)), uintptr(unsafe.Pointer(&available)), uintptr(unsafe.Pointer(&total)), uintptr(unsafe.Pointer(&free))); ok != 0 {
			r.DiskFreeBytes = available
		}
	}
	return r
}

func hostVersion() string {
	var v struct {
		Size, Major, Minor, Build, Platform uint32
		ServicePack                         [128]uint16
	}
	v.Size = uint32(unsafe.Sizeof(v))
	status, _, _ := syscall.NewLazyDLL("ntdll.dll").NewProc("RtlGetVersion").Call(uintptr(unsafe.Pointer(&v)))
	if status != 0 {
		return "unknown"
	}
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Build)
}
