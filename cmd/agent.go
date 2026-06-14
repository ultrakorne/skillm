package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

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
		Long: "agent shows an interactive multiselect of the agents defined in " +
			"config.toml, seeded with the currently enabled set, and writes the toggled " +
			"enabled flags back (each agent's locations are preserved). Defining a new " +
			"agent is a config edit, not something this command does. It does not " +
			"retroactively link or unlink existing skills; the change only affects future links.",
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

	// The option set is every agent defined in config (sorted by name); the
	// picker is seeded with those currently enabled.
	all := cfg.AgentNames()

	// SelectAgents seeds the picker from the currently enabled set and returns
	// the new selection. It refuses on a non-TTY with a message naming
	// config.toml, satisfying the non-interactive contract for this command.
	selection, err := ui.SelectAgents(all, cfg.EnabledNames())
	if err != nil {
		return err
	}

	// Write the selection back as per-agent enabled flags, keeping each agent's
	// locations intact.
	cfg.SetEnabled(selection)
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
