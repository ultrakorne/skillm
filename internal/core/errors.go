package core

import (
	"errors"
	"strings"
)

// Typed errors core returns where the terminal front-end used to prompt. cmd
// decides whether to ask the user and retry (with Options.Force or
// Options.Yes set); a non-interactive caller reports them as they are.

// ForeignFilesError means an operation would overwrite entries skillm did not
// create. Nothing has been written when it is returned; retrying with
// Options.Force overwrites them.
type ForeignFilesError struct {
	Paths []string
}

func (e *ForeignFilesError) Error() string {
	return "refusing to overwrite files skillm did not create: " + strings.Join(e.Paths, ", ")
}

// ErrNeedsConfirm means an operation needs the caller's confirmation before
// it changes anything; retrying with Options.Yes (or Options.Force) confirms.
var ErrNeedsConfirm = errors.New("confirmation required")
