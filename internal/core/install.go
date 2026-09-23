package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/ultrakorne/skillm/internal/agentdir"
	"github.com/ultrakorne/skillm/internal/config"
	"github.com/ultrakorne/skillm/internal/state"
	"github.com/ultrakorne/skillm/internal/store"
)

// Event codes InstallSkills reports with, beyond the vendor primitives' own.
const (
	// CodeAgentSkipped: an enabled agent gets no install at this scope (it
	// defines no location there, or its local folder is its global one).
	CodeAgentSkipped = "agent_skipped"
	// CodeInstalled: a skill's canonical copy landed (Text names the action).
	CodeInstalled = "installed"
	// CodeInstallBlocked: a skill was skipped because a foreign entry holds its
	// canonical slot and the caller declined to overwrite it.
	CodeInstallBlocked = "install_blocked"
	// CodeStateNotSaved: the installs landed but the Registry save failed.
	CodeStateNotSaved = "state_not_saved"
	// CodeInstallFailed: the ItemDone code of the skill whose content could
	// not be staged or written; the install stopped there.
	CodeInstallFailed = "install_failed"
)

// InstallRequest says what InstallSkills installs and where.
type InstallRequest struct {
	// Inspection is the inspected Source to install from (source mode). Nil
	// selects id mode: IDs are then already-installed skills to add a scope
	// to.
	Inspection *Inspection
	// IDs are the skills to install. In source mode they are ids of
	// Inspection.Skills (their own ids, before As); in id mode they are
	// registered Skill IDs. At least one is required.
	IDs []string
	// As installs the single selected skill under this Skill ID instead of its
	// own (source mode only).
	As string
	// Commit, when set, is the git commit the caller expects the Inspection to
	// be pinned to (the full SHA, or a prefix of at least 7 characters). It
	// lets a caller that inspected in another process install exactly what it
	// showed while Inspection.Ref, the ref recorded for updates, stays a
	// branch or tag: a mismatch is a *CommitMismatchError before anything is
	// written. Source mode with a git Source only.
	Commit string
	// Scope is where to install.
	Scope agentdir.Scope
	// Base is the absolute project directory of a Local install. It is
	// ignored for Global.
	Base string
	// SkipForeign installs the skills whose canonical slot is free and skips
	// (with a CodeInstallBlocked event) those where a foreign entry is in the
	// way, instead of returning a *ForeignFilesError. It is how a caller
	// proceeds after the user declined to overwrite.
	SkipForeign bool
}

// InstalledSkill is what InstallSkills did for one skill.
type InstalledSkill struct {
	ID string
	// Action is what happened at the canonical slot; VendorBlocked means the
	// skill was skipped and nothing was written for it.
	Action VendorAction
}

// InstallResult is InstallSkills' outcome, one entry per skill it reached.
type InstallResult struct {
	Scope  agentdir.Scope
	Base   string
	Skills []InstalledSkill
}

// InstalledAny reports whether at least one skill's copy landed.
func (r InstallResult) InstalledAny() bool {
	for _, s := range r.Skills {
		if s.Action != VendorBlocked {
			return true
		}
	}
	return false
}

// installItem is one skill resolved for installing: the Registry entry to
// record once it lands and where its content comes from.
type installItem struct {
	entry state.SkillEntry
	// insp is the inspected skill (source mode); nil in id mode.
	insp *InspectedSkill
	// dir is the staged content, set by stageItems.
	dir string
}

