package store

import (
	"errors"
	"fmt"
	"io/fs"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
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
// file. It writes a temp file in the same directory, fsyncs it, and renames it
// over the destination; on any failure the temp file is removed and the
// destination is left untouched. The parent directory must already exist.
//
// Like os.WriteFile it writes through a symlink at path (a config.toml linked
// from a dotfiles repo stays a link) and keeps an existing file's mode; a new
// file gets perm filtered by the umask.
func WriteFileAtomic(path string, data []byte, perm fs.FileMode) (err error) {
	dst := path
	if resolved, rerr := filepath.EvalSymlinks(path); rerr == nil {
		dst = resolved
	} else if !errors.Is(rerr, fs.ErrNotExist) {
		return rerr
	}

	// An existing file keeps its mode exactly; a new one is created with perm
	// so the kernel applies the umask, as os.WriteFile would.
	createPerm := perm
	info, serr := os.Stat(dst)
	if serr == nil {
		createPerm = 0o600
	}

	f, err := createTemp(dst, createPerm)
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
	if serr == nil {
		if err := f.Chmod(info.Mode().Perm()); err != nil {
			return err
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := renameReplace(tmp, dst); err != nil {
		return fmt.Errorf("replace %s: %w", dst, err)
	}

	// Persist the rename itself. Best-effort: not every platform can sync a
	// directory (Windows cannot), and the content is already durable.
	if d, derr := os.Open(filepath.Dir(dst)); derr == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
}

// createTemp creates a new hidden temp file next to dst with the given mode
// (os.CreateTemp would force 0600).
func createTemp(dst string, perm fs.FileMode) (*os.File, error) {
	prefix := filepath.Join(filepath.Dir(dst), "."+filepath.Base(dst)+".tmp-")
	for range 100 {
		f, err := os.OpenFile(prefix+strconv.FormatUint(rand.Uint64(), 36), os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
		if !errors.Is(err, fs.ErrExist) {
			return f, err
		}
	}
	return nil, fmt.Errorf("create temp file for %s: too many collisions", dst)
}
