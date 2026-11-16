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
func durableArtifactDir(path string) error {
	if st, e := os.Stat(path); e == nil {
		if !st.IsDir() {
			return &os.PathError{Op: "mkdir", Path: path, Err: os.ErrExist}
		}
		return nil
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	parent := filepath.Dir(path)
	if e := durableArtifactDir(parent); e != nil {
		return e
	}
	if e := os.Mkdir(path, 0700); e != nil && !errors.Is(e, os.ErrExist) {
		return e
	}
	return syncArtifactDir(parent)
}

func durableArtifactMove(src, dst string) error {
	if e := os.Rename(src, dst); e != nil {
		return e
	}
	return syncArtifactDir(filepath.Dir(dst))
}
