//go:build windows

package store

import (
	"errors"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// renameReplace renames oldpath over newpath. On Windows the replace fails
// while another process has newpath open (a `skillm list` reading state.toml
// takes no lock), so it retries briefly, as cmd/go's robustio does.
func renameReplace(oldpath, newpath string) error {
	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		err := os.Rename(oldpath, newpath)
		if err == nil || time.Now().After(deadline) ||
			!(errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_SHARING_VIOLATION)) {
			return err
		}
		time.Sleep(10 * time.Millisecond)
	}
}
