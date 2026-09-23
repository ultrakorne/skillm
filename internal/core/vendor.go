package core

import (
	"errors"
	"fmt"
	"os"

	"github.com/ultrakorne/skillm/internal/agentdir"
	"github.com/ultrakorne/skillm/internal/linker"
	"github.com/ultrakorne/skillm/internal/store"
)

// Vendored-install primitives shared by install, update, uninstall, import
// and agent. An install at either scope writes a real copy of the skill into
// that scope's canonical store — <base>/.agents/skills/<id> for a Local
// install, ~/.agents/skills/<id> for a Global one: the cross-agent convention
// read natively by Codex, Cursor, Amp, Gemini CLI and friends, and the same
// layout vercel's skills CLI produces — plus a symlink into that copy for
// every enabled agent whose folder at the scope is elsewhere (RELATIVE at
// Local so links survive a clone, e.g. .claude/skills/<id> ->
// ../../.agents/skills/<id>; absolute at Global). At Local scope a
// skills-lock.json entry is written alongside (see locksync.go), so copy,
// links, and lockfile are all committable and teammates get working skills on
// clone with no tooling.
//
// A real-directory copy cannot be re-discovered by a live link scan, so the
// installs holding one are recorded in state.toml: the project roots per
// skill in SkillEntry.VendoredAt, the Global install in SkillEntry.Global.
// The invariant is that a recorded install's canonical slot is skillm's own
// copy.
//
// These primitives write to disk but never take the Home lock: the caller
// holds it for the whole command (see store.Lock).

// Event codes the vendor primitives report with.
const (
	CodeLinked         = "linked"
	CodeLinkTakenOver  = "link_taken_over"
	CodeLinkRefused    = "link_refused" // a foreign entry is in the way; force would take it over
	CodeLinkFailed     = "link_failed"  // an I/O failure (permissions, no symlink privilege, …)
	CodeUnlinked       = "unlinked"
	CodeUnlinkRefused  = "unlink_refused" // the entry is not skillm's; it is left alone
	CodeUnlinkFailed   = "unlink_failed"  // an I/O failure removing or inspecting the link
	CodeCopyRefreshed  = "copy_refreshed"
	CodeCopySynced     = "copy_synced"
	CodeCopyFailed     = "copy_failed"
	CodeLockfileFailed = "lockfile_not_updated"
)

// VendorAction is what VendorOne did to the canonical slot at one scope/base.
type VendorAction int

const (
	// VendorWrote: a copy was written where nothing existed.
	VendorWrote VendorAction = iota
	// VendorConverted: a legacy skillm symlink into Home was replaced by a copy.
	VendorConverted
	// VendorRefreshed: an existing recorded copy was overwritten.
	VendorRefreshed
	// VendorAdopted: a foreign file/dir/symlink was overwritten under force.
	VendorAdopted
	// VendorBlocked: a foreign entry was left untouched (no force).
	VendorBlocked
)

// Label renders a VendorAction as a short past-tense verb for report lines.
func (a VendorAction) Label() string {
	switch a {
	case VendorWrote, VendorAdopted:
		return "installed"
	case VendorConverted:
		return "converted to copy"
	case VendorRefreshed:
		return "refreshed"
	default:
		return ""
	}
}

// CanonicalDisplay names the scope's canonical store in report lines:
// ".agents/skills" for a Local install, "~/.agents/skills" for a Global one.
func CanonicalDisplay(scope agentdir.Scope) string {
	if scope == agentdir.Local {
		return agentdir.CanonicalLocalRel
	}
	return "~/" + agentdir.CanonicalLocalRel
}

// VendorConflict returns the canonical slot's path when installing id at
// (scope, base) would overwrite something skillm did not create: a real file,
// a foreign symlink, or an unrecorded real directory. When the install is
// already recorded (a vendored root for Local, the Global flag for Global),
// the directory there is skillm's own copy — no conflict. A legacy skillm
// symlink into Home is never a conflict (it is converted).
func VendorConflict(home, id string, scope agentdir.Scope, base string, recorded bool) string {
	slot := agentdir.CanonicalSkillDirAt(scope, base, id)
	kind, _, err := linker.Classify(home, slot)
	if err != nil {
		return slot
	}
	switch kind {
	case linker.TargetDir:
		if !recorded {
			return slot
		}
	case linker.TargetForeignLink, linker.TargetFile:
		return slot
	}
	return ""
}

