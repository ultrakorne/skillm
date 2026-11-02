package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/ultrakorne/skillm/internal/agentdir"
	"github.com/ultrakorne/skillm/internal/config"
	"github.com/ultrakorne/skillm/internal/linker"
	"github.com/ultrakorne/skillm/internal/state"
)

// Event codes Uninstall reports with, beyond the vendor primitives' own
// (unlinked, unlink_refused, unlink_failed, lockfile_not_updated).
const (
	// CodeCopyRemoved: a canonical copy of the skill was deleted.
	CodeCopyRemoved = "copy_removed"
	// CodeRemoveFailed: a canonical copy (or a legacy symlink in its slot)
	// could not be removed, and Options.Force stepped past it.
	CodeRemoveFailed = "remove_failed"
	// CodeUninstalled: the requested installs were removed.
	CodeUninstalled = "uninstalled"
	// CodeNotInstalled: a skill asked for is not installed (any more), and
	// UninstallRequest.SkipMissing skipped it.
	CodeNotInstalled = "not_installed"
)

// UninstalledSkill is what Uninstall did for one skill.
type UninstalledSkill struct {
	ID string
	// RemovedCopies lists the installs whose canonical copy was deleted:
	// "global" or a project root.
	RemovedCopies []string
	// Warnings are the removals that failed but did not stop the skill's
	// uninstall: the failures Options.Force stepped past (the entry in the
	// way is left in place), the Global agent links that could not be removed
	// (such an entry stays in place, as before; it never blocks), and the
	// skills-lock.json entries that could not be removed. Each was also
	// reported as a warning event.
	Warnings []error
}

// UninstallRequest is what Uninstall removes.
type UninstallRequest struct {
	// IDs are the skills to uninstall, in the order to report them.
	IDs []string
	// Scope limits removal to Global or to the Local install at Base. Nil
	// removes every install. Base must be absolute for Local.
	Scope *agentdir.Scope
	Base  string
	// CheckRoots holds the uninstall to the projects the caller's
	// confirmation named: when set, Uninstall refuses with an
	// *UninstallScopeChangedError, before removing anything, if the skills
	// now have Local installs (committed copies to delete) in a project not in
	// ConfirmedRoots. Unset, every recorded project is cleared, as for a run
	// that asked no question (--yes, --force, or off a terminal).
	CheckRoots bool
	// ConfirmedRoots are the project roots the confirmation named, as
	// UninstallRoots returned them.
	ConfirmedRoots []string
	// SkipMissing skips the ids that are not installed (any more) with a
	// warning instead of refusing the batch, so an uninstall that stopped
	// part-way can be retried with the same ids: the ones it already removed
	// are then skipped.
	SkipMissing bool
}

// Includes reports whether id has an install in the requested scope.
func (req UninstallRequest) Includes(opts Options, st *state.State, agents []agentdir.Agent, id string) (bool, error) {
	if _, ok := st.Get(id); !ok {
		return false, nil
	}
	if req.Scope == nil {
		return true, nil
	}
	if *req.Scope == agentdir.Global {
		if st.IsGlobal(id) {
			return true, nil
		}
		return managedLinksAt(opts.Home, id, agents, agentdir.Global, "")
	}
	if recordedProject(st, id, req.Base) {
		return true, nil
	}
	return managedLinksAt(opts.Home, id, agents, agentdir.Local, req.Base)
}

// managedLinksAt discovers legacy installs without canonical-copy markers.
// Inspection errors are propagated so callers never mistake an unreadable
// installation for an absent one.
func managedLinksAt(home, id string, agents []agentdir.Agent, scope agentdir.Scope, base string) (bool, error) {
	if scope == agentdir.Local {
		agents, _ = SplitLocalAliased(agents, base)
	}
	res, err := linker.ScanLinks(home, id, agents, scope, base)
	for _, a := range res.Agents {
		if a.Action == linker.ActionFound {
			return true, err
		}
	}
	return false, err
}