// InstallSkills installs skills at one Scope for every enabled agent: the
// canonical copy in the scope's .agents/skills store, a link for every other
// enabled agent, a skills-lock.json entry at Local scope, and the Registry
// entry recording the install. In source mode the content is exactly the
// inspected commit; in id mode it comes from the skill's global copy, its
// local source directory, or a re-fetch of its recorded source@ref.
//
// Everything is checked before anything is written: an unknown id, a
// different-source collision (*SourceCollisionError), an As on several skills
// (ErrAsMultiple), an Inspection not at req.Commit (*CommitMismatchError),
// and foreign entries at the canonical slots. The last is a
// *ForeignFilesError unless opts.Force or opts.Yes permits overwriting them, or
// req.SkipForeign skips them; the caller may ask the user and call again.
// opts.Force alone also takes over foreign entries at agent link paths.
//
// It reports progress to rep: once the selection is checked, an EventBatch
// naming the skills (by their final ids), then an ItemStart as each skill's
// content is staged and an ItemDone once its copy landed (Code CodeInstalled),
// was skipped (CodeInstallBlocked) or failed (CodeInstallFailed), alongside
// the log events. It does not take the Home lock: the caller holds it. It reloads config and the Registry and re-plans the selection itself, so
// a caller may inspect and prompt before locking. On a failure part-way it
// still records the installs that landed and returns them with the error.
func InstallSkills(ctx context.Context, opts Options, rep Reporter, req InstallRequest) (InstallResult, error) {
	rep = nopIfNil(rep)
	res := InstallResult{Scope: req.Scope, Base: req.Base}
	if len(req.IDs) == 0 {
		return res, errors.New("no skills selected to install")
	}
	if req.Scope == agentdir.Local && !filepath.IsAbs(req.Base) {
		return res, fmt.Errorf("a local install needs an absolute project directory, got %q", req.Base)
	}
	home := opts.Home

	if req.Inspection != nil {
		// Materialize Home and config.toml (with the built-in defaults) on
		// first use, so config is the visible, hand-editable source of truth
		// for agent locations. Never clobbers an existing file.
		if err := store.EnsureHome(home); err != nil {
			return res, err
		}
		if err := config.EnsureExists(home); err != nil {
			return res, err
		}
	}
	cfg, err := config.Load(home)
	if err != nil {
		return res, err
	}
	enabled := cfg.EnabledAgents()
	if len(enabled) == 0 {
		return res, fmt.Errorf("no enabled agents in %s", config.Path(home))
	}
	st, err := state.Load(home)
	if err != nil {
		return res, err
	}

	var items []installItem
	if req.Inspection != nil {
		if err := checkCommit(req.Inspection, req.Commit); err != nil {
			return res, err
		}
		items, err = planSource(st, opts.Cwd, req.Inspection, req.IDs, req.As)
	} else {
		if req.Commit != "" {
			return res, errors.New("an expected commit applies only when installing from a git source")
		}
		items, err = planIDs(st, req.IDs, req.As)
	}
	if err != nil {
		return res, err
	}

	agents, err := installAgents(rep, home, enabled, req.Scope, req.Base)
	if err != nil {
		return res, err
	}

	// Scan every canonical slot for foreign entries first, so one question
	// (or one refusal) covers the whole batch and nothing is written before.
	recorded := make(map[string]bool, len(items))
	var conflicts []string
	for _, it := range items {
		id := it.entry.ID
		if req.Scope == agentdir.Local {
			recorded[id] = slices.Contains(st.VendoredRoots(id), req.Base)
		} else {
			recorded[id] = st.IsGlobal(id)
		}
		if c := VendorConflict(home, id, req.Scope, req.Base, recorded[id]); c != "" {
			conflicts = append(conflicts, c)
		}
	}
	// force overwrites foreign entries at the canonical slots; forceLinks,
	// deliberately separate, takes over foreign entries at agent link paths.
	// Yes answers the canonical-slot question only (which never listed the
	// link paths), so only Force sets forceLinks.
	force := opts.Force || opts.Yes
	forceLinks := opts.Force
	if len(conflicts) > 0 && !force && !req.SkipForeign {
		return res, &ForeignFilesError{Paths: conflicts}
	}

	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.entry.ID
	}
	rep.Event(Event{Type: EventBatch, Items: ids})

	cleanup, err := stageItems(ctx, rep, home, opts.Cwd, st, req.Inspection, items)
	defer cleanup()
	if err != nil {
		return res, err
	}

	label := ScopeLabel(req.Scope, req.Base, opts.Cwd)
	stateDirty := false
	var runErr error
	for i, it := range items {
		id := it.entry.ID
		action, err := VendorOne(rep, home, id, it.dir, agents, req.Scope, req.Base, recorded[id], force, forceLinks, label)
		if err != nil {
			rep.Event(itemDone(i, installFailed(id, err)))
			runErr = err
			break
		}
		res.Skills = append(res.Skills, InstalledSkill{ID: id, Action: action})
		if action == VendorBlocked {
			ev := logEvent(LevelWarn, id, CodeInstallBlocked,
				fmt.Sprintf("skipped %s: installing here would overwrite files skillm did not create", id))
			rep.Event(ev)
			rep.Event(itemDone(i, ev))
			continue
		}
		ev := logEvent(LevelSuccess, id, CodeInstalled,
			fmt.Sprintf("%s %s in %s (%s)", action.Label(), id, CanonicalDisplay(req.Scope), label))
		rep.Event(ev)
		rep.Event(itemDone(i, ev))

		// Record the entry now that its copy landed. Upsert first (Source/
		// Path/Ref/Revision, preserving any install markers merged in
		// earlier), then add this scope's marker.
		st.Upsert(it.entry)
		stateDirty = true
		if req.Scope == agentdir.Local {
			st.AddVendoredRoot(id, req.Base)
			if entry, ok := st.Get(id); ok {
				_ = UpsertLockEntry(rep, entry, req.Base)
			}
		} else {
			st.SetGlobal(id, true)
		}
	}

	// Remember the project directory so list, update and uninstall can find
	// this install from anywhere. Done even on a partial failure, so a copy
	// that did land is found.
	if req.Scope == agentdir.Local && res.InstalledAny() && st.AddLocalRoot(req.Base) {
		stateDirty = true
	}
	if stateDirty {
		if serr := state.Save(home, st); serr != nil {
			rep.Event(logEvent(LevelWarn, "", CodeStateNotSaved,
				fmt.Sprintf("installed, but could not record the install for `skillm list`/`update`: %v", serr)))
		}
	}
	return res, runErr
}