// VendorOne materializes skill id's install at (scope, base): the canonical
// copy from srcDir (the skill's staged/fetched content, or an existing
// canonical copy elsewhere), then a link for every supplied agent that needs
// one. recorded says whether the install is already recorded for this skill (so
// a directory at the canonical slot is skillm's own copy); force permits
// overwriting a foreign entry at the canonical slot; label names the scope in
// per-link report lines. It returns what happened to the canonical slot;
// VendorBlocked means nothing was written at all. Link refusals (a foreign
// entry at an agent's link path) are reported, never fatal: the copy is the
// unit that is recorded, links are re-derivable from disk.
//
// forceLinks is deliberately separate from force: force may come from an
// interactive "yes" to a prompt that only ever lists canonical-slot
// conflicts, so it must not also authorize deleting unrelated foreign entries
// at agent link paths that prompt never showed. Only an explicit --force
// should set forceLinks.
func VendorOne(rep Reporter, home, id, srcDir string, agents []agentdir.Agent, scope agentdir.Scope, base string, recorded, force, forceLinks bool, label string) (VendorAction, error) {
	slot := agentdir.CanonicalSkillDirAt(scope, base, id)

	kind, _, err := linker.Classify(home, slot)
	if err != nil {
		return VendorBlocked, err
	}

	action := VendorWrote
	switch kind {
	case linker.TargetAbsent:
	case linker.TargetOurLink:
		// A legacy absolute symlink into Home from before installs were
		// vendored: drop it, then write the real copy in its place.
		if err := os.Remove(slot); err != nil && !os.IsNotExist(err) {
			return VendorBlocked, fmt.Errorf("remove legacy symlink %s: %w", slot, err)
		}
		action = VendorConverted
	case linker.TargetDir:
		if recorded {
			action = VendorRefreshed
		} else if force {
			action = VendorAdopted
		} else {
			return VendorBlocked, nil
		}
	case linker.TargetForeignLink, linker.TargetFile:
		if !force {
			return VendorBlocked, nil
		}
		if err := os.Remove(slot); err != nil && !os.IsNotExist(err) {
			return VendorBlocked, fmt.Errorf("remove %s: %w", slot, err)
		}
		action = VendorAdopted
	}

	// ReplaceDir stages the new content before touching the slot, so a failure
	// never destroys an existing copy.
	if err := store.ReplaceDir(srcDir, slot); err != nil {
		return VendorBlocked, fmt.Errorf("install copy of %s: %w", id, err)
	}

	LinkVendorAgents(rep, home, id, agents, scope, base, label, forceLinks)
	return action, nil
}

// LinkVendorAgents creates (or repoints) the agent links into the canonical
// copy of id at (scope, base) for every supplied agent, reporting refusals
// instead of failing — a foreign file at one agent's link path must not block
// the others. With force, such a foreign entry is replaced by the link
// instead (taking the skill over); without it, the refusal is reported with
// code link_refused, which a caller may answer by retrying with force. Any
// other link error (an I/O failure that force cannot fix) is reported as
// link_failed. It reports whether any link was created or replaced.
func LinkVendorAgents(rep Reporter, home, id string, agents []agentdir.Agent, scope agentdir.Scope, base, label string, force bool) (linked bool) {
	rep = nopIfNil(rep)
	link := linker.Link
	if force {
		link = linker.LinkForce
	}
	for _, a := range agents {
		res, err := link(home, id, []agentdir.Agent{a}, scope, base)
		if err != nil {
			if errors.Is(err, linker.ErrNotManaged) {
				rep.Event(logEvent(LevelWarn, id, CodeLinkRefused, err.Error()))
			} else {
				rep.Event(logEvent(LevelWarn, id, CodeLinkFailed, err.Error()))
			}
		}
		for _, ar := range res.Agents {
			switch ar.Action {
			case linker.ActionCreated:
				linked = true
				rep.Event(logEvent(LevelSuccess, id, CodeLinked,
					fmt.Sprintf("linked %s for %s (%s)", id, ar.Agent.Name, label)))
			case linker.ActionReplaced:
				linked = true
				rep.Event(logEvent(LevelSuccess, id, CodeLinkTakenOver,
					fmt.Sprintf("took over %s for %s (%s): replaced %s", id, ar.Agent.Name, label, ar.Path)))
			}
		}
	}
	return linked
}

