package cmd

import (
	"fmt"
	"os"

	"github.com/ultrakorne/skillm/internal/agentdir"
	"github.com/ultrakorne/skillm/internal/linker"
	"github.com/ultrakorne/skillm/internal/store"
)

// Vendoring orchestration shared by install, add, update, uninstall, list, and
// agent. A Vendored copy is a real, committed copy of a skill written into an
// agent's project skill folder instead of a symlink into Home (see CONTEXT
// "Vendored copy"). Unlike a symlink it cannot be re-discovered by a live disk
// scan, so the project roots that hold copies are recorded per skill in
// state.toml (SkillEntry.VendoredAt). These helpers turn that record plus a
// fresh classification of what is on disk into the concrete copy/remove actions.

// vendorAction is what vendorApply did for one agent's target at one root.
type vendorAction int

const (
	// vendorWrote: a copy was written where nothing existed.
	vendorWrote vendorAction = iota
	// vendorConverted: skillm's own symlink was replaced by a copy.
	vendorConverted
	// vendorRefreshed: an existing recorded copy was overwritten with Home's content.
	vendorRefreshed
	// vendorAdopted: a foreign file/dir/symlink was overwritten under --force/confirm.
	vendorAdopted
	// vendorBlocked: a foreign entry was left untouched (no force/confirm).
	vendorBlocked
)

// vendorOutcome records what happened for one agent at one root.
type vendorOutcome struct {
	Agent  agentdir.Agent
	Path   string
	Action vendorAction
}

// touched reports whether a copy now exists as a result of this outcome.
func (o vendorOutcome) touched() bool {
	return o.Action != vendorBlocked
}

// vendorTarget is one agent's copy path at a base, for agents that have a
// distinct local skill folder there.
type vendorTarget struct {
	Agent agentdir.Agent
	Path  string
}

// vendorTargets computes the copy path (<base>/<agent.local>/<id>) for each
// agent that defines a local folder distinct from its global one at base.
// Agents whose local scope aliases their global folder at base (e.g. base ==
// home) are skipped: a "copy" there would write into the global folder. Callers
// generally pass already-real agents, but the guard keeps the helper safe.
func vendorTargets(agents []agentdir.Agent, base, id string) []vendorTarget {
	real, _ := splitLocalAliased(agents, base)
	out := make([]vendorTarget, 0, len(real))
	for _, a := range real {
		path, ok := agentdir.LinkPath(a, agentdir.Local, base, id)
		if !ok {
			continue
		}
		out = append(out, vendorTarget{Agent: a, Path: path})
	}
	return out
}

// vendorConflicts returns the foreign target paths a vendor install at base
// would have to overwrite: real files/dirs or foreign symlinks skillm did not
// create. When base is already a recorded vendored root for the skill, every
// directory there is skillm's own copy, so there are no conflicts. skillm's own
// symlink (which a copy install simply converts) is never a conflict.
func vendorConflicts(home, id string, agents []agentdir.Agent, base string, recorded bool) []string {
	if recorded {
		return nil
	}
	var foreign []string
	for _, t := range vendorTargets(agents, base, id) {
		kind, _, err := linker.Classify(home, t.Path)
		if err != nil {
			foreign = append(foreign, t.Path)
			continue
		}
		switch kind {
		case linker.TargetDir, linker.TargetForeignLink, linker.TargetFile:
			foreign = append(foreign, t.Path)
		}
	}
	return foreign
}

