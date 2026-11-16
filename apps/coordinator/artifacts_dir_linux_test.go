package coordinator

import (
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestArtifactDirectoryRetryRepeatsFailedParentSync(t *testing.T) {
	path := filepath.Join(t.TempDir(), "artifacts")
	parent := filepath.Dir(path)
	calls := 0
	injected := errors.New("parent sync failed")
	sync := func(dir string) error {
		if dir == parent {
			calls++
			return injected
		}
		return syncArtifactDir(dir)
	}
	if e := durableArtifactDirWithSync(path, sync); !errors.Is(e, injected) {
		t.Fatalf("initial sync=%v", e)
	}
	if _, e := os.Stat(path); e != nil {
		t.Fatal(e)
	}
	if e := durableArtifactDirWithSync(path, sync); !errors.Is(e, injected) {
		t.Fatalf("retry skipped parent durability barrier: %v", e)
	}
	if calls != 2 {
		t.Fatalf("parent sync calls=%d", calls)
	}
}

func TestArtifactDirectoryConcurrentCreateCannotSkipStalledParentSync(t *testing.T) {
	path := filepath.Join(t.TempDir(), "artifacts")
	parent := filepath.Dir(path)
	entered := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan error, 1)
	var calls atomic.Int32
	sync := func(dir string) error {
		if dir == parent {
			if calls.Add(1) == 1 {
				close(entered)
				<-release
				return errors.New("first sync failed")
			}
			return errors.New("second sync failed")
		}
		return syncArtifactDir(dir)
	}
	go func() { firstDone <- durableArtifactDirWithSync(path, sync) }()
	<-entered
	// The directory is visible, but the first parent's publication barrier has
	// not succeeded. A second upload must establish its own barrier or fail.
	second := durableArtifactDirWithSync(path, sync)
	close(release)
	first := <-firstDone
	if first == nil || second == nil {
		t.Fatalf("concurrent create bypassed parent sync: first=%v second=%v", first, second)
	}
}