// VendorRemove deletes skill id's install at (scope, base): every supplied
// agent's link into the canonical copy, any legacy skillm symlink occupying
// the canonical slot itself, and — when removeCopy is true, i.e. the install
// is recorded so the directory there is skillm's own — the copy. Foreign
// entries are never touched (a refusal is reported as unlink_refused, an I/O
// failure as unlink_failed); a missing copy is a no-op. It returns whether a
// copy was removed.
func VendorRemove(rep Reporter, home, id string, agents []agentdir.Agent, scope agentdir.Scope, base string, removeCopy bool, label string) (removedCopy bool, err error) {
	rep = nopIfNil(rep)
	res, lerr := linker.Unlink(home, id, agents, scope, base)
	if lerr != nil {
		code := CodeUnlinkFailed
		if errors.Is(lerr, linker.ErrNotManaged) {
			code = CodeUnlinkRefused
		}
		rep.Event(logEvent(LevelWarn, id, code, lerr.Error()))
	}
	for _, ar := range res.Agents {
		if ar.Action == linker.ActionRemoved {
			rep.Event(logEvent(LevelSuccess, id, CodeUnlinked,
				fmt.Sprintf("unlinked %s from %s (%s)", id, ar.Agent.Name, label)))
		}
	}

	slot := agentdir.CanonicalSkillDirAt(scope, base, id)
	kind, _, cerr := linker.Classify(home, slot)
	if cerr != nil {
		return false, cerr
	}
	switch kind {
	case linker.TargetOurLink:
		// A legacy layout where the canonical slot itself is a symlink into
		// Home: it is skillm's, and Unlink skipped it (the canonical agent
		// holds no separate link), so clear it here.
		if rerr := os.Remove(slot); rerr != nil && !os.IsNotExist(rerr) {
			return false, fmt.Errorf("remove legacy symlink %s: %w", slot, rerr)
		}
		return false, nil
	case linker.TargetDir:
		if !removeCopy {
			return false, nil
		}
		if rerr := os.RemoveAll(slot); rerr != nil {
			return false, fmt.Errorf("remove copy %s: %w", slot, rerr)
		}
		return true, nil
	}
	return false, nil
}

// RefreshCopy overwrites the canonical copy at target from src when it is due:
// always for a just-updated git skill (src is its staged clone), and otherwise
// on content drift — for a git skill whose revision did not advance (src is the
// staged clone of the unchanged upstream) as much as for a local-path skill
// (src is its recorded source directory). Comparing content, rather than
// trusting the recorded revision, is what repairs an install that was reverted
// or hand-edited out from under state.toml; comparing it first is what keeps an
// already-correct install from producing pointless git churn. An empty src
// means there is nothing to refresh from (the fetch failed, or the skill was
// not in scope), so nothing is written. place names the install in report lines
// (a project root or "global"). It returns whether the copy was rewritten; a
// write failure is reported and returned, with rewritten false, leaving the
// install recorded so the next update retries.
func RefreshCopy(rep Reporter, src, id, target, place string, isGit, updated bool) (rewritten bool, err error) {
	rep = nopIfNil(rep)
	write := func(verb, done, code string) (bool, error) {
		if err := store.ReplaceDir(src, target); err != nil {
			err = fmt.Errorf("%s copy %s: %w", verb, target, err)
			rep.Event(logEvent(LevelWarn, id, CodeCopyFailed, err.Error()))
			return false, err
		}
		rep.Event(logEvent(LevelSuccess, id, code, fmt.Sprintf("%s copy of %s (%s)", done, id, place)))
		return true, nil
	}
	switch {
	case src == "":
		return false, nil
	case isGit && updated:
		return write("refresh", "refreshed", CodeCopyRefreshed)
	case !store.DirContentEqual(src, target):
		return write("sync", "synced", CodeCopySynced)
	}
	return false, nil
}

// CopyExists reports whether the canonical slot for id at (scope, base) holds
// a real directory (skillm's copy or otherwise — the caller decides via the
// recorded installs).
func CopyExists(home, id string, scope agentdir.Scope, base string) bool {
	kind, _, err := linker.Classify(home, agentdir.CanonicalSkillDirAt(scope, base, id))
	return err == nil && kind == linker.TargetDir
}

// LocalCopyExists is CopyExists at Local scope, the common case.
func LocalCopyExists(home, id, base string) bool {
	return CopyExists(home, id, agentdir.Local, base)
}

// logEvent builds an EventLog about skill id.
func logEvent(level Level, id, code, text string) Event {
	return Event{Type: EventLog, Level: level, Skill: id, Code: code, Text: text}
}

// nopIfNil returns rep, or a NopReporter when rep is nil.
func nopIfNil(rep Reporter) Reporter {
	if rep == nil {
		return NopReporter{}
	}
	return rep
}
