package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ultrakorne/skillm/internal/agentdir"
	"github.com/ultrakorne/skillm/internal/config"
	"github.com/ultrakorne/skillm/internal/store"
	"github.com/ultrakorne/skillm/internal/ui"
)

func init() {
	rootCmd.AddCommand(newAgentCmd())
}

func newAgentCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "agent",
		Short: "Choose which agents skillm links skills into",
		Long: "agent shows an interactive multiselect of the supported agents, seeded " +
			"with the currently enabled set, and writes the new selection back to " +
			"config.toml. It does not retroactively link or unlink existing skills; the " +
			"change only affects future links.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgent()
		},
	}
	return c
}

func runAgent() error {
	home, err := store.Home(flagHome)
	if err != nil {
		return err
	}

	cfg, err := config.Load(home)
	if err != nil {
		return err
	}

	// Supported agents in stable registry order; this is the full option set.
	supported := agentdir.All()
	all := make([]string, 0, len(supported))
	for _, a := range supported {
		all = append(all, a.Name)
	}

	// SelectAgents seeds the picker from the currently enabled set and returns
	// the new selection. It refuses on a non-TTY with a message naming
	// config.toml, satisfying the non-interactive contract for this command.
	selection, err := ui.SelectAgents(all, cfg.Agents)
	if err != nil {
		return err
	}

	cfg.Agents = selection
	if err := config.Save(home, cfg); err != nil {
		return err
	}

	if len(selection) == 0 {
		ui.Warnf("no agents enabled; skillm will link skills into nothing until you enable one")
	} else {
		ui.Successf("enabled agents: %s", strings.Join(selection, ", "))
	}
	// The change is forward-looking only — existing links are untouched.
	fmt.Println("(existing links are unchanged; this only affects future links)")

	return nil
}
