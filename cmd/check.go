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

	if len(st.Skills) == 0 {
		fmt.Fprintln(os.Stdout, "No skills in Home.")
		return nil
	}

	// One row per skill, checked concurrently with a live per-skill spinner.
	// Git skills incur a treeless fetch; local skills resolve instantly. ui
	// renders the rows (live on a TTY, plain otherwise) and returns the per-row
	// results in input order so the summary below is deterministic.
	labels := make([]string, len(st.Skills))
	for i, e := range st.Skills {
		labels[i] = e.ID
	}

	check := func(ctx context.Context, i int) ui.Result {
		e := st.Skills[i]
		if e.Kind != state.KindGit {
			return ui.Result{Level: ui.LevelWarn, Text: fmt.Sprintf("%s: local skill — edit it in Home directly (not update-tracked)", e.ID)}
		}
		switch upstreamStatus(ctx, e) {
		case statusUpdateAvailable:
			return ui.Result{Level: ui.LevelWarn, Text: fmt.Sprintf("%s: update available (%s)", e.ID, sourceLabel(e))}
		case statusUntracked:
			return ui.Result{Level: ui.LevelError, Text: fmt.Sprintf("%s: untracked — its subdir was not found upstream (%s)", e.ID, sourceLabel(e))}
		default: // up-to-date
			return ui.Result{Level: ui.LevelSuccess, Text: fmt.Sprintf("%s: up-to-date", e.ID)}
		}
	}

	results := ui.RunChecks(ctx, labels, check)
	if err := ctx.Err(); err != nil {
		return err
	}

	// Count only git skills: a git "update available" is LevelWarn, an "untracked"
	// is LevelError. Local skills are also LevelWarn but carry no upstream, so the
	// Kind guard keeps them out of the headline count.
	updates := 0
	untracked := 0
	for i, r := range results {
		if st.Skills[i].Kind != state.KindGit {
			continue
		}
		switch r.Level {
		case ui.LevelWarn:
			updates++
		case ui.LevelError:
			untracked++
		}
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
