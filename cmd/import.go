package cmd

import (
	"context"
	"os"

	"github.com/spf13/cobra"

	"github.com/ultrakorne/skillm/internal/core"
	"github.com/ultrakorne/skillm/internal/lockfile"
	"github.com/ultrakorne/skillm/internal/protocol"
	"github.com/ultrakorne/skillm/internal/ui"
)

func init() {
	rootCmd.AddCommand(newImportCmd())
}

func newImportCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "import [dir]",
		Short: "Adopt a project's skills-lock.json into skillm's tracking",
		Long: "import reads the skills-lock.json at the given directory (default: the " +
			"current one) — typically written by a teammate with skillm or with vercel's " +
			"`npx skills` CLI — and brings every entry under skillm management: each git " +
			"skill's source is fetched at the locked ref (recording its revision for " +
			"`check`/`update`), the directory is recorded as a Local install root, a missing " +
			"canonical copy in .agents/skills is written from the fetched content (or " +
			"restored from an existing install), and missing agent links are created for the " +
			"enabled agents. Entries already managed here " +
			"are simply adopted; entries that do not describe a git remote (local paths, " +
			"node_modules, registry skills) are reported and skipped. `skillm update` also " +
			"runs this adoption automatically across every tracked project, so a teammate's " +
			"additions join your machine-wide updates.\n\n" +
			"With --json, pass the project directory as an absolute path; a directory " +
			"with no skills-lock.json reports 0 entries rather than failing.",
		Args:        cobra.MaximumNArgs(1),
		Annotations: map[string]string{annotationJSON: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			return runImport(cmd.Context(), dir)
		},
	}
	return c
}

func runImport(ctx context.Context, dir string) error {
	opts, err := coreOptions(false)
	if err != nil {
		return err
	}
	// A relative dir is resolved against the working directory; an absolute
	// one does not need it, so an unreadable one is left for core to refuse.
	if cwd, err := os.Getwd(); err == nil {
		opts.Cwd = cwd
	}
	// core fetches first and takes Home's lock for its writes only.
	opts.Lock = func(ctx context.Context) (func(), error) {
		return lockHome(ctx, opts.Home, "skillm import")
	}
	if flagJSON {
		out := jsonOut()
		res, err := core.Import(ctx, opts, out, dir)
		if err != nil {
			return err
		}
		return out.Result(protocol.NewImportData(res))
	}
	res, err := core.Import(ctx, opts, termLog, dir)
	if err != nil {
		return err
	}
	switch {
	case res.Entries == 0:
		ui.Warnf("no %s (or no entries) in %s; nothing to import", lockfile.FileName, res.Root)
	case !res.ImportedAny():
		ui.Successf("nothing new to import from %s", res.Root)
	}
	return nil
}