// ValidateSourceSelection checks, without writing anything, that installing
// ids from insp (under As, when set) would not fail on the selection itself:
// every id is inspected, As names at most one skill (ErrAsMultiple), and no
// id is already installed from a different Source (*SourceCollisionError).
// InstallSkills checks the same; calling this first lets a caller report a
// bad selection before asking where to install.
func ValidateSourceSelection(opts Options, insp *Inspection, ids []string, as string) error {
	st, err := state.Load(opts.Home)
	if err != nil {
		return err
	}
	_, err = planSource(st, opts.Cwd, insp, ids, as)
	return err
}

// ValidateIDSelection checks, without writing anything or touching the
// network, that installing the registered ids (id mode) would not fail on the
// selection itself: every id is registered, and a local-path skill with no
// global copy still has its source directory. InstallSkills checks the same;
// calling this first lets a caller report it before asking where to install.
// A git skill that has to be re-fetched can still fail later, in
// InstallSkills.
func ValidateIDSelection(opts Options, ids []string) error {
	st, err := state.Load(opts.Home)
	if err != nil {
		return err
	}
	items, err := planIDs(st, ids, "")
	if err != nil {
		return err
	}
	for _, it := range items {
		e := it.entry
		if e.Kind != state.KindLocal || (e.Global && CopyExists(opts.Home, e.ID, agentdir.Global, "")) {
			continue
		}
		if _, err := localSourceDir(e, opts.Cwd); err != nil {
			return err
		}
	}
	return nil
}

// CheckCommit reports a *CommitMismatchError when want is set and the
// Inspection is not pinned to it (want may be an abbreviated SHA of at least
// 7 characters), and a plain error when want is not a commit SHA at all (see
// IsCommitSHA) or is set for a local Source. It is the
// check InstallSkills makes for InstallRequest.Commit, for a caller that
// wants to fail before asking any question.
func (i *Inspection) CheckCommit(want string) error { return checkCommit(i, want) }

// checkCommit is CheckCommit.
func checkCommit(insp *Inspection, want string) error {
	if want == "" {
		return nil
	}
	if insp.Kind != state.KindGit {
		return errors.New("an expected commit applies only when installing from a git source")
	}
	if !IsCommitSHA(want) {
		// Not a mismatch: no inspection could ever match it, so reporting
		// "the source changed" would send a caller round a re-inspect loop.
		return fmt.Errorf("expected commit %q is not a commit SHA (7 to 64 hex characters)", want)
	}
	if !strings.HasPrefix(insp.Commit, strings.ToLower(want)) {
		return &CommitMismatchError{Want: want, Got: insp.Commit}
	}
	return nil
}

// IsCommitSHA reports whether s is a full or abbreviated commit SHA as an
// expected commit accepts it: 7 to 64 hex characters, nothing else.
func IsCommitSHA(s string) bool {
	if len(s) < 7 || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return false
		}
	}
	return true
}

