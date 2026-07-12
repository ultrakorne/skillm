package cmd

import (
	"fmt"
	"os"

	"github.com/ultrakorne/skillm/internal/agentdir"
	"github.com/ultrakorne/skillm/internal/linker"
	"github.com/ultrakorne/skillm/internal/store"
	"github.com/ultrakorne/skillm/internal/ui"
)

// Local-install orchestration shared by install, update, uninstall, list, and
// agent. A Local install writes a real, committable copy of the skill into the
// project's canonical store, <base>/.agents/skills/<id> — the cross-agent
// convention read natively by Codex, Cursor, Amp, Gemini CLI and friends, and
// the same layout vercel's skills CLI produces — plus a RELATIVE symlink into
// that copy for every enabled agent whose local folder is elsewhere (e.g.
// .claude/skills/<id> -> ../../.agents/skills/<id>). Copy, links, and the
// skills-lock.json entry written alongside (see locksync.go) are all
// committable, so teammates get working skills on clone with no tooling.
//
// A real-directory copy cannot be re-discovered by a live link scan, so the
// project roots holding one are recorded per skill in state.toml
// (SkillEntry.VendoredAt); the invariant is that a recorded root's canonical
// slot is skillm's own copy.

// localAction is what localInstallOne did to the canonical slot at one root.
type localAction int

const (
	// localWrote: a copy was written where nothing existed.
	localWrote localAction = iota
	// localConverted: a legacy skillm symlink into Home was replaced by a copy.
	localConverted
	// localRefreshed: an existing recorded copy was overwritten with Home's content.
	localRefreshed
	// localAdopted: a foreign file/dir/symlink was overwritten under --force/confirm.
	localAdopted
	// localBlocked: a foreign entry was left untouched (no force/confirm).
	localBlocked
)

// localActionLabel renders a localAction as a short past-tense verb for report
// lines.
func localActionLabel(a localAction) string {
	switch a {
	case localWrote, localAdopted:
		return "installed"
	case localConverted:
		return "converted to copy"
	case localRefreshed:
		return "refreshed"
	default:
		return ""
	}
}

// localConflict returns the canonical slot's path when a local install of id at
// base would overwrite something skillm did not create: a real file, a foreign
// symlink, or an unrecorded real directory. When base is already a recorded
// root for the skill, the directory there is skillm's own copy — no conflict.
// A legacy skillm symlink into Home is never a conflict (it is converted).
func localConflict(home, id, base string, recorded bool) string {
	slot := agentdir.CanonicalSkillDir(base, id)
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

// localInstallOne materializes skill id's Local install at base: the canonical
// copy from Home, then a relative link for every supplied agent that needs one.
// recorded says whether base is already a recorded root for this skill (so a
// directory at the canonical slot is skillm's own copy); force permits
// overwriting a foreign entry there; label names the scope in per-link report
// lines. It returns what happened to the canonical slot; localBlocked means
// nothing was written at all. Link refusals (a foreign entry at an agent's link
// path) are warned about, never fatal: the copy is the unit that is recorded,
// links are re-derivable from disk.
func localInstallOne(home, id string, agents []agentdir.Agent, base string, recorded, force bool, label string) (localAction, error) {
	src := store.SkillDir(home, id)
	slot := agentdir.CanonicalSkillDir(base, id)

	kind, _, err := linker.Classify(home, slot)
	if err != nil {
		return localBlocked, err
	}

	action := localWrote
	switch kind {
	case linker.TargetAbsent:
	case linker.TargetOurLink:
		// A legacy absolute symlink into Home from before local installs were
		// vendored: drop it, then write the real copy in its place.
		if err := os.Remove(slot); err != nil && !os.IsNotExist(err) {
			return localBlocked, fmt.Errorf("remove legacy symlink %s: %w", slot, err)
		}
		action = localConverted
	case linker.TargetDir:
		if recorded {
			action = localRefreshed
		} else if force {
			action = localAdopted
		} else {
			return localBlocked, nil
		}
	case linker.TargetForeignLink, linker.TargetFile:
		if !force {
			return localBlocked, nil
		}
		if err := os.Remove(slot); err != nil && !os.IsNotExist(err) {
			return localBlocked, fmt.Errorf("remove %s: %w", slot, err)
		}
		action = localAdopted
	}

	// ReplaceDir stages the new content before touching the slot, so a failure
	// never destroys an existing copy.
	if err := store.ReplaceDir(src, slot); err != nil {
		return localBlocked, fmt.Errorf("install copy of %s: %w", id, err)
	}

	linkLocalAgents(home, id, agents, base, label)
	return action, nil
}

// linkLocalAgents creates (or repoints) the relative agent links into the
// canonical copy of id at base for every supplied agent, warning on refusals
// instead of failing — a foreign file at one agent's link path must not block
// the others.
func linkLocalAgents(home, id string, agents []agentdir.Agent, base, label string) {
	for _, a := range agents {
		res, err := linker.Link(home, id, []agentdir.Agent{a}, agentdir.Local, base)
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

// localCopyExists reports whether the canonical slot for id at base currently
// holds a real directory (skillm's copy or otherwise — the caller decides via
// the recorded roots).
func localCopyExists(home, id, base string) bool {
	kind, _, err := linker.Classify(home, agentdir.CanonicalSkillDir(base, id))
	return err == nil && kind == linker.TargetDir
}

// localServedAgents returns the names of the supplied agents that skill id's
// Local install at base currently serves, read live from disk: agents whose
// local folder IS the canonical store when the copy exists, plus agents holding
// a skillm-owned link there. Sorted by the caller if needed.
func localServedAgents(home, id string, agents []agentdir.Agent, base string) []string {
	copyExists := localCopyExists(home, id, base)
	var names []string
	for _, a := range agents {
		if agentdir.IsCanonicalLocal(a) {
			if copyExists {
				names = append(names, a.Name)
			}
			continue
		}
	}
	names = append(names, scanLinkNames(home, id, agents, agentdir.Local, base)...)
	return dedupeStrings(names)
}

// localRemove deletes skill id's Local install at base: every supplied agent's
// link into the canonical copy, then the copy itself. Foreign entries are never
// touched (link refusals are warned about); a missing copy is a no-op. It
// returns whether a copy was removed.
func localRemove(home, id string, agents []agentdir.Agent, base string) (removedCopy bool, err error) {
	res, lerr := linker.Unlink(home, id, agents, agentdir.Local, base)
	if lerr != nil {
		ui.Warnf("%v", lerr)
	}
	for _, ar := range res.Agents {
		if ar.Action == linker.ActionRemoved {
			ui.Successf("unlinked %s from %s (%s)", id, ar.Agent.Name, base)
		}
	}

	if !localCopyExists(home, id, base) {
		return false, nil
	}
	if rerr := os.RemoveAll(agentdir.CanonicalSkillDir(base, id)); rerr != nil {
		return false, fmt.Errorf("remove copy %s: %w", agentdir.CanonicalSkillDir(base, id), rerr)
	}
	return true, nil
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
