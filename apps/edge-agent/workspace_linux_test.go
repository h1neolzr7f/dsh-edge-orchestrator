package agent

import (
	"context"
	"encoding/json"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestFilesystemReadRejectsFIFOWithoutBlocking(t *testing.T) {
	root := t.TempDir()
	pipe := filepath.Join(root, "pipe")
	if e := syscall.Mkfifo(pipe, 0600); e != nil {
		t.Fatal(e)
	}
	a, e := Open(Config{DBPath: filepath.Join(t.TempDir(), "a.db"), DeviceID: "w", Token: "s", CoordinatorURL: "https://localhost", Workspaces: map[string]string{"workspace/project": root}})
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	done := make(chan error, 1)
	go func() {
		_, e := a.execute(context.Background(), protocol.TaskEnvelope{Capability: protocol.CapabilityRef{ID: "filesystem.read"}, Input: json.RawMessage(`{"workspace":"workspace/project","path":"pipe"}`)})
		done <- e
	}()
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("FIFO accepted as regular file")
		}
	case <-time.After(100 * time.Millisecond):
		fd, _ := syscall.Open(pipe, syscall.O_WRONLY|syscall.O_NONBLOCK, 0600)
		if fd >= 0 {
			syscall.Close(fd)
		}
		select {
		case <-done:
		case <-time.After(time.Second):
		}
		t.Fatal("filesystem.read blocked opening FIFO")
	}
}
