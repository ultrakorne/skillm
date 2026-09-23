package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ultrakorne/skillm/internal/agentdir"
	"github.com/ultrakorne/skillm/internal/core"
	"github.com/ultrakorne/skillm/internal/linker"
	"github.com/ultrakorne/skillm/internal/state"
	"github.com/ultrakorne/skillm/internal/ui"
)

func init() {
	rootCmd.AddCommand(newListCmd())
}

func newListCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "list",
		Short: "Show every skill in Home",
		Long: "List shows every skill registered in Home together with its source, its " +
			"kind (git or local), and the scopes and agents it is currently linked to " +
			"(read live from disk). It is fully offline and fast; run `skillm check` to " +
			"see which git skills have upstream updates.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runList()
		},
	}
	return c
}

// runList builds and renders the `skillm list` table. It is fully offline: it
// reports each skill's kind, not its upstream update status (see `skillm check`).
func runList() error {
	opts, err := coreOptions(true)
	if err != nil {
		return err
	}
	res, err := core.List(opts)
	if err != nil {
		return err
	}

	// list stays fast and offline: it reports each skill's kind (git or local),
	// which is free to derive, and never touches the network. Upstream update
	// status (up-to-date / update available / untracked) is the job of
	// `skillm check`, which fetches each git skill's ref.
	rows := make([]ui.Row, 0, len(res.Skills))
	for _, s := range res.Skills {
		rows = append(rows, ui.Row{
			ID:        s.ID,
			Source:    s.SourceLabel(),
			Installed: installedLabel(s.Installs, opts.Cwd),
			Kind:      s.Kind,
		})
	}

	fmt.Fprintln(os.Stdout, ui.RenderSkillTable(rows))
	return nil
}

// installedLabel renders the Installed column from a skill's installs:
// "global: a,b; local: a; local(/proj): b", where the bare "local" is the
// install in cwd. Installs serving no agent are left out, and a skill
// installed nowhere renders as "-".
func installedLabel(installs []core.Install, cwd string) string {
	cwdAbs, err := filepath.Abs(cwd)
	if err != nil {
		cwdAbs = cwd
	}
	var parts []string
	for _, in := range installs {
		if len(in.Agents) == 0 {
			continue
		}
		label := "global"
		if in.Scope == core.ScopeLocal {
			label = "local"
			if in.Root != cwdAbs {
				label = fmt.Sprintf("local(%s)", in.Root)
			}
		}
		parts = append(parts, label+": "+strings.Join(in.Agents, ","))
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, "; ")
}

// scanLinkNames returns the sorted names of the enabled agents that have a
// skillm link to skill id at the given scope and base directory (dir is ignored
// for Global scope). A scan error yields no names rather than failing the row.
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

// localScanDirs returns the absolute local directories to inspect for links:
// the current directory plus every tracked root, de-duplicated and sorted. cwd
// is always included so links in the current folder — or made before roots were
// tracked — are still found.
func localScanDirs(roots []string, cwd string) []string {
	seen := make(map[string]bool)
	var out []string
	add := func(p string) {
		abs, err := filepath.Abs(p)
		if err != nil {
			abs = p
		}
		if !seen[abs] {
			seen[abs] = true
			out = append(out, abs)
		}
	}
	add(cwd)
	for _, r := range roots {
		add(r)
	}
	sort.Strings(out)
	return out
}

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
			if !hasCopy[root] && core.LocalCopyExists(home, e.ID, root) {
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
		localAgents, _ := splitLocalAliased(agents, root)
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
		if st.Skills[i].Global && !core.CopyExists(home, st.Skills[i].ID, agentdir.Global, "") {
			st.Skills[i].Global = false
			changed = true
		}
		roots := st.Skills[i].VendoredAt
		if len(roots) == 0 {
			continue
		}
		kept := make([]string, 0, len(roots))
		for _, root := range roots {
			if !core.LocalCopyExists(home, st.Skills[i].ID, root) {
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
