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
		Long: "remove unlinks a skill from every enabled agent at both the global and " +
			"local scopes, then deletes the Home copy and its registry entry so no " +
			"dangling symlinks are left behind. On a terminal it confirms first unless " +
			"--yes or --force is given.",
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

			agents := agentdir.Enabled(cfg.Agents)
			scopes := []agentdir.Scope{agentdir.Global, agentdir.Local}

			// Unlink from every enabled agent at both scopes. linker.Unlink is
			// idempotent for absent links and refuses to touch foreign symlinks
			// or real files; under --force we skip such refusals so the Home copy
			// can still be deleted (the foreign entry is left in place).
			for _, scope := range scopes {
				res, err := linker.Unlink(home, id, agents, scope, cwd)
				if err != nil {
					if flagForce {
						ui.Warnf("%v", err)
					} else {
						return err
					}
				}
				for _, ar := range res.Agents {
					if ar.Action == linker.ActionRemoved {
						ui.Successf("unlinked %s (%s: %s)", id, scope, ar.Agent.Name)
					}
				}
			}

			if err := store.RemoveSkillDir(home, id); err != nil {
				return err
			}

			if st.Remove(id) {
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
