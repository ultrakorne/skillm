package cmd

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ultrakorne/skillm/internal/agentdir"
	"github.com/ultrakorne/skillm/internal/core"
	"github.com/ultrakorne/skillm/internal/linker"
	"github.com/ultrakorne/skillm/internal/protocol"
	"github.com/ultrakorne/skillm/internal/ui"
)

func init() {
	rootCmd.AddCommand(newListCmd())
}

func newListCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "list",
		Short: "Show every skill in Home",
		Long: "List shows every skill registered in Home together with its source and " +
			"where it is currently installed: global, or the project path (read live " +
			"from disk). It is fully offline and fast; run `skillm check` to " +
			"see which git skills have upstream updates.",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{annotationJSON: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runList()
		},
	}
	return c
}

// runList builds and renders the `skillm list` table. It is fully offline:
// upstream update status is the job of `skillm check`.
func runList() error {
	opts, err := coreOptions(true)
	if err != nil {
		return err
	}
	res, err := core.List(opts)
	if err != nil {
		return err
	}
	if flagJSON {
		return jsonOut().Result(protocol.NewListData(res))
	}

	// list stays fast and offline and never touches the network. Upstream
	// update status (up-to-date / update available / untracked) is the job of
	// `skillm check`, which fetches each git skill's ref.
	rows := make([]ui.Row, 0, len(res.Skills))
	for _, s := range res.Skills {
		rows = append(rows, ui.Row{
			ID:        s.ID,
			Source:    s.SourceLabel(),
			Installed: installedLabel(s.Installs),
		})
	}

	fmt.Fprintln(os.Stdout, ui.RenderSkillTable(rows))
	return nil
}

// installedLabel renders the Installed column from a skill's installs:
// "global; /proj/a; /proj/b", naming each local install by its project root.
// Installs serving no agent are left out, and a skill installed nowhere
// renders as "-".
func installedLabel(installs []core.Install) string {
	var parts []string
	for _, in := range installs {
		if len(in.Agents) == 0 {
			continue
		}
		label := "global"
		if in.Scope == core.ScopeLocal {
			label = in.Root
		}
		parts = append(parts, label)
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
