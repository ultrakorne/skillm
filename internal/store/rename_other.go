//go:build !windows

package store

import "os"

// renameReplace renames oldpath over newpath.
func renameReplace(oldpath, newpath string) error {
	return os.Rename(oldpath, newpath)
}
