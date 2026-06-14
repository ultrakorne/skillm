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
		Long: "List shows every skill registered in Home together with its source, the " +
			"scopes and agents it is currently linked to (read live from disk), and its " +
			"update status (up-to-date, update available, local, or untracked).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runList(cmd.Context())
		},
	}
	return c
}

// runList builds and renders the `skillm list` table.
func runList(ctx context.Context) error {
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

	// Resolve the upstream status of every git skill (one treeless fetch each).
	// Local skills need no network work. The check is read-only.
	statuses := make([]string, len(st.Skills))
	var gitIdx []int
	for i, e := range st.Skills {
		if e.Kind == state.KindGit {
			gitIdx = append(gitIdx, i)
		} else {
			statuses[i] = statusLocal
		}
	}

	if len(gitIdx) > 0 {
		work := func(report func(done int)) error {
			for n, i := range gitIdx {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
				}
				statuses[i] = upstreamStatus(ctx, st.Skills[i])
				report(n + 1)
			}
			return nil
		}
		if err := ui.RunProgress(len(gitIdx), work); err != nil {
			return err
		}
	}

	rows := make([]ui.Row, 0, len(st.Skills))
	for i, e := range st.Skills {
		rows = append(rows, ui.Row{
			ID:     e.ID,
			Source: sourceLabel(e),
			Linked: linkedLabel(home, e.ID, agents, cwd),
			Status: statuses[i],
		})
	}

	fmt.Fprintln(os.Stdout, ui.RenderSkillTable(rows))
	return nil
}

// Status labels for the list/check output. statusUpToDate is the default for a
// git skill whose upstream subdir SHA still matches the recorded revision.
const (
	statusUpToDate        = "up-to-date"
	statusUpdateAvailable = "update available"
	statusLocal           = "local"
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
// id at each scope and renders a compact "global: a,b; local: a" summary. A
// skill that is linked nowhere renders as "-".
func linkedLabel(home, id string, agents []agentdir.Agent, cwd string) string {
	scopes := []agentdir.Scope{agentdir.Global, agentdir.Local}
	var parts []string
	for _, scope := range scopes {
		res, err := linker.ScanLinks(home, id, agents, scope, cwd)
		if err != nil {
			continue
		}
		var names []string
		for _, ar := range res.Agents {
			if ar.Action == linker.ActionFound {
				names = append(names, ar.Agent.Name)
			}
		}
		if len(names) > 0 {
			sort.Strings(names)
			parts = append(parts, fmt.Sprintf("%s: %s", scope, strings.Join(names, ",")))
		}
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, "; ")
}
