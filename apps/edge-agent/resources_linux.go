package agent

import (
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

func hostResources(dir string) protocol.Resources {
	r := protocol.Resources{CPUThreads: runtime.NumCPU()}
	if b, e := os.ReadFile("/proc/meminfo"); e == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(line, "MemTotal:") {
				fields := strings.Fields(line)
				if len(fields) > 1 {
					n, e := strconv.ParseUint(fields[1], 10, 64)
					if e == nil {
						r.MemoryBytes = n * 1024
					}
				}
			}
		}
	}
	var stat syscall.Statfs_t
	if syscall.Statfs(dir, &stat) == nil {
		r.DiskFreeBytes = stat.Bavail * uint64(stat.Bsize)
	}
	return r
}

func hostVersion() string {
	b, e := os.ReadFile("/proc/sys/kernel/osrelease")
	if e != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(b))
}