// planSource resolves the selected inspected skills to install items with
// their Registry entries (Revision filled in by stageItems), refusing the
// whole selection on the first different-source collision.
func planSource(st *state.State, cwd string, insp *Inspection, ids []string, as string) ([]installItem, error) {
	chosen, err := insp.Select(ids)
	if err != nil {
		return nil, err
	}
	if len(chosen) == 0 {
		return nil, errors.New("no skills selected to install")
	}
	if err := CheckAsSingle(as, len(chosen)); err != nil {
		return nil, err
	}
	items := make([]installItem, 0, len(chosen))
	for i := range chosen {
		s := &chosen[i]
		id := ChosenID(s.ID, as)
		fresh := state.SkillEntry{ID: id, Kind: insp.Kind, InstalledAt: time.Now().UTC()}
		ident := SrcIdentity{Kind: insp.Kind, Base: cwd}
		if insp.Kind == state.KindGit {
			fresh.Source, fresh.Path, fresh.Ref = insp.Source, s.Path, insp.Ref
			ident.Source, ident.Path = insp.Source, s.Path
		} else {
			fresh.Source = s.Path
			ident.Source = s.Path
		}
		if err := RegistryCollision(st, id, ident); err != nil {
			return nil, err
		}
		items = append(items, installItem{entry: fresh, insp: s})
	}
	return items, nil
}

// planIDs resolves registered ids to install items (id mode).
func planIDs(st *state.State, ids []string, as string) ([]installItem, error) {
	if as != "" {
		return nil, errors.New("an id override applies only when installing from a source")
	}
	items := make([]installItem, 0, len(ids))
	for _, id := range ids {
		e, ok := st.Get(id)
		if !ok {
			return nil, fmt.Errorf("skill %q is not registered", id)
		}
		items = append(items, installItem{entry: e})
	}
	return items, nil
}

// itemDone turns ev, a log event about item i, into that item's ItemDone.
func itemDone(i int, ev Event) Event {
	ev.Type, ev.Index = EventItemDone, i
	return ev
}

// installFailed is the error event for skill id's failed install.
func installFailed(id string, err error) Event {
	return logEvent(LevelError, id, CodeInstallFailed, fmt.Sprintf("%s: %v", id, err))
}

// stageItems finds every item's content: an inspected skill is staged from its
// Inspection (its entry then gets the Revision just read and is merged over
// its existing entry in st), a registered one from idModeSource. rep gets an
// ItemStart as each item's staging begins, and an ItemDone for the item whose
// staging failed. The returned cleanup removes any re-fetch temp dirs; it is
// never nil.
func stageItems(ctx context.Context, rep Reporter, home, cwd string, st *state.State, insp *Inspection, items []installItem) (cleanup func(), err error) {
	var cleanups []func()
	cleanup = func() {
		for _, c := range cleanups {
			c()
		}
	}
	for i := range items {
		it := &items[i]
		rep.Event(Event{Type: EventItemStart, Index: i, Skill: it.entry.ID})
		if it.insp != nil {
			dir, rev, err := insp.stage(ctx, *it.insp, it.entry.ID)
			if err != nil {
				rep.Event(itemDone(i, installFailed(it.entry.ID, err)))
				return cleanup, err
			}
			it.dir = dir
			it.entry.Revision = rev
			it.entry = MergeEntry(st, it.entry.ID, it.entry)
			continue
		}
		dir, clean, err := idModeSource(ctx, home, cwd, &it.entry)
		if clean != nil {
			cleanups = append(cleanups, clean)
		}
		if err != nil {
			rep.Event(itemDone(i, installFailed(it.entry.ID, err)))
			return cleanup, err
		}
		it.dir = dir
	}
	return cleanup, nil
}

