package cmd

import (
	"github.com/spf13/cobra"

	"github.com/ultrakorne/skillm/internal/store"
)

// add-specific flags, bound on the command in newAddCmd. `add` is strictly
// fetch-only — exposing a skill to agents is `install`'s job (PLAN §3) — so it
// carries no scope/copy flags, only the fetch selectors.
var (
	addAs  string // --as:  override the Skill ID
	addRef string // --ref: pin a branch/tag/sha (git sources)
	addAll bool   // --all: add every discovered skill without prompting
)

func init() {
	rootCmd.AddCommand(newAddCmd())
}

func newAddCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "add <url|local-path> [skill_id]",
		Short: "Fetch a skill from a git repo or local path into Home",
		Long: "add fetches a skill into the central Home. The argument is either a git " +
			"repository URL (a catalog of one or more skills, fetched treelessly) or a " +
			"local directory containing a SKILL.md.\n\n" +
			"When a git repo holds more than one skill, pass a skill_id (or --all) to " +
			"select non-interactively, otherwise skillm shows an interactive picker. " +
			"--as overrides the Skill ID (to resolve a collision); --ref pins a " +
			"branch, tag, or commit. add only fetches into Home and never installs — " +
			"run `skillm install` to expose a skill to your agents (it can also fetch " +
			"and install a source in one step).",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAdd(cmd, args)
		},
	}

	f := c.Flags()
	f.StringVar(&addAs, "as", "", "override the Skill ID (resolves a collision)")
	f.StringVar(&addRef, "ref", "", "pin a branch, tag, or commit (git sources; default: repo default branch)")
	f.BoolVar(&addAll, "all", false, "add every skill discovered in the source without prompting")

	return c
}

func runAdd(cmd *cobra.Command, args []string) error {
	srcArg := args[0]
	var selectArgs []string
	if len(args) == 2 {
		selectArgs = []string{args[1]}
	}

	home, err := store.Home(flagHome)
	if err != nil {
		return err
	}

	// Fetch-only: run the shared pipeline with the add collision policy (any id
	// already in Home is a hard error). The returned ids are not installed.
	_, err = fetchToHome(cmd, home, srcArg, fetchOpts{
		As:         addAs,
		Ref:        addRef,
		All:        addAll,
		SelectArgs: selectArgs,
	})
	return err
}
