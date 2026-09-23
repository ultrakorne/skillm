package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/ultrakorne/skillm/internal/core"
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
			"and changes nothing. It compares upstream revisions only and never inspects the " +
			"installed copies, so `skillm update` may still re-sync an install whose copy has " +
			"drifted from its recorded revision. Local skills have no upstream and are skipped.",
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
	opts, err := coreOptions(false)
	if err != nil {
		return err
	}

	// One row per skill, checked concurrently by core with a live per-skill
	// spinner. Quitting the live view cancels the remaining checks; what was
	// found so far is still summarized, as before.
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	rep := newTermReporter(ctx, ui.ChecklistOptions{OnAbort: cancel})
	res, err := core.Check(wctx, opts, rep)
	rep.Wait()
	if cerr := ctx.Err(); cerr != nil {
		return cerr
	}
	if err != nil && wctx.Err() == nil {
		return err
	}

	if len(res.Skills) == 0 {
		fmt.Fprintln(os.Stdout, "No skills in Home.")
		return nil
	}

	// A skill whose upstream could not be read counts as untracked, whatever
	// the cause, as the CLI always reported it.
	updates := 0
	untracked := 0
	for _, s := range res.Skills {
		switch s.Status {
		case core.StatusUpdateAvailable:
			updates++
		case core.StatusUntracked, core.StatusError:
			untracked++
		}
	}

	// A concise trailing summary so scripts/users get the headline count. It is
	// deliberately phrased in terms of upstream revisions: update also re-syncs
	// installs whose copies drifted, which this command does not look for.
	switch {
	case updates == 0 && untracked == 0:
		fmt.Fprintln(os.Stdout, "All git skills are at their upstream revision.")
	case updates == 1:
		fmt.Fprintln(os.Stdout, "1 skill has an update available; run `skillm update` to apply it.")
	case updates > 1:
		fmt.Fprintf(os.Stdout, "%d skills have updates available; run `skillm update` to apply them.\n", updates)
	}

	return nil
}