// Roots returns only the projects the requested uninstall would change.
func (req UninstallRequest) Roots(st *state.State) []string {
	if req.Scope == nil {
		return UninstallRoots(st, req.IDs)
	}
	if *req.Scope == agentdir.Local {
		for _, id := range req.IDs {
			if recordedProject(st, id, req.Base) {
				return []string{req.Base}
			}
		}
	}
	return nil
}

// UninstallResult is Uninstall's outcome: one entry per uninstalled skill, in
// the order they were asked for.
type UninstallResult struct {
	Skills []UninstalledSkill
}

// NotInstalledError means some skills asked for are not in the Registry.
// Nothing was removed.
type NotInstalledError struct {
	IDs []string
}

func (e *NotInstalledError) Error() string {
	return fmt.Sprintf("not installed: %s; nothing to uninstall", strings.Join(e.IDs, ", "))
}

// UninstallScopeChangedError means the skills to uninstall now have Local
// installs in projects the caller's confirmation did not name: another
// process installed or adopted one while the question was open. Nothing was
// removed. Roots is the full, sorted set of projects whose committed copies
// the uninstall would now delete; once that list is confirmed, retrying with
// it as UninstallRequest.ConfirmedRoots proceeds. It matches ErrNeedsConfirm.
type UninstallScopeChangedError struct {
	Roots []string
}

func (e *UninstallScopeChangedError) Error() string {
	return "the projects whose committed copies would be deleted changed since the confirmation: " +
		strings.Join(e.Roots, ", ")
}

// Unwrap lets errors.Is match ErrNeedsConfirm.
func (e *UninstallScopeChangedError) Unwrap() error { return ErrNeedsConfirm }

// UninstallBlockedError means removing part of skill ID's installs failed —
// a link path holds an entry skillm did not create, or a removal hit an I/O
// error — so its Registry entry was kept. The skills before ID were
// uninstalled and saved, and ID's removals up to the failure stay done (an
// uninstall is safe to repeat). A retry with Options.Force reports such
// failures as warnings and leaves the failed entries in place. It clears the
// selected install markers, retaining the Registry entry for other installs
// or an unreadable remaining location. An unscoped retry drops the entry.
// Only a foreign entry (Err matches linker.ErrNotManaged) makes
// it match ErrNeedsForce; an I/O failure is a plain error, which Force still
// steps past but which a caller should not offer "force" for.
type UninstallBlockedError struct {
	ID  string
	Err error
}

// Error is the underlying failure's text.
func (e *UninstallBlockedError) Error() string { return e.Err.Error() }

// Unwrap lets errors.Is match the underlying error (e.g.
// linker.ErrNotManaged) and, for a foreign entry, ErrNeedsForce.
func (e *UninstallBlockedError) Unwrap() []error {
	if errors.Is(e.Err, linker.ErrNotManaged) {
		return []error{ErrNeedsForce, e.Err}
	}
	return []error{e.Err}
}

// UninstallRoots returns the sorted, de-duplicated project roots where any of
// ids has a Local install in st: the projects whose committed copies an
// uninstall of ids deletes, which its confirmation names.
func UninstallRoots(st *state.State, ids []string) []string {
	var roots []string
	for _, id := range ids {
		for _, d := range st.VendoredRoots(id) {
			if !slices.Contains(roots, d) {
				roots = append(roots, d)
			}
		}
	}
	sort.Strings(roots)
	return roots
}

// CheckInstalled returns a *NotInstalledError naming every id in ids that is
// not in st's Registry, or nil when all are.
func CheckInstalled(st *state.State, ids []string) error {
	var missing []string
	for _, id := range ids {
		if _, ok := st.Get(id); !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		return &NotInstalledError{IDs: missing}
	}
	return nil
}

