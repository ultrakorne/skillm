package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/ultrakorne/skillm/internal/agentdir"
	"github.com/ultrakorne/skillm/internal/config"
	"github.com/ultrakorne/skillm/internal/linker"
	"github.com/ultrakorne/skillm/internal/state"
	"github.com/ultrakorne/skillm/internal/store"
	"github.com/ultrakorne/skillm/internal/ui"
)

func init() {
	rootCmd.AddCommand(newRemoveCmd())
}

func newRemoveCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "remove <skill_id>",
		Short: "Delete a skill from Home, unlinking it from every agent first",
		Long: "remove unlinks a skill from every agent at the global scope and in every " +
			"local folder skillm linked it into (tracked in state.toml), then deletes the " +
			"Home copy and its registry entry so no dangling symlinks are left behind. On " +
			"a terminal it confirms first unless --yes or --force is given.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id := args[0]

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

			_, inRegistry := st.Get(id)
			inHome := store.Exists(home, id)
			if !inRegistry && !inHome {
				return fmt.Errorf("skill %q is not in Home; nothing to remove", id)
			}

			// Confirm on a TTY unless the user opted out via --yes/--force.
			if ui.IsTTY() && !flagYes && !flagForce {
				ok, err := ui.Confirm(fmt.Sprintf("Remove skill %q from Home and unlink it from all agents?", id))
				if err != nil {
					return err
				}
				if !ok {
					ui.Warnf("aborted; %q was left untouched", id)
					return nil
				}
			}

			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("determine current directory: %w", err)
			}

			// Clear links for EVERY defined agent (not just the enabled ones):
			// a link made while an agent was enabled must not be left dangling
			// just because it is disabled now. Targets are the global folder plus
			// every local folder skillm tracked for this Home.
			agents := cfg.AllAgents()
			type target struct {
				scope agentdir.Scope
				dir   string
			}
			targets := []target{{agentdir.Global, cwd}}
			for _, dir := range localScanDirs(st.LocalRoots, cwd) {
				targets = append(targets, target{agentdir.Local, dir})
			}

			// linker.Unlink is idempotent for absent links and refuses to touch
			// foreign symlinks or real files; under --force we skip such refusals
			// so the Home copy can still be deleted (the foreign entry stays put).
			for _, tg := range targets {
				res, err := linker.Unlink(home, id, agents, tg.scope, tg.dir)
				if err != nil {
					if flagForce {
						ui.Warnf("%v", err)
					} else {
						return err
					}
				}
				for _, ar := range res.Agents {
					if ar.Action == linker.ActionRemoved {
						ui.Successf("unlinked %s from %s (%s)", id, ar.Agent.Name, scopeLabel(tg.scope, tg.dir, cwd))
					}
				}
			}

			if err := store.RemoveSkillDir(home, id); err != nil {
				return err
			}

			// Drop the skill, then prune any tracked root that no longer holds a
			// link now that this skill's local links are gone.
			removed := st.Remove(id)
			reconciled := reconcileLocalRoots(home, cfg.AllAgents(), st)
			if removed || reconciled {
				if err := state.Save(home, st); err != nil {
					return err
				}
			}

			ui.Successf("removed %s", id)
			return nil
		},
	}
	return c
}
