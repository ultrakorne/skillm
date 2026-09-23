package core

import (
	"errors"
	"fmt"
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

// SourceCollisionError means skill ID is already installed from a different
// Source, so installing this one under the same id would replace an unrelated
// skill. Installing it under another id (InstallRequest.As) resolves it.
type SourceCollisionError struct {
	ID string
}

func (e *SourceCollisionError) Error() string {
	return fmt.Sprintf("skill %q is already installed from a different source", e.ID)
}

// ErrAsMultiple means an As override (which renames one skill) was given for a
// selection of more than one skill.
var ErrAsMultiple = errors.New("an id override renames a single skill but more than one skill was selected")

// LocalScopeAliasedError means a Local install at Base would land in the
// enabled agents' global skill folders (Base is the user's home directory), so
// there is no real local scope there.
type LocalScopeAliasedError struct {
	Base string
}

func (e *LocalScopeAliasedError) Error() string {
	return fmt.Sprintf("local scope resolves to the global skill folder here (%s)", e.Base)
}