// Uninstall removes each skill's requested installs: all of them by default,
// or only req.Scope (with req.Base for Local). It sweeps every defined agent,
// disabled ones included, and drops the Registry entry only when no installs
// remain. The Registry is saved after each skill, so
// disk and Registry never drift if a later skill fails. Finally a tracked
// local root that no longer holds anything is forgotten.
//
// The caller holds Home's lock for the whole call and has already confirmed
// the removal with the user; Uninstall re-reads config and the Registry. Before
// anything is removed, an id that is not installed (any more) is a
// *NotInstalledError (with req.SkipMissing, a warning, and the id is
// skipped), and with req.CheckRoots a project the confirmation did not name
// is an *UninstallScopeChangedError. A removal failure is an
// *UninstallBlockedError unless opts.Force is set. A cancelled ctx stops the
// batch between skills with ctx's error; the skills already done are in the
// result and saved. Options.Cwd labels the report lines and is scanned for
// stray links as well.
func Uninstall(ctx context.Context, opts Options, rep Reporter, req UninstallRequest) (UninstallResult, error) {
	rep = nopIfNil(rep)
	var res UninstallResult
	if req.Scope != nil {
		if *req.Scope != agentdir.Global && *req.Scope != agentdir.Local {
			return res, fmt.Errorf("invalid uninstall scope %v", *req.Scope)
		}
		if *req.Scope == agentdir.Local {
			if !filepath.IsAbs(req.Base) {
				return res, fmt.Errorf("a local uninstall needs an absolute project directory, got %q", req.Base)
			}
			if filepath.Clean(agentdir.CanonicalLocalDir(req.Base)) == filepath.Clean(agentdir.CanonicalGlobalDir()) {
				return res, &LocalScopeAliasedError{Base: req.Base}
			}
			local, lerr := os.Stat(agentdir.CanonicalLocalDir(req.Base))
			global, gerr := os.Stat(agentdir.CanonicalGlobalDir())
			if lerr == nil && gerr == nil && os.SameFile(local, global) {
				return res, &LocalScopeAliasedError{Base: req.Base}
			}
		}
	}
	cfg, err := config.Load(opts.Home)
	if err != nil {
		return res, err
	}
	st, err := state.Load(opts.Home)
	if err != nil {
		return res, err
	}
	agents := cfg.AllAgents()
	ids := req.IDs
	if req.SkipMissing {
		present := make([]string, 0, len(ids))
		for _, id := range ids {
			included, err := req.Includes(opts, st, agents, id)
			if err != nil {
				return res, err
			}
			if !included {
				rep.Event(logEvent(LevelWarn, id, CodeNotInstalled, id+" is not installed in the requested scope; skipped"))
				continue
			}
			present = append(present, id)
		}
		ids = present
	} else {
		var missing []string
		for _, id := range ids {
			included, err := req.Includes(opts, st, agents, id)
			if err != nil {
				return res, err
			}
			if !included {
				missing = append(missing, id)
			}
		}
		if len(missing) > 0 {
			return res, &NotInstalledError{IDs: missing}
		}
	}
	req.IDs = ids
	if req.CheckRoots {
		roots := req.Roots(st)
		for _, r := range roots {
			if !slices.Contains(req.ConfirmedRoots, r) {
				return res, &UninstallScopeChangedError{Roots: roots}
			}
		}
	}

	// Whatever was removed (even when a later skill fails), the refresh
	// cache stops reporting it.
	defer func() {
		if len(res.Skills) > 0 {
			recordUninstalls(opts.Home, rep, st)
		}
	}()

	// Clear links for EVERY defined agent (not just the enabled ones): a link
	// made while an agent was enabled must not be left dangling just because it
	// is disabled now.
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		done, err := uninstallOne(opts, rep, agents, st, id, req)
		if err != nil {
			return res, err
		}
		rep.Event(logEvent(LevelSuccess, id, CodeUninstalled, "uninstalled "+id))
		// Persist after each skill so disk and registry never drift if a later
		// skill fails mid-batch.
		if err := state.Save(opts.Home, st); err != nil {
			return res, err
		}
		res.Skills = append(res.Skills, done)
	}

	// Drop any tracked root that no longer holds a link now that these skills'
	// local links are gone.
	if reconcileLocalRoots(opts.Home, agents, st) {
		if err := state.Save(opts.Home, st); err != nil {
			return res, err
		}
	}
	return res, nil
}

