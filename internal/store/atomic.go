package store

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// writeTemp writes data to the temp file f and flushes it to stable storage. It
// is a variable so tests can simulate a failure part-way through the write.
var writeTemp = func(f *os.File, data []byte) error {
	if _, err := f.Write(data); err != nil {
		return err
	}
	return f.Sync()
}

// WriteFileAtomic replaces the file at path with data so that a reader (or a
// crash) sees either the old content or the new content, never a partial
// file. It writes a temp file in the same directory, fsyncs it, sets perm, and
// renames it over path; on any failure the temp file is removed and path is
// left untouched. The parent directory must already exist.
func WriteFileAtomic(path string, data []byte, perm fs.FileMode) (err error) {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			f.Close()
			os.Remove(tmp)
		}
	}()

	if err := writeTemp(f, data); err != nil {
		return err
	}
	if err := f.Chmod(perm); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}

	// Persist the rename itself. Best-effort: not every platform can sync a
	// directory (Windows cannot), and the content is already durable.
	if d, derr := os.Open(dir); derr == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
}
