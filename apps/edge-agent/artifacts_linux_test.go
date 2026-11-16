package agent

import (
	"context"
	"encoding/json"
	"github.com/h1neolzr7f/dsh-edge-orchestrator/packages/protocol"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStandaloneArtifactUploadRejectsFIFOWithoutBlocking(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "pipe")
	if e := unix.Mkfifo(path, 0600); e != nil {
		t.Fatal(e)
	}
	a, e := Open(Config{DBPath: filepath.Join(t.TempDir(), "a.db"), DeviceID: "w", Token: "secret", CoordinatorURL: "https://localhost", Workspaces: map[string]string{"workspace/project": root}})
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, e := a.executeArtifactUpload(ctx, protocol.TaskEnvelope{TaskID: "fifo", PlanID: "p", Input: json.RawMessage(`{"workspace":"workspace/project","path":"pipe","name":"pipe","media_type":"application/octet-stream"}`)})
		done <- e
	}()
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("FIFO uploaded")
		}
	case <-time.After(500 * time.Millisecond):
		// Release the baseline blocking open so a failing test does not leak a goroutine.
		f, e := os.OpenFile(path, os.O_RDWR|unix.O_NONBLOCK, 0600)
		if e == nil {
			f.Close()
		}
		<-done
		t.Fatal("standalone upload blocked opening FIFO")
	}
}

func TestConfinedArtifactOpenRejectsReplacedParent(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	os.Mkdir(filepath.Join(root, "out"), 0700)
	os.WriteFile(filepath.Join(root, "out", "file"), []byte("checked"), 0600)
	os.WriteFile(filepath.Join(outside, "file"), []byte("outside secret"), 0600)
	checked, e := confinedPath(root, "out/file")
	if e != nil {
		t.Fatal(e)
	}
	if e = os.Rename(filepath.Join(root, "out"), filepath.Join(root, "old")); e != nil {
		t.Fatal(e)
	}
	if e = os.Symlink(outside, filepath.Join(root, "out")); e != nil {
		t.Fatal(e)
	}
	f, e := openConfinedRegular(root, checked)
	if e == nil {
		f.Close()
		t.Fatal("replaced parent redirected confined upload outside workspace")
	}
}
