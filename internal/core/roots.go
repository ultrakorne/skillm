package core

import (
	"github.com/ultrakorne/skillm/internal/agentdir"
	"github.com/ultrakorne/skillm/internal/linker"
	"github.com/ultrakorne/skillm/internal/state"
)

// reconcileLocalRoots prunes tracked local roots that hold neither any of
// skillm's links nor any skill's recorded canonical copy — because everything
// there was removed, or the directory is gone — mutating st.LocalRoots in
// place. It scans across ALL supported agents (not just the enabled ones) so a
// root with links for a currently-disabled agent is kept. It returns true if
// it changed st; the caller persists via state.Save.
func reconcileLocalRoots(home string, agents []agentdir.Agent, st *state.State) bool {
	if len(st.LocalRoots) == 0 {
		return false
	}
	// Roots where some skill's recorded copy still exists stay tracked even
	// with zero links (e.g. when only .agents-native agents are enabled).
	hasCopy := make(map[string]bool)
	for _, e := range st.Skills {
		for _, root := range e.VendoredAt {
			if !hasCopy[root] && LocalCopyExists(home, e.ID, root) {
				hasCopy[root] = true
			}
		}
	}

	kept := make([]string, 0, len(st.LocalRoots))
	changed := false
	for _, root := range st.LocalRoots {
		if hasCopy[root] {
			kept = append(kept, root)
			continue
		}
		// Only agents with a real local scope at root count toward keeping it.
		// A legacy root that aliases global for every agent (e.g. home) exposes
		// only global links there and must be pruned, not kept alive by them.
		localAgents, _ := SplitLocalAliased(agents, root)
		infos, err := linker.ScanAll(home, localAgents, agentdir.Local, root)
		if err == nil && len(infos) == 0 {
			changed = true
			continue
		}
		kept = append(kept, root)
	}
	st.LocalRoots = kept
	return changed
}

// reconcileVendoredRoots prunes each skill's recorded installs whose canonical
// copy no longer exists — because the files were deleted or the project moved
// away. That covers the Global install (~/.agents/skills) as well as the
// recorded project roots. It mutates st in place and returns true if anything
// changed; the caller persists via state.Save.
func reconcileVendoredRoots(home string, st *state.State) bool {
	changed := false
	for i := range st.Skills {
		if st.Skills[i].Global && !CopyExists(home, st.Skills[i].ID, agentdir.Global, "") {
			st.Skills[i].Global = false
			changed = true
		}
		roots := st.Skills[i].VendoredAt
		if len(roots) == 0 {
			continue
		}
		kept := make([]string, 0, len(roots))
		for _, root := range roots {
			if !LocalCopyExists(home, st.Skills[i].ID, root) {
				changed = true
				continue
			}
			kept = append(kept, root)
		}
		if len(kept) == 0 {
			st.Skills[i].VendoredAt = nil
		} else {
			st.Skills[i].VendoredAt = kept
		}
	}
	return changed
}
