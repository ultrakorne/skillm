package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/ultrakorne/skillm/internal/config"
	"github.com/ultrakorne/skillm/internal/state"
	"github.com/ultrakorne/skillm/internal/store"
	"github.com/ultrakorne/skillm/internal/ui"
)

func init() {
	rootCmd.AddCommand(newCheckCmd())
}

func newCheckCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "check",
		Short: "Report which git skills have upstream updates",
		Long: "Check inspects every git-sourced skill: it treeless-fetches the skill's " +
			"pinned ref, recomputes the skill subdir's tree SHA, and compares it to the " +
			"revision recorded at add time. It reports which skills have updates available " +
			"and changes nothing. Local skills have no upstream and are skipped.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCheck(cmd.Context())
		},
	}
	return c
}

// runCheck reports the upstream update status of every git skill, per-skill and
// read-only. It never mutates Home or the registry.
func runCheck(ctx context.Context) error {
	home, err := store.Home(flagHome)
	if err != nil {
		return err
	}

	// config is loaded only to keep behaviour consistent with the rest of the
	// CLI (e.g. honoring a relocated Home); check itself needs no agent data.
	if _, err := config.Load(home); err != nil {
		return err
	}

	st, err := state.Load(home)
	if err != nil {
		return err
	}

	var git []state.SkillEntry
	var local []state.SkillEntry
	for _, e := range st.Skills {
		switch e.Kind {
		case state.KindGit:
			git = append(git, e)
		default:
			local = append(local, e)
		}
	}

	if len(git) == 0 && len(local) == 0 {
		fmt.Fprintln(os.Stdout, "No skills in Home.")
		return nil
	}

	updates := 0
	untracked := 0
	for _, e := range git {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		status := upstreamStatus(ctx, e)
		switch status {
		case statusUpdateAvailable:
			updates++
			ui.Warnf("%s: update available (%s)", e.ID, sourceLabel(e))
		case statusUntracked:
			untracked++
			ui.Errorf("%s: untracked — its subdir was not found upstream (%s)", e.ID, sourceLabel(e))
		default: // up-to-date
			ui.Successf("%s: up-to-date", e.ID)
		}
	}

	for _, e := range local {
		ui.Warnf("%s: local skill — edit it in Home directly (not update-tracked)", e.ID)
	}

	// A concise trailing summary so scripts/users get the headline count.
	switch {
	case updates == 0 && untracked == 0:
		fmt.Fprintln(os.Stdout, "All git skills are up-to-date.")
	case updates == 1:
		fmt.Fprintln(os.Stdout, "1 skill has an update available; run `skillm update` to apply it.")
	case updates > 1:
		fmt.Fprintf(os.Stdout, "%d skills have updates available; run `skillm update` to apply them.\n", updates)
	}

	return nil
}
