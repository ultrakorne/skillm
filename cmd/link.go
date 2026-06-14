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

// link-command scope flags. These are package-private to the link command's
// file but, because unlink.go shares package cmd, the resolveScope helper below
// is reused by both commands (each binds its own flag vars).
var (
	linkFlagGlobal bool
	linkFlagLocal  bool
)

func init() {
	rootCmd.AddCommand(newLinkCmd())
}

func newLinkCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "link <skill_id>",
		Short: "Link a skill into every enabled agent at the chosen scope",
		Long: "Create a symlink for <skill_id> in every enabled agent's skill folder " +
			"(see config.agents) at the chosen scope. --global links into the agents' " +
			"user-level folders (~/.<agent>/skills); --local links into the current " +
			"directory's project folders (<cwd>/.<agent>/skills), created if missing. " +
			"When neither flag is given, config.default_scope is used. Re-linking an " +
			"already-correct symlink is a no-op; skillm refuses to overwrite anything it " +
			"did not create.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLink(args[0], linkFlagGlobal, linkFlagLocal)
		},
	}
	f := c.Flags()
	f.BoolVar(&linkFlagGlobal, "global", false, "link into the agents' user-level skill folders")
	f.BoolVar(&linkFlagLocal, "local", false, "link into the current directory's project skill folders")
	c.MarkFlagsMutuallyExclusive("global", "local")
	return c
}

func runLink(id string, global, local bool) error {
	home, err := store.Home(flagHome)
	if err != nil {
		return err
	}

	cfg, err := config.Load(home)
	if err != nil {
		return err
	}

	scope, err := resolveScope(global, local, cfg.DefaultScope)
	if err != nil {
		return err
	}

	// A skill must exist in Home before it can be linked.
	if !store.Exists(home, id) {
		return fmt.Errorf("skill %q is not in Home (%s); add it first with `skillm add`", id, store.SkillsDir(home))
	}

	agents := agentdir.Enabled(cfg.Agents)
	if len(agents) == 0 {
		return fmt.Errorf("no enabled agents in %s; run `skillm agent` to enable at least one", config.Path(home))
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("determine current directory: %w", err)
	}

	res, err := linker.Link(home, id, agents, scope, cwd)
	// Report whatever succeeded before any refusal, then surface the error.
	reportLinkResult("Linked", res, scope)
	if err != nil {
		return err
	}
	return nil
}

// resolveScope maps the --global/--local flags (mutually exclusive) to a Scope.
// When neither is set it falls back to defaultScope from config (an invalid or
// empty default is reported as a configuration error). cobra enforces that the
// two flags are not both set.
func resolveScope(global, local bool, defaultScope string) (agentdir.Scope, error) {
	switch {
	case global:
		return agentdir.Global, nil
	case local:
		return agentdir.Local, nil
	default:
		scope, err := agentdir.ParseScope(defaultScope)
		if err != nil {
			return agentdir.Global, fmt.Errorf("config default_scope %q is invalid: %w", defaultScope, err)
		}
		return scope, nil
	}
}

// reportLinkResult prints a styled line per agent describing what Link did.
func reportLinkResult(verb string, res linker.Result, scope agentdir.Scope) {
	for _, ar := range res.Agents {
		switch ar.Action {
		case linker.ActionCreated:
			ui.Successf("%s %s for %s (%s)", verb, ar.ID, ar.Agent.Name, scope)
		case linker.ActionAlreadyLinked:
			ui.Successf("%s already linked for %s (%s)", ar.ID, ar.Agent.Name, scope)
		}
	}
}
