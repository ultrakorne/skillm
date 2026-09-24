package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ultrakorne/skillm/internal/core"
	"github.com/ultrakorne/skillm/internal/protocol"
	"github.com/ultrakorne/skillm/internal/store"
	"github.com/ultrakorne/skillm/internal/ui"
)

func init() {
	rootCmd.AddCommand(newUpdateCmd())
}

func newUpdateCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "update [skill_id]",
		Short: "Pull the latest revision of outdated git skills into every install",
		Long: "Update re-fetches the upstream revision of git-sourced skills. With no " +
			"argument it updates every outdated git skill; with a skill id it updates that " +
			"one. When a skill's upstream has advanced, its new content is written straight " +
			"into every recorded install from a single clone: the Global install's " +
			"~/.agents/skills copy and each tracked project's Local install — its " +
			".agents/skills copy is rewritten and its skills-lock.json entry refreshed. An " +
			"all-skills update also adopts skills teammates added to any tracked project's " +
			"skills-lock.json (see `skillm import`). An install whose copy no longer matches " +
			"its upstream is re-synced even when the upstream revision has not moved, so a " +
			"copy reverted by git or edited in place is repaired while its install is still " +
			"recorded. Local-path skills have no " +
			"upstream and are not re-fetched, but their installed copies are re-synced from " +
			"the recorded source directory when it still exists and its content has changed. " +
			"An agent link path occupied by something skillm did not create (a skill " +
			"copied in by hand or by another tool) is left alone (with a warning when its " +
			"copy is re-synced); pass " +
			"--force to replace it with skillm's link and take the skill over.\n\n" +
			"With --json a successful run's result lists every skill's outcome; --events " +
			"also streams one row per fetched skill as it happens. A skill that failed " +
			"makes the run fail with code update_failed after the others were written: " +
			"each failed skill is then a warning with its skill_id, and the outcomes of " +
			"the others are only on the --events stream.",
		Args:        cobra.MaximumNArgs(1),
		Annotations: map[string]string{annotationJSON: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			var id string
			if len(args) == 1 {
				id = strings.TrimSpace(args[0])
			}
			return runUpdate(cmd.Context(), flagHome, id, flagForce)
		},
	}
	return c
}

func runUpdate(ctx context.Context, homeOverride, id string, force bool) error {
	home, err := store.Home(homeOverride)
	if err != nil {
		return err
	}
	opts := core.Options{Home: home, Force: force, Yes: flagYes}
	// Only an older relative local Source needs the working directory, and
	// update must not fail when it is gone, so it is best effort.
	if cwd, err := os.Getwd(); err == nil {
		opts.Cwd = cwd
	}
	if flagJSON {
		return runUpdateJSON(ctx, opts, id)
	}

	// One live row per fetched skill, with a progress bar. Quitting the live
	// view cancels the fetches, and then nothing is written.
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	rep := newTermReporter(ctx, ui.ChecklistOptions{Bar: true, OnAbort: cancel})
	// core takes Home's lock around its write phases only, so the fetches
	// never block another skillm process. The checklist ends first: the lock
	// may print that it is waiting.
	opts.Lock = func(ctx context.Context) (func(), error) {
		rep.Wait()
		return lockHome(ctx, home, "skillm update")
	}

	// A failed fetch's row already shows its error; core's extra
	// update_failed line is for callers that do not follow the rows.
	res, err := core.Update(wctx, opts, dropCodes{rep: rep, codes: []string{core.CodeUpdateFailed}}, core.UpdateRequest{ID: id})
	rep.Wait()
	if cerr := ctx.Err(); cerr != nil {
		return cerr
	}
	if wctx.Err() != nil {
		return errors.New("update interrupted; no skill was updated")
	}
	var unknown *core.UnknownSkillError
	if errors.As(err, &unknown) {
		return fmt.Errorf("%w; run `skillm list` to see installed skills", err)
	}
	if err != nil {
		return err
	}

	if len(res.Skills) == 0 {
		ui.Successf("Nothing to update.")
		return nil
	}
	// Only claim nothing was due when nothing was: a re-synced copy is work
	// done, even though no upstream revision advanced.
	if !res.UpdatedAny() && !res.Synced {
		ui.Successf("Everything is up to date.")
	}
	return nil
}

// runUpdateJSON is `update --json`: core.Update reports to the protocol
// writer (one row per fetched skill with --events) and takes Home's lock for
// its writes only. A cancelled run fails with code "cancelled" (nothing was
// written if the fetches had not finished); a failed skill fails the run with
// code "update_failed" after the others were written.
func runUpdateJSON(ctx context.Context, opts core.Options, id string) error {
	opts.Lock = func(ctx context.Context) (func(), error) {
		return lockHome(ctx, opts.Home, "skillm update")
	}
	out := jsonOut()
	res, err := core.Update(ctx, opts, out, core.UpdateRequest{ID: id})
	if cerr := ctx.Err(); cerr != nil {
		return cerr
	}
	if err != nil {
		return err
	}
	return out.Result(protocol.NewUpdateData(res))
}