// vendorApply writes (or refreshes) a Vendored copy of skill id from Home into
// each agent's project skill folder at base. Per target:
//
//   - absent           → write the copy (vendorWrote);
//   - skillm's symlink  → remove it and write the copy (vendorConverted);
//   - a recorded copy   → overwrite with Home's content (vendorRefreshed);
//   - a foreign entry   → overwrite only if force (vendorAdopted), else leave
//     everything at this base untouched (see atomicity below).
//
// recorded says whether base is already a vendored root for this skill (so a
// directory there is skillm's own copy, not foreign). The Home copy is the
// source; ReplaceDir stages then swaps so a failure never destroys a target.
//
// Atomicity: a vendor at one base is all-or-nothing across its agents under
// !force. If ANY target is a foreign conflict, vendorApply writes nothing and
// reports every target as vendorBlocked. This is essential because base is
// recorded as a single vendored root: if it wrote the clean slots and the caller
// recorded the root while one slot stayed a skipped foreign directory, a later
// update/uninstall/convert would treat that foreign dir as skillm's own and
// overwrite or delete it without a prompt. Refusing the whole base keeps the
// invariant "a recorded root's agent slots are all skillm's" intact.
func vendorApply(home, id string, agents []agentdir.Agent, base string, recorded, force bool) ([]vendorOutcome, error) {
	src := store.SkillDir(home, id)
	targets := vendorTargets(agents, base, id)

	kinds := make([]linker.TargetKind, len(targets))
	for i, t := range targets {
		k, _, err := linker.Classify(home, t.Path)
		if err != nil {
			return nil, err
		}
		kinds[i] = k
	}

	// All-or-nothing: any unforced foreign conflict blocks the entire base.
	if !force {
		for _, k := range kinds {
			if vendorForeignConflict(k, recorded) {
				out := make([]vendorOutcome, len(targets))
				for i, t := range targets {
					out[i] = vendorOutcome{Agent: t.Agent, Path: t.Path, Action: vendorBlocked}
				}
				return out, nil
			}
		}
	}

	var outcomes []vendorOutcome
	for i, t := range targets {
		write := func(act vendorAction) error {
			if err := store.ReplaceDir(src, t.Path); err != nil {
				return fmt.Errorf("vendor %s for %s: %w", id, t.Agent.Name, err)
			}
			outcomes = append(outcomes, vendorOutcome{Agent: t.Agent, Path: t.Path, Action: act})
			return nil
		}

		switch kinds[i] {
		case linker.TargetAbsent:
			if err := write(vendorWrote); err != nil {
				return outcomes, err
			}
		case linker.TargetOurLink:
			// Auto-convert: drop skillm's own symlink, then drop a real copy in.
			if err := os.Remove(t.Path); err != nil && !os.IsNotExist(err) {
				return outcomes, fmt.Errorf("remove symlink %s: %w", t.Path, err)
			}
			if err := write(vendorConverted); err != nil {
				return outcomes, err
			}
		case linker.TargetDir:
			// A recorded copy is refreshed; an unrecorded dir reaching here must be
			// forced (the !force foreign case was handled atomically above).
			if recorded {
				if err := write(vendorRefreshed); err != nil {
					return outcomes, err
				}
			} else {
				if err := write(vendorAdopted); err != nil {
					return outcomes, err
				}
			}
		case linker.TargetForeignLink, linker.TargetFile:
			// Reachable only under force; otherwise blocked atomically above.
			if err := os.Remove(t.Path); err != nil && !os.IsNotExist(err) {
				return outcomes, fmt.Errorf("remove %s: %w", t.Path, err)
			}
			if err := write(vendorAdopted); err != nil {
				return outcomes, err
			}
		}
	}
	return outcomes, nil
}

// vendorForeignConflict reports whether a target of the given kind is a foreign
// entry that a vendor install must not overwrite without force: an unrecorded
// real directory (an unrecorded root's dir is not known to be skillm's), a
// foreign symlink, or a real file. An absent slot and skillm's own symlink are
// never conflicts; a directory at a recorded root is skillm's own copy.
func vendorForeignConflict(k linker.TargetKind, recorded bool) bool {
	switch k {
	case linker.TargetDir:
		return !recorded
	case linker.TargetForeignLink, linker.TargetFile:
		return true
	default:
		return false
	}
}

// vendorScan returns the agents that currently have a real-directory Vendored
// copy of skill id at base — used by `list` and by the reconcile that prunes
// emptied vendored roots. A symlink or absent target does not count.
func vendorScan(home, id string, agents []agentdir.Agent, base string) []agentdir.Agent {
	var have []agentdir.Agent
	for _, t := range vendorTargets(agents, base, id) {
		kind, _, err := linker.Classify(home, t.Path)
		if err == nil && kind == linker.TargetDir {
			have = append(have, t.Agent)
		}
	}
	return have
}

// vendorRemove deletes the Vendored copies of skill id at base for the supplied
// agents. It removes only real directories (a recorded copy); a symlink at the
// path is left for the symlink-unlink pass, and an absent target is a no-op. It
// returns the agents whose copy was removed and the first error encountered.
func vendorRemove(home, id string, agents []agentdir.Agent, base string) ([]agentdir.Agent, error) {
	var removed []agentdir.Agent
	for _, t := range vendorTargets(agents, base, id) {
		kind, _, err := linker.Classify(home, t.Path)
		if err != nil {
			return removed, err
		}
		if kind != linker.TargetDir {
			continue
		}
		if err := os.RemoveAll(t.Path); err != nil {
			return removed, fmt.Errorf("remove vendored copy %s: %w", t.Path, err)
		}
		removed = append(removed, t.Agent)
	}
	return removed, nil
}

// dedupeStrings returns s with duplicates removed, preserving first-seen order.
// Used to tidy the list of "places" an enable/disable touched before joining it
// for a report line.
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

// vendorActionLabel renders a vendorAction as a short past-tense verb for the
// per-agent report lines.
func vendorActionLabel(a vendorAction) string {
	switch a {
	case vendorWrote, vendorAdopted:
		return "copied"
	case vendorConverted:
		return "converted to copy"
	case vendorRefreshed:
		return "refreshed copy"
	default:
		return ""
	}
}
