package cmd

import (
	"fmt"
	"os"

	"github.com/ultrakorne/skillm/internal/agentdir"
	"github.com/ultrakorne/skillm/internal/linker"
	"github.com/ultrakorne/skillm/internal/store"
	"github.com/ultrakorne/skillm/internal/ui"
)

// Vendored-install orchestration shared by install, update, uninstall, list,
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

// vendorAction is what vendorOne did to the canonical slot at one scope/base.
type vendorAction int

const (
	// vendorWrote: a copy was written where nothing existed.
	vendorWrote vendorAction = iota
	// vendorConverted: a legacy skillm symlink into Home was replaced by a copy.
	vendorConverted
	// vendorRefreshed: an existing recorded copy was overwritten with Home's content.
	vendorRefreshed
	// vendorAdopted: a foreign file/dir/symlink was overwritten under --force/confirm.
	vendorAdopted
	// vendorBlocked: a foreign entry was left untouched (no force/confirm).
	vendorBlocked
)

// vendorActionLabel renders a vendorAction as a short past-tense verb for
// report lines.
func vendorActionLabel(a vendorAction) string {
	switch a {
	case vendorWrote, vendorAdopted:
		return "installed"
	case vendorConverted:
		return "converted to copy"
	case vendorRefreshed:
		return "refreshed"
	default:
		return ""
	}
}

// canonicalDisplay names the scope's canonical store in report lines:
// ".agents/skills" for a Local install, "~/.agents/skills" for a Global one.
func canonicalDisplay(scope agentdir.Scope) string {
	if scope == agentdir.Local {
		return agentdir.CanonicalLocalRel
	}
	return "~/" + agentdir.CanonicalLocalRel
}

// vendorConflict returns the canonical slot's path when installing id at
// (scope, base) would overwrite something skillm did not create: a real file,
// a foreign symlink, or an unrecorded real directory. When the install is
// already recorded (a vendored root for Local, the Global flag for Global),
// the directory there is skillm's own copy — no conflict. A legacy skillm
// symlink into Home is never a conflict (it is converted).
func vendorConflict(home, id string, scope agentdir.Scope, base string, recorded bool) string {
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

// vendorOne materializes skill id's install at (scope, base): the canonical
// copy from srcDir (the skill's staged/fetched content, or an existing
// canonical copy elsewhere), then a link for every supplied agent that needs
// one. recorded says whether the install is already recorded for this skill (so
// a directory at the canonical slot is skillm's own copy); force permits
// overwriting a foreign entry there; label names the scope in per-link report
// lines. It returns what happened to the canonical slot; vendorBlocked means
// nothing was written at all. Link refusals (a foreign entry at an agent's
// link path) are warned about, never fatal: the copy is the unit that is
// recorded, links are re-derivable from disk.
func vendorOne(home, id, srcDir string, agents []agentdir.Agent, scope agentdir.Scope, base string, recorded, force bool, label string) (vendorAction, error) {
	src := srcDir
	slot := agentdir.CanonicalSkillDirAt(scope, base, id)

	kind, _, err := linker.Classify(home, slot)
	if err != nil {
		return vendorBlocked, err
	}

	action := vendorWrote
	switch kind {
	case linker.TargetAbsent:
	case linker.TargetOurLink:
		// A legacy absolute symlink into Home from before installs were
		// vendored: drop it, then write the real copy in its place.
		if err := os.Remove(slot); err != nil && !os.IsNotExist(err) {
			return vendorBlocked, fmt.Errorf("remove legacy symlink %s: %w", slot, err)
		}
		action = vendorConverted
	case linker.TargetDir:
		if recorded {
			action = vendorRefreshed
		} else if force {
			action = vendorAdopted
		} else {
			return vendorBlocked, nil
		}
	case linker.TargetForeignLink, linker.TargetFile:
		if !force {
			return vendorBlocked, nil
		}
		if err := os.Remove(slot); err != nil && !os.IsNotExist(err) {
			return vendorBlocked, fmt.Errorf("remove %s: %w", slot, err)
		}
		action = vendorAdopted
	}

	// ReplaceDir stages the new content before touching the slot, so a failure
	// never destroys an existing copy.
	if err := store.ReplaceDir(src, slot); err != nil {
		return vendorBlocked, fmt.Errorf("install copy of %s: %w", id, err)
	}

	linkVendorAgents(home, id, agents, scope, base, label)
	return action, nil
}

// linkVendorAgents creates (or repoints) the agent links into the canonical
// copy of id at (scope, base) for every supplied agent, warning on refusals
// instead of failing — a foreign file at one agent's link path must not block
// the others.
func linkVendorAgents(home, id string, agents []agentdir.Agent, scope agentdir.Scope, base, label string) {
	for _, a := range agents {
		res, err := linker.Link(home, id, []agentdir.Agent{a}, scope, base)
		if err != nil {
			ui.Warnf("%v", err)
		}
		for _, ar := range res.Agents {
			if ar.Action == linker.ActionCreated {
				ui.Successf("linked %s for %s (%s)", id, ar.Agent.Name, label)
			}
		}
	}
}

// vendorCopyExists reports whether the canonical slot for id at (scope, base)
// currently holds a real directory (skillm's copy or otherwise — the caller
// decides via the recorded installs).
func vendorCopyExists(home, id string, scope agentdir.Scope, base string) bool {
	kind, _, err := linker.Classify(home, agentdir.CanonicalSkillDirAt(scope, base, id))
	return err == nil && kind == linker.TargetDir
}

// localCopyExists is vendorCopyExists at Local scope, the common case.
func localCopyExists(home, id, base string) bool {
	return vendorCopyExists(home, id, agentdir.Local, base)
}

// servedAgents returns the names of the supplied agents that skill id's
// install at (scope, base) currently serves, read live from disk: agents
// whose folder at the scope IS the canonical store when the copy exists, plus
// agents holding a skillm-owned link there. Sorted by the caller if needed.
func servedAgents(home, id string, agents []agentdir.Agent, scope agentdir.Scope, base string) []string {
	copyExists := vendorCopyExists(home, id, scope, base)
	var names []string
	for _, a := range agents {
		if agentdir.IsCanonicalAt(a, scope) && copyExists {
			names = append(names, a.Name)
		}
	}
	names = append(names, scanLinkNames(home, id, agents, scope, base)...)
	return dedupeStrings(names)
}

// vendorRemove deletes skill id's install at (scope, base): every supplied
// agent's link into the canonical copy, any legacy skillm symlink occupying
// the canonical slot itself, and — when removeCopy is true, i.e. the install
// is recorded so the directory there is skillm's own — the copy. Foreign
// entries are never touched (link refusals are warned about); a missing copy
// is a no-op. It returns whether a copy was removed.
func vendorRemove(home, id string, agents []agentdir.Agent, scope agentdir.Scope, base string, removeCopy bool, label string) (removedCopy bool, err error) {
	res, lerr := linker.Unlink(home, id, agents, scope, base)
	if lerr != nil {
		ui.Warnf("%v", lerr)
	}
	for _, ar := range res.Agents {
		if ar.Action == linker.ActionRemoved {
			ui.Successf("unlinked %s from %s (%s)", id, ar.Agent.Name, label)
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

// dedupeStrings returns s with duplicates removed, preserving first-seen order.
func dedupeStrings(s []string) []string {
	seen := make(map[string]bool, len(s))
	out := make([]string, 0, len(s))
	for _, v := range s {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
