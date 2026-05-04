// Package atomicio provides a single helper: WriteFile, which writes via a
// sibling temp file + fsync + rename so a crash mid-write can't leave a
// half-written destination on disk.
package atomicio

import (
	"os"
	"path/filepath"
)

// WriteFile writes data to path atomically and durably. The temp file lives
// in the same directory as path so os.Rename is a true atomic move on every
// supported filesystem; tmp.Sync ensures the bytes are on disk before the
// rename so a crash after rename doesn't roll back to an empty file.
func WriteFile(path string, data []byte, perm os.FileMode) (err error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// Remove the temp file on any error path. After a successful Rename the
	// path no longer exists, so this becomes a no-op.
	defer func() {
		if err != nil {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err = tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