// idModeSource resolves the directory whose content is skill e's, for an
// install-by-id (adding a scope/project to an already-registered skill). In
// order: (1) the canonical global copy when the skill is installed globally
// and that copy exists (no network); (2) a local-path skill's recorded source
// directory when it still exists; (3) otherwise a fresh re-fetch from
// e.Source@e.Ref (git), which materializes the content and may advance e's
// recorded Revision. It returns the content dir and a cleanup func (nil when
// no temp was created). e is mutated in place when a re-fetch advances the
// revision.
func idModeSource(ctx context.Context, home, cwd string, e *state.SkillEntry) (string, func(), error) {
	if e.Global && CopyExists(home, e.ID, agentdir.Global, "") {
		return agentdir.CanonicalSkillDirAt(agentdir.Global, "", e.ID), nil, nil
	}
	if e.Kind == state.KindLocal {
		src, err := localSourceDir(*e, cwd)
		return src, nil, err
	}
	// Git skill with no reusable global copy: re-fetch from the pinned source.
	dir, rev, clean, err := RefetchSkill(ctx, *e)
	if err != nil {
		return "", nil, fmt.Errorf("re-fetch %q from %s: %w", e.ID, e.Source, err)
	}
	if rev != e.Revision {
		e.Revision = rev
		e.InstalledAt = time.Now().UTC()
	}
	return dir, clean, nil
}

// localSourceDir returns the recorded source directory of the local-path
// skill e when it still exists. A legacy relative Source is resolved against
// cwd; with no cwd it is refused rather than looked up in the process's
// working directory.
func localSourceDir(e state.SkillEntry, cwd string) (string, error) {
	if !filepath.IsAbs(e.Source) && cwd == "" {
		return "", fmt.Errorf("local skill %q has no global copy and its recorded source %s is relative, with no working directory to resolve it against; reinstall it from a source", e.ID, e.Source)
	}
	if src := ResolvePath(e.Source, cwd); isDir(src) {
		return src, nil
	}
	return "", fmt.Errorf("local skill %q has no global copy and its source %s is gone; reinstall it from a source", e.ID, e.Source)
}

// installAgents returns the enabled agents an install at (scope, base) links
// for. An agent that defines no location at scope is skipped with a
// CodeAgentSkipped event; it is only an error when no enabled agent has one.
// At Local scope an agent whose local folder IS its global folder at base (the
// canonical case: base is the home directory) is skipped the same way, so a
// "local" link never silently means global and base is never recorded as a
// local root; when that leaves none, it is a *LocalScopeAliasedError.
func installAgents(rep Reporter, home string, enabled []agentdir.Agent, scope agentdir.Scope, base string) ([]agentdir.Agent, error) {
	var supported []agentdir.Agent
	for _, a := range enabled {
		if a.Supports(scope) {
			supported = append(supported, a)
		} else {
			rep.Event(logEvent(LevelWarn, "", CodeAgentSkipped, fmt.Sprintf("skipped %s: no %s location", a.Name, scope)))
		}
	}
	if scope == agentdir.Local {
		real, aliased := SplitLocalAliased(supported, base)
		for _, a := range aliased {
			rep.Event(logEvent(LevelWarn, "", CodeAgentSkipped,
				fmt.Sprintf("skipped %s: local scope here resolves to its global skill folder", a.Name)))
		}
		if len(real) == 0 && len(aliased) > 0 {
			return nil, &LocalScopeAliasedError{Base: base}
		}
		supported = real
	}
	if len(supported) == 0 {
		return nil, fmt.Errorf("no enabled agent has a %s location; define one in %s", scope, config.Path(home))
	}
	return supported, nil
}

// SplitLocalAliased partitions agents by whether each has a *usable* local
// skill folder at base. An agent's local scope is real when its local folder
// resolves to a different directory than its global folder; it is aliased
// when the two coincide (see agentdir.LocalAliasesGlobal), the canonical case
// being base == home, where e.g. <base>/.claude/skills *is* ~/.claude/skills.
// Callers pass agents already known to support a local folder; an agent
// without a global folder cannot alias and falls into real. This is how every
// local scan/write site avoids treating the global folder as if it were local.
func SplitLocalAliased(agents []agentdir.Agent, base string) (real, aliased []agentdir.Agent) {
	for _, a := range agents {
		if agentdir.LocalAliasesGlobal(a, base) {
			aliased = append(aliased, a)
		} else {
			real = append(real, a)
		}
	}
	return real, aliased
}

// ScopeLabel renders the scope for per-agent report lines. Global and a Local
// install rooted at cwd keep their bare names; a Local install rooted
// elsewhere also shows the directory so the output is unambiguous.
func ScopeLabel(scope agentdir.Scope, base, cwd string) string {
	if scope == agentdir.Global || base == "" || base == cwd {
		return scope.String()
	}
	return fmt.Sprintf("%s: %s", scope, base)
}

// isDir reports whether p is an existing directory.
func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}
