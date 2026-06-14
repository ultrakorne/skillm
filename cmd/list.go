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

	agents := agentdir.Enabled(cfg.Agents)

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
			ID:     e.ID,
			Source: sourceLabel(e),
			Linked: linkedLabel(home, e.ID, agents, st.LocalRoots, cwd),
			Kind:   e.Kind,
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

// linkedLabel scans, live from disk, which enabled agents have a link to skill
// id and renders a compact summary: "global: a,b; local: a; local(/proj): b".
// Local links are looked for in the current directory (shown as bare "local")
// and in every tracked root (shown as "local(<path>)"). A skill linked nowhere
// renders as "-".
func linkedLabel(home, id string, agents []agentdir.Agent, roots []string, cwd string) string {
	var parts []string

	if names := scanLinkNames(home, id, agents, agentdir.Global, ""); len(names) > 0 {
		parts = append(parts, "global: "+strings.Join(names, ","))
	}

	cwdAbs, err := filepath.Abs(cwd)
	if err != nil {
		cwdAbs = cwd
	}
	for _, dir := range localScanDirs(roots, cwd) {
		names := scanLinkNames(home, id, agents, agentdir.Local, dir)
		if len(names) == 0 {
			continue
		}
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

// reconcileLocalRoots prunes tracked local roots that hold none of skillm's
// links — because every linked skill there was unlinked or removed, or the
// directory is gone — mutating st.LocalRoots in place. It scans across ALL
// supported agents (not just the enabled ones) so a root with links for a
// currently-disabled agent is kept. It returns true if it changed st; the
// caller persists via state.Save.
func reconcileLocalRoots(home string, st *state.State) bool {
	if len(st.LocalRoots) == 0 {
		return false
	}
	kept := make([]string, 0, len(st.LocalRoots))
	changed := false
	for _, root := range st.LocalRoots {
		infos, err := linker.ScanAll(home, agentdir.All(), agentdir.Local, root)
		if err == nil && len(infos) == 0 {
			changed = true
			continue
		}
		kept = append(kept, root)
	}
	st.LocalRoots = kept
	return changed
}

// pruneLocalRoots reconciles the tracked local roots and persists the result.
// It is best-effort: any error is swallowed so a failed prune never fails the
// user's command (the stale root is simply skipped by `list` next time).
func pruneLocalRoots(home string) {
	st, err := state.Load(home)
	if err != nil {
		return
	}
	if reconcileLocalRoots(home, st) {
		_ = state.Save(home, st)
	}
}
