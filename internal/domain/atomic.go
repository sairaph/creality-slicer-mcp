package domain

import (
	"fmt"
	"os"
	"path/filepath"
)

// WriteFileAtomic writes data to path by creating a temporary file in the
// same directory, syncing it, then atomically replacing whatever was at path.
// A reader never observes a partially written file, and a crash mid-write
// leaves the previous file (or none) rather than a truncated one. The parent
// directory is created with mode 0700 when missing. The Windows replacement
// uses MoveFileEx (atomic_windows.go), which a plain rename cannot do over an
// existing file.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create directory %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	name := tmp.Name()
	defer os.Remove(name)

	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return fmt.Errorf("set permissions on temporary file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temporary file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temporary file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temporary file: %w", err)
	}
	if err := replaceFile(name, path); err != nil {
		return fmt.Errorf("publish %s: %w", path, err)
	}
	if dirHandle, err := os.Open(dir); err == nil {
		_ = dirHandle.Sync()
		_ = dirHandle.Close()
	}
	return nil
}

// ReplaceFile moves source over destination the way WriteFileAtomic does,
// with the same retries on Windows when another program holds a file for a
// moment. It returns the last error when the replacement cannot be done.
func ReplaceFile(source, destination string) error { return replaceFile(source, destination) }
