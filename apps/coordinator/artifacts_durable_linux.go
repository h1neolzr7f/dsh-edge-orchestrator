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

// Publish the owned artifact directory name in the pre-established DB directory.
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
	// The DB directory already exists before artifact storage is used. Only
	// this owned child name needs a publication barrier: unrelated ancestors
	// may permit traversal without granting read/list access for fsync.
	// Repeat the immediate-parent barrier even for an existing name, which may
	// be visible from a concurrent mkdir or a previous failed parent fsync.
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
