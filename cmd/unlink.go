package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/ultrakorne/skillm/internal/agentdir"
	"github.com/ultrakorne/skillm/internal/config"
	"github.com/ultrakorne/skillm/internal/linker"
	"github.com/ultrakorne/skillm/internal/store"
	"github.com/ultrakorne/skillm/internal/ui"
)

// unlink-command scope flags (independent of the link command's flag vars).
var (
	unlinkFlagGlobal bool
	unlinkFlagLocal  bool
)

func init() {
	rootCmd.AddCommand(newUnlinkCmd())
}

func newUnlinkCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "unlink <skill_id>",
		Short: "Remove a skill's symlink from every enabled agent at the chosen scope",
		Long: "Remove the symlink for <skill_id> from every enabled agent's skill folder " +
			"(see config.agents) at the chosen scope. With no flag, skillm asks " +
			"interactively which scope to target: Global (the agents' user-level " +
			"~/.<agent>/skills folders), Local (this directory's <cwd>/.<agent>/skills " +
			"folders), or a custom directory you type with Tab path-completion. --global " +
			"or --local skip the prompt; on a non-interactive terminal one of them is " +
			"required. Only symlinks skillm created (pointing into Home) are removed; real " +
			"files, directories, or foreign symlinks are left untouched. Unlinking a skill " +
			"that is not linked is a no-op.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUnlink(args[0], unlinkFlagGlobal, unlinkFlagLocal)
		},
	}
	f := c.Flags()
	f.BoolVar(&unlinkFlagGlobal, "global", false, "unlink from the agents' user-level skill folders")
	f.BoolVar(&unlinkFlagLocal, "local", false, "unlink from the current directory's project skill folders")
	c.MarkFlagsMutuallyExclusive("global", "local")
	return c
}

func runUnlink(id string, global, local bool) error {
	home, err := store.Home(flagHome)
	if err != nil {
		return err
	}

	cfg, err := config.Load(home)
	if err != nil {
		return err
	}

	agents := cfg.EnabledAgents()
	if len(agents) == 0 {
		return fmt.Errorf("no enabled agents in %s; run `skillm agent` to enable at least one", config.Path(home))
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("determine current directory: %w", err)
	}

	scope, base, err := resolveScope(global, local, cwd)
	if err != nil {
		return err
	}

	// Skip enabled agents that define no location for this scope (with a notice);
	// error only when none of them does.
	supported, skipped := splitByScope(agents, scope)
	for _, a := range skipped {
		ui.Warnf("skipped %s: no %s location", a.Name, scope)
	}
	if len(supported) == 0 {
		return fmt.Errorf("no enabled agent has a %s location; define one in %s", scope, config.Path(home))
	}

	res, err := linker.Unlink(home, id, supported, scope, base)
	reportUnlinkResult(res, scopeLabel(scope, base, cwd))
	// A local unlink may have emptied a tracked root; drop any that no longer
	// hold a skillm link so `list` stops scanning them.
	if scope == agentdir.Local {
		pruneLocalRoots(home)
	}
	if err != nil {
		return err
	}
	return nil
}

// reportUnlinkResult prints a styled line per agent describing what Unlink did.
func reportUnlinkResult(res linker.Result, label string) {
	for _, ar := range res.Agents {
		switch ar.Action {
		case linker.ActionRemoved:
			ui.Successf("Unlinked %s from %s (%s)", ar.ID, ar.Agent.Name, label)
		case linker.ActionAbsent:
			ui.Warnf("%s was not linked for %s (%s)", ar.ID, ar.Agent.Name, label)
		}
	}
}
