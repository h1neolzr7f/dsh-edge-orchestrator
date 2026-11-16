package coordinator

import (
	"errors"
	"os"
	"path/filepath"
)

func syncArtifactDir(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}

// Persist new ancestor names as well as the artifact directory itself.
func durableArtifactDir(path string) error { return durableArtifactDirWithSync(path, syncArtifactDir) }
func durableArtifactDirWithSync(path string, syncDir func(string) error) error {
	exists := false
	if st, e := os.Stat(path); e == nil {
		if !st.IsDir() {
			return &os.PathError{Op: "mkdir", Path: path, Err: os.ErrExist}
		}
		exists = true
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	parent := filepath.Dir(path)
	if parent == path {
		return nil
	} // Filesystem root has no parent name to publish.
	// Existing names may come from a concurrent mkdir or a prior failed fsync.
	// Re-establish every ancestor barrier instead of treating visibility as durability.
	if e := durableArtifactDirWithSync(parent, syncDir); e != nil {
		return e
	}
	if !exists {
		if e := os.Mkdir(path, 0700); e != nil && !errors.Is(e, os.ErrExist) {
			return e
		}
	}
	return syncDir(parent)
}

func durableArtifactMove(src, dst string) error {
	if e := os.Rename(src, dst); e != nil {
		return e
	}
	return syncArtifactDir(filepath.Dir(dst))
}
