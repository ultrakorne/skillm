package core

import (
	"sort"

	"github.com/ultrakorne/skillm/internal/agentdir"
	"github.com/ultrakorne/skillm/internal/linker"
)

// Read-only disk scans behind List. cmd still holds its own copies of these
// helpers for the commands that have not moved into core yet; each copy goes
// away when its last caller moves (A3–A6 of docs/plans/macos-menubar.md).

// copyExists reports whether the canonical slot for id at (scope, base) holds
// a real directory (skillm's copy or otherwise — the caller decides via the
// recorded installs).
func copyExists(home, id string, scope agentdir.Scope, base string) bool {
	kind, _, err := linker.Classify(home, agentdir.CanonicalSkillDirAt(scope, base, id))
	return err == nil && kind == linker.TargetDir
}

// servedAgents returns the names of the agents that skill id's install at
// (scope, base) serves: agents whose folder at the scope IS the canonical
// store when the copy exists, plus agents holding a skillm link there.
func servedAgents(home, id string, agents []agentdir.Agent, scope agentdir.Scope, base string) []string {
	exists := copyExists(home, id, scope, base)
	var names []string
	for _, a := range agents {
		if agentdir.IsCanonicalAt(a, scope) && exists {
			names = append(names, a.Name)
		}
	}
	names = append(names, scanLinkNames(home, id, agents, scope, base)...)
	return dedupe(names)
}

// scanLinkNames returns the sorted names of the agents that have a skillm
// link to skill id at the given scope and base directory (dir is ignored for
// Global scope). A scan error yields no names rather than failing the caller.
func scanLinkNames(home, id string, agents []agentdir.Agent, scope agentdir.Scope, dir string) []string {
	res, err := linker.ScanLinks(home, id, agents, scope, dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, ar := range res.Agents {
		if ar.Action == linker.ActionFound {
			names = append(names, ar.Agent.Name)
		}
	}
	sort.Strings(names)
	return names
}

// nonAliasedAt returns the agents whose local folder at base is not their
// global folder.
func nonAliasedAt(agents []agentdir.Agent, base string) []agentdir.Agent {
	var out []agentdir.Agent
	for _, a := range agents {
		if !agentdir.LocalAliasesGlobal(a, base) {
			out = append(out, a)
		}
	}
	return out
}

// localScanDirs returns the absolute local directories to inspect for links:
// cwd (when non-empty) plus every root, de-duplicated and sorted. cwd is
// included so links in the current folder — or made before roots were
// tracked — are still found.
func localScanDirs(roots []string, cwd string) []string {
	if cwd != "" {
		roots = append([]string{cwd}, roots...)
	}
	return uniqueAbs(roots)
}

// uniqueAbs returns roots as absolute, de-duplicated, sorted paths.
func uniqueAbs(roots []string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, r := range roots {
		abs := absOr(r)
		if !seen[abs] {
			seen[abs] = true
			out = append(out, abs)
		}
	}
	sort.Strings(out)
	return out
}

// dedupe returns s with duplicates removed, preserving first-seen order.
func dedupe(s []string) []string {
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
