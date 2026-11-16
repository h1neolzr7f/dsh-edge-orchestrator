package coordinator

import (
	"errors"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
)

// MoveFileExW WRITE_THROUGH is the documented native namespace publication
// operation. No COPY_ALLOWED: staging and ready names must remain on one volume.
// https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-movefileexw
// Microsoft describes completion on disk, with an explicit flush guarantee for
// copy/delete moves. This is not a proof of ancestor durability under arbitrary
// power loss, filter drivers, or hardware caches. Native recovery tests are
// still required; errors from file flushing and native moves are never ignored.
func artifactNativeMove(src, dst string) error {
	from, e := windows.UTF16PtrFromString(src)
	if e != nil {
		return e
	}
	to, e := windows.UTF16PtrFromString(dst)
	if e != nil {
		return e
	}
	return windows.MoveFileEx(from, to, windows.MOVEFILE_WRITE_THROUGH)
}
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
	temp, e := os.MkdirTemp(parent, ".artifact-dir-")
	if e != nil {
		return e
	}
	defer os.Remove(temp)
	e = artifactNativeMove(temp, path)
	if errors.Is(e, windows.ERROR_ALREADY_EXISTS) || errors.Is(e, windows.ERROR_FILE_EXISTS) {
		if st, se := os.Stat(path); se == nil && st.IsDir() {
			return nil
		}
	}
	return e
}
func durableArtifactMove(src, dst string) error {
	f, e := os.OpenFile(src, os.O_RDWR, 0600)
	if e != nil {
		return e
	}
	e = f.Sync()
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	return artifactNativeMove(src, dst)
}
