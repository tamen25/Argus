// Package atomicfile writes files so a reader never sees half of one.
package atomicfile

import (
	"os"
	"path/filepath"
)

// Write writes data to path the way os.WriteFile does, but atomically: the
// data goes to a temporary file in the same directory, which then replaces
// path in one rename. A reader (the leaderboard loading a directory of bench
// reports, a matrix run deciding which cells are done) sees either the old
// file or the whole new one. A write that fails or is interrupted leaves
// path as it was; the temporary file is removed.
func Write(path string, data []byte, perm os.FileMode) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		return err
	}
	// Flushed to disk before the rename, so a crash cannot leave path
	// pointing at a file whose contents never made it.
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Chmod(tmp.Name(), perm); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