// uninstallOne removes one skill's requested copies, links and lock entries,
// clearing only their install markers in st (the caller persists). An entry
// with remaining installs is kept. linker.Unlink is
// idempotent for absent links and refuses to touch foreign symlinks or real
// files; under opts.Force such refusals are downgraded to warnings so the entry
// can still be dropped (the foreign entry stays put).
func uninstallOne(opts Options, rep Reporter, agents []agentdir.Agent, st *state.State, id string, req UninstallRequest) (UninstalledSkill, error) {
	home, cwd := opts.Home, opts.Cwd
	done := UninstalledSkill{ID: id}
	// blocked reports err as a warning under Force, and otherwise turns it
	// into the error that stops the uninstall.
	blocked := func(code string, err error) error {
		if !opts.Force {
			return &UninstallBlockedError{ID: id, Err: err}
		}
		rep.Event(logEvent(LevelWarn, id, code, err.Error()))
		done.Warnings = append(done.Warnings, err)
		return nil
	}

	// Inspect other installs before deleting the selected target. Force can
	// continue past an unreadable location, but uncertainty keeps tracking.
	remaining := false
	if req.Scope != nil {
		var err error
		remaining, err = remainingInstalls(opts, agents, st, id, req)
		if err != nil {
			if err := blocked(CodeUnlinkFailed, err); err != nil {
				return done, err
			}
			remaining = true
		}
	}

	// Delete the canonical copies FIRST — the Global one, then the committed
	// Local ones in every recorded project — so a later symlink sweep over the
	// same place sees an empty slot rather than refusing on a real directory.
	// VendorRemove also clears each scope's agent links, and only deletes a
	// directory the registry records as skillm's own copy. The Local removals
	// edit the user's git working tree; the caller's confirmation already
	// named those directories. A missing copy (project moved/deleted) is
	// silently skipped.
	if req.Scope == nil || *req.Scope == agentdir.Global {
		removedGlobal, unlinkErr, err := VendorRemove(rep, home, id, agents, agentdir.Global, cwd, st.IsGlobal(id), agentdir.Global.String())
		if unlinkErr != nil {
			// Nothing revisits the global link paths, so a link left behind is
			// recorded here. It never blocked the uninstall, and still does not.
			done.Warnings = append(done.Warnings, unlinkErr)
		}
		if err != nil {
			if err := blocked(CodeRemoveFailed, err); err != nil {
				return done, err
			}
		}
		if removedGlobal {
			done.RemovedCopies = append(done.RemovedCopies, agentdir.Global.String())
			rep.Event(logEvent(LevelSuccess, id, CodeCopyRemoved,
				fmt.Sprintf("deleted copy of %s in %s (global)", id, CanonicalDisplay(agentdir.Global))))
		}
	}

	roots := append([]string(nil), st.VendoredRoots(id)...)
	if req.Scope != nil {
		roots = nil
		if *req.Scope == agentdir.Local {
			roots = []string{req.Base}
		}
	}
	for _, dir := range roots {
		localAgents, _ := SplitLocalAliased(agents, dir)
		label := ScopeLabel(agentdir.Local, dir, cwd)
		// An unlink failure here is not recorded: the sweep below revisits
		// the same link paths and blocks (or, under Force, warns) on it.
		recorded := recordedProject(st, id, dir)
		removed, _, err := VendorRemove(rep, home, id, localAgents, agentdir.Local, dir, recorded, label)
		if err != nil {
			if err := blocked(CodeRemoveFailed, err); err != nil {
				return done, err
			}
		}
		if removed {
			done.RemovedCopies = append(done.RemovedCopies, dir)
			rep.Event(logEvent(LevelSuccess, id, CodeCopyRemoved,
				fmt.Sprintf("deleted copy of %s in %s (%s)", id, agentdir.CanonicalLocalRel, label)))
		}
		if recorded {
			if err := RemoveLockEntry(rep, id, dir); err != nil {
				done.Warnings = append(done.Warnings, err)
			}
		}
	}

	// Sweep tracked local roots AND vendored roots for stray symlinks: a
	// vendored root may also hold one, and need not be in LocalRoots.
	sweepDirs := append(append([]string{}, st.LocalRoots...), st.VendoredRoots(id)...)
	sweepDirs = localScanDirs(sweepDirs, cwd)
	if req.Scope != nil {
		sweepDirs = roots
	}
	for _, dir := range sweepDirs {
		// Skip a local dir where every agent's local folder is its global one
		// (e.g. home): the global pass above already removed those links, so a
		// local pass would only repeat the work and double-report it.
		real, _ := SplitLocalAliased(agents, dir)
		if len(real) == 0 {
			continue
		}
		res, err := linker.Unlink(home, id, real, agentdir.Local, dir)
		if err != nil {
			code := CodeUnlinkFailed
			if errors.Is(err, linker.ErrNotManaged) {
				code = CodeUnlinkRefused
			}
			if err := blocked(code, err); err != nil {
				return done, err
			}
		}
		for _, ar := range res.Agents {
			if ar.Action == linker.ActionRemoved {
				rep.Event(logEvent(LevelSuccess, id, CodeUnlinked,
					fmt.Sprintf("unlinked %s from %s (%s)", id, ar.Agent.Name, ScopeLabel(agentdir.Local, dir, cwd))))
			}
		}
	}

	// Keep the entry, including its Source and Revision, while another install
	// remains so it can still be updated or installed in another project.
	if req.Scope == nil {
		st.Remove(id)
	} else {
		if *req.Scope == agentdir.Global {
			st.SetGlobal(id, false)
		} else {
			for _, root := range append([]string(nil), st.VendoredRoots(id)...) {
				if sameProject(root, req.Base) {
					st.RemoveVendoredRoot(id, root)
				}
			}
		}
		if !remaining {
			st.Remove(id)
		}
	}
	return done, nil
}

