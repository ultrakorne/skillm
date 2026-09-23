package protocol

import (
	"context"
	"errors"

	"github.com/ultrakorne/skillm/internal/core"
	"github.com/ultrakorne/skillm/internal/store"
)

// Error codes. They are part of the contract: a GUI switches on them, so a
// code is never renamed or reused for another meaning.
const (
	// CodeError is any failure without a more specific code.
	CodeError = "error"
	// CodeUsage: the command line was invalid (unknown flag or command,
	// wrong arguments, --events without --json).
	CodeUsage = "usage"
	// CodeJSONUnsupported: the command has no JSON mode (yet).
	CodeJSONUnsupported = "json_unsupported"
	// CodeGitMissing: the system git binary is not on PATH.
	CodeGitMissing = "git_missing"
	// CodeCancelled: the run was interrupted (SIGINT). Retryable.
	CodeCancelled = "cancelled"
	// CodeTimeout: the run hit a deadline. Retryable.
	CodeTimeout = "timeout"
	// CodeHomeLocked: another skillm process held Home's lock for the whole
	// wait. Path is the lock file. Retryable.
	CodeHomeLocked = "home_locked"
	// CodeForeignFiles: an install would overwrite entries skillm did not
	// create (Paths). Nothing was written; retry with --yes (canonical
	// slots only) or --force.
	CodeForeignFiles = "foreign_files"
	// CodeNeedsForce: an entry skillm did not create is in the way (SkillID
	// names the skill). Work before it may be done; --force steps past it.
	CodeNeedsForce = "needs_force"
	// CodeNeedsConfirm: the confirmation given is out of date. For an
	// uninstall, Paths is the new list of projects to confirm.
	CodeNeedsConfirm = "needs_confirm"
	// CodeUninstallFailed: removing part of skill SkillID's installs failed
	// with an I/O error (no force offer). The skills before it were removed.
	CodeUninstallFailed = "uninstall_failed"
	// CodeSourceCollision: SkillID is already installed from another
	// source; install it under another id.
	CodeSourceCollision = "source_collision"
	// CodeAsMultiple: an id override was given for more than one skill.
	CodeAsMultiple = "as_multiple"
	// CodeLocalScopeAliased: a Local install at Path would land in the
	// global skill folders (Path is the user's home directory).
	CodeLocalScopeAliased = "local_scope_aliased"
	// CodeCommitMismatch: the source moved since it was inspected;
	// re-inspect and ask again (the same command fails again unchanged).
	CodeCommitMismatch = "commit_mismatch"
	// CodeNotInstalled: a skill asked for is not installed (SkillID when
	// there is one).
	CodeNotInstalled = "not_installed"
	// CodeUpdateFailed: at least one skill failed to update; the others
	// were updated. SkillID names the skill when only one failed; every
	// failed skill is also a warning (code update_failed or update_skipped)
	// with its skill_id.
	CodeUpdateFailed = "update_failed"
	// CodeNoAgentEnabled: the change would leave no agent enabled.
	CodeNoAgentEnabled = "no_agent_enabled"
	// CodeUnknownAgent: an agent name is not defined in config.toml.
	CodeUnknownAgent = "unknown_agent"
	// CodeManagedByApp: this skillm is inside the app bundle; the app
	// upgrades it. Not a failure a GUI should show as one.
	CodeManagedByApp = "managed_by_app"
	// CodeSourceBuild: a source (dev) build has no release to upgrade to.
	CodeSourceBuild = "source_build"
	// CodeNoUpgrade: skillm is already the latest release.
	CodeNoUpgrade = "no_upgrade"
)

// ErrorFrom maps err to its protocol Error: an *Error is returned as is, a
// known core or store error gets its code and fields, and anything else is
// CodeError with err's text. A nil err gives nil.
func ErrorFrom(err error) *Error {
	if err == nil {
		return nil
	}
	out := &Error{Code: CodeError, Message: err.Error()}

	var (
		pe        *Error
		lockErr   *store.LockTimeoutError
		foreign   *core.ForeignFilesError
		scope     *core.UninstallScopeChangedError
		blocked   *core.UninstallBlockedError
		collision *core.SourceCollisionError
		aliased   *core.LocalScopeAliasedError
		mismatch  *core.CommitMismatchError
		unknown   *core.UnknownSkillError
		notInst   *core.NotInstalledError
		updFailed *core.UpdateFailedError
		badAgent  *core.UnknownAgentError
		dirErr    *core.ProjectDirError
	)
	switch {
	case errors.As(err, &pe):
		return pe
	case errors.Is(err, context.Canceled):
		out.Code, out.Retryable = CodeCancelled, true
	case errors.Is(err, context.DeadlineExceeded):
		out.Code, out.Retryable = CodeTimeout, true
	case errors.As(err, &lockErr):
		out.Code, out.Path, out.Retryable = CodeHomeLocked, lockErr.Path, true
	case errors.As(err, &foreign):
		out.Code, out.Paths = CodeForeignFiles, foreign.Paths
	case errors.As(err, &scope):
		out.Code, out.Paths = CodeNeedsConfirm, scope.Roots
	case errors.As(err, &blocked):
		out.Code, out.SkillID = CodeUninstallFailed, blocked.ID
		if errors.Is(err, core.ErrNeedsForce) {
			out.Code = CodeNeedsForce
		}
	case errors.Is(err, core.ErrNeedsForce):
		out.Code = CodeNeedsForce
	case errors.Is(err, core.ErrNeedsConfirm):
		out.Code = CodeNeedsConfirm
	case errors.As(err, &collision):
		out.Code, out.SkillID = CodeSourceCollision, collision.ID
	case errors.Is(err, core.ErrAsMultiple):
		out.Code = CodeAsMultiple
	case errors.As(err, &aliased):
		out.Code, out.Path = CodeLocalScopeAliased, aliased.Base
	case errors.As(err, &mismatch):
		out.Code = CodeCommitMismatch
	case errors.As(err, &unknown):
		out.Code, out.SkillID = CodeNotInstalled, unknown.ID
	case errors.As(err, &notInst):
		out.Code = CodeNotInstalled
		if len(notInst.IDs) == 1 {
			out.SkillID = notInst.IDs[0]
		}
	case errors.As(err, &updFailed):
		out.Code = CodeUpdateFailed
		if len(updFailed.IDs) == 1 {
			out.SkillID = updFailed.IDs[0]
		}
	case errors.Is(err, core.ErrNoAgentEnabled):
		out.Code = CodeNoAgentEnabled
	case errors.As(err, &badAgent):
		out.Code = CodeUnknownAgent
	case errors.Is(err, core.ErrManagedByApp):
		out.Code = CodeManagedByApp
	case errors.Is(err, core.ErrSourceBuild):
		out.Code = CodeSourceBuild
	case errors.Is(err, core.ErrNoUpgrade):
		out.Code = CodeNoUpgrade
	case errors.As(err, &dirErr):
		out.Path = dirErr.Path
	}
	return out
}
