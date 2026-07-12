package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ultrakorne/skillm/internal/agentdir"
	"github.com/ultrakorne/skillm/internal/config"
	"github.com/ultrakorne/skillm/internal/gitx"
	"github.com/ultrakorne/skillm/internal/linker"
	"github.com/ultrakorne/skillm/internal/state"
	"github.com/ultrakorne/skillm/internal/store"
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
	home, err := store.Home(flagHome)
	if err != nil {
		return err
	}

	cfg, err := config.Load(home)
	if err != nil {
		return err
	}
	st, err := state.Load(home)
	if err != nil {
		return err
	}

	agents := cfg.EnabledAgents()

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("determine working directory: %w", err)
	}

	// list stays fast and offline: it reports each skill's kind (git or local),
	// which is free to derive, and never touches the network. Upstream update
	// status (up-to-date / update available / untracked) is the job of
	// `skillm check`, which fetches each git skill's ref.
	rows := make([]ui.Row, 0, len(st.Skills))
	for _, e := range st.Skills {
		rows = append(rows, ui.Row{
			ID:        e.ID,
			Source:    sourceLabel(e),
			Installed: linkedLabel(home, e.ID, agents, st.LocalRoots, e.VendoredAt, cwd),
			Kind:      e.Kind,
		})
	}

	fmt.Fprintln(os.Stdout, ui.RenderSkillTable(rows))
	return nil
}

// Status labels for `skillm check`. statusUpToDate is the default for a git
// skill whose upstream subdir SHA still matches the recorded revision.
const (
	statusUpToDate        = "up-to-date"
	statusUpdateAvailable = "update available"
	statusUntracked       = "untracked"
)

// upstreamStatus determines the current update status of a single git skill by
// treeless-fetching its pinned ref into a temp clone and comparing the skill
// subdir's tree SHA against the recorded revision. The clone is discarded; the
// operation changes nothing in Home. A subdir that has vanished upstream (or any
// other lookup failure) is reported as untracked rather than aborting the whole
// listing.
func upstreamStatus(ctx context.Context, e state.SkillEntry) string {
	cur, err := currentRevision(ctx, e)
	if err != nil {
		return statusUntracked
	}
	if cur == e.Revision {
		return statusUpToDate
	}
	return statusUpdateAvailable
}

// currentRevision treeless-clones e's source at its pinned ref into a temporary
// directory and returns the current git tree SHA of the skill's subdir. The
// temporary clone is always removed before returning.
func currentRevision(ctx context.Context, e state.SkillEntry) (string, error) {
	tmp, err := os.MkdirTemp("", "skillm-check-")
	if err != nil {
		return "", fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmp)

	// git clone creates the destination itself; hand it a non-existent child.
	dest := filepath.Join(tmp, "repo")
	if err := gitx.TreelessClone(ctx, e.Source, e.Ref, dest); err != nil {
		return "", err
	}

	ref := e.Ref
	if ref == "" {
		// No pinned ref: compare against the repository's default branch tip.
		ref, err = gitx.DefaultRef(ctx, dest)
		if err != nil {
			return "", err
		}
	}

	sha, err := gitx.SubtreeSHA(ctx, dest, ref, e.Path)
	if err != nil {
		return "", err
	}
	return sha, nil
}

// sourceLabel renders the Source column: the git URL (with subpath appended for
// catalog repos) or the local origin path.
func sourceLabel(e state.SkillEntry) string {
	if e.Kind == state.KindGit && e.Path != "" {
		return e.Source + "//" + e.Path
	}
	return e.Source
}

// linkedLabel scans, live from disk, where skill id is installed for the
// enabled agents and renders a compact summary: "global: a,b; local: a;
// local(/proj): b". Global links are read from the agents' user-level folders.
// Local installs are looked for in the current directory, every tracked root,
// and every recorded install root: an agent is served locally when it holds a
// skillm link there or — for the canonical .agents/skills agent — when the
// recorded copy exists. The canonical copy is only counted at roots recorded in
// vendoredRoots, so a foreign directory in the current folder is never mistaken
// for skillm's install. A skill installed nowhere renders as "-".
func linkedLabel(home, id string, agents []agentdir.Agent, roots, vendoredRoots []string, cwd string) string {
	var parts []string

	if names := scanLinkNames(home, id, agents, agentdir.Global, ""); len(names) > 0 {
		parts = append(parts, "global: "+strings.Join(names, ","))
	}

	cwdAbs, err := filepath.Abs(cwd)
	if err != nil {
		cwdAbs = cwd
	}
	recorded := make(map[string]bool)
	for _, dir := range uniqueAbs(vendoredRoots) {
		recorded[dir] = true
	}
	for _, dir := range localScanDirs(append(append([]string{}, roots...), vendoredRoots...), cwd) {
		// Skip agents whose local folder aliases their global one at dir, so a
		// global link (e.g. when dir is home) is never also rendered as local.
		localAgents, _ := splitLocalAliased(agents, dir)
		var names []string
		if recorded[dir] {
			names = localServedAgents(home, id, localAgents, dir)
		} else {
			names = scanLinkNames(home, id, localAgents, agentdir.Local, dir)
		}
		if len(names) == 0 {
			continue
		}
		sort.Strings(names)
		label := "local"
		if dir != cwdAbs {
			label = fmt.Sprintf("local(%s)", dir)
		}
		parts = append(parts, label+": "+strings.Join(names, ","))
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

// uniqueAbs returns roots as absolute, de-duplicated, sorted paths. Unlike
// localScanDirs it does NOT inject the current directory — vendored copies are
// read only from roots that were explicitly recorded, so a stray directory in
// cwd is never mistaken for a managed copy.
func uniqueAbs(roots []string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, r := range roots {
		abs, err := filepath.Abs(r)
		if err != nil {
			abs = r
		}
		if !seen[abs] {
			seen[abs] = true
			out = append(out, abs)
		}
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
			if !hasCopy[root] && localCopyExists(home, e.ID, root) {
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

// reconcileVendoredRoots prunes each skill's recorded install roots whose
// canonical copy no longer exists — because the files were deleted or the
// project moved away. It mutates st in place and returns true if anything
// changed; the caller persists via state.Save.
func reconcileVendoredRoots(home string, st *state.State) bool {
	changed := false
	for i := range st.Skills {
		roots := st.Skills[i].VendoredAt
		if len(roots) == 0 {
			continue
		}
		kept := make([]string, 0, len(roots))
		for _, root := range roots {
			if !localCopyExists(home, st.Skills[i].ID, root) {
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