// remainingInstalls checks recorded copies and live links outside the selected
// target. It discovers other installs without deleting them.
func remainingInstalls(opts Options, agents []agentdir.Agent, st *state.State, id string, req UninstallRequest) (bool, error) {
	remaining := false
	var scanErrors []error
	if *req.Scope != agentdir.Global {
		remaining = st.IsGlobal(id)
		found, err := managedLinksAt(opts.Home, id, agents, agentdir.Global, "")
		if err != nil {
			scanErrors = append(scanErrors, err)
		}
		remaining = remaining || found
	}
	for _, root := range st.VendoredRoots(id) {
		if *req.Scope != agentdir.Local || !sameProject(root, req.Base) {
			remaining = true
		}
	}
	for _, root := range localScanDirs(st.LocalRoots, opts.Cwd) {
		if *req.Scope == agentdir.Local && sameProject(root, req.Base) {
			continue
		}
		found, err := managedLinksAt(opts.Home, id, agents, agentdir.Local, root)
		if err != nil {
			scanErrors = append(scanErrors, err)
		}
		if found || err != nil {
			// A forced removal must remember unreadable roots too, so a
			// later run can discover links hidden by the inspection failure.
			st.AddLocalRoot(root)
			remaining = true
		}
	}
	return remaining, errors.Join(scanErrors...)
}

// sameProject accepts alternate paths to the same directory, while exact
// cleaned paths still match when a recorded project has been deleted.
func sameProject(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ai, aerr := os.Stat(a)
	bi, berr := os.Stat(b)
	return aerr == nil && berr == nil && os.SameFile(ai, bi)
}

func recordedProject(st *state.State, id, base string) bool {
	for _, root := range st.VendoredRoots(id) {
		if sameProject(root, base) {
			return true
		}
	}
	return false
}
