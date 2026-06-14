package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/ultrakorne/skillm/internal/agentdir"
	"github.com/ultrakorne/skillm/internal/config"
	"github.com/ultrakorne/skillm/internal/linker"
	"github.com/ultrakorne/skillm/internal/state"
	"github.com/ultrakorne/skillm/internal/store"
	"github.com/ultrakorne/skillm/internal/ui"
)

// link-command scope flags. These are package-private to the link command's
// file but, because unlink.go shares package cmd, the resolveScope and
// scopeLabel helpers below are reused by both commands (each binds its own flag
// vars).
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
			"(see config.agents) at the chosen scope. With no flag, skillm asks " +
			"interactively where to link: Global (the agents' user-level " +
			"~/.<agent>/skills folders), Local (this directory's <cwd>/.<agent>/skills " +
			"folders), or a custom directory you type with Tab path-completion. --global " +
			"or --local skip the prompt and link at that scope directly; on a " +
			"non-interactive terminal one of them is required. Folders are created if " +
			"missing. Re-linking an already-correct symlink is a no-op; skillm refuses to " +
			"overwrite anything it did not create.",
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

	// A skill must exist in Home before it can be linked.
	if !store.Exists(home, id) {
		return fmt.Errorf("skill %q is not in Home (%s); add it first with `skillm add`", id, store.SkillsDir(home))
	}

	agents := cfg.EnabledAgents()
	if len(agents) == 0 {
		return fmt.Errorf("no enabled agents in %s; run `skillm agent` to enable at least one", config.Path(home))
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("determine current directory: %w", err)
	}

	// Resolve scope only after the cheap checks pass, so an interactive prompt
	// never asks the user to pick a scope for a link that cannot happen.
	scope, base, err := resolveScope(global, local, cwd)
	if err != nil {
		return err
	}

	// An enabled agent that defines no location for this scope is skipped with a
	// notice; it is only an error when none of the enabled agents has one.
	supported, skipped := splitByScope(agents, scope)
	for _, a := range skipped {
		ui.Warnf("skipped %s: no %s location", a.Name, scope)
	}
	if len(supported) == 0 {
		return fmt.Errorf("no enabled agent has a %s location; define one in %s", scope, config.Path(home))
	}

	res, err := linker.Link(home, id, supported, scope, base)
	// Report whatever succeeded before any refusal, then surface the error.
	reportLinkResult("Linked", res, scopeLabel(scope, base, cwd))
	// Remember the project directory so `list` and `remove` can find this link
	// from anywhere, not just from within base. Global links need no record.
	if scope == agentdir.Local && linkedAny(res) {
		if rerr := addLocalRoot(home, base); rerr != nil {
			ui.Warnf("linked, but could not record %s for `skillm list`: %v", base, rerr)
		}
	}
	if err != nil {
		return err
	}
	return nil
}

// splitByScope partitions agents into those that define a skill folder at scope
// (supported) and those that do not (skipped). Callers link only the supported
// ones and warn about the rest; an agent may legitimately have no folder at a
// given scope (e.g. a global-only agent linked locally).
func splitByScope(agents []agentdir.Agent, scope agentdir.Scope) (supported, skipped []agentdir.Agent) {
	for _, a := range agents {
		if a.Supports(scope) {
			supported = append(supported, a)
		} else {
			skipped = append(skipped, a)
		}
	}
	return supported, skipped
}

// linkedAny reports whether the Link result contains at least one link skillm
// created or already had in place — i.e. base is a directory worth remembering.
func linkedAny(res linker.Result) bool {
	for _, ar := range res.Agents {
		if ar.Action == linker.ActionCreated || ar.Action == linker.ActionAlreadyLinked {
			return true
		}
	}
	return false
}

// addLocalRoot records dir (made absolute) in Home's tracked local roots so
// later commands scan it for links. It loads, mutates, and saves state only
// when dir is new.
func addLocalRoot(home, dir string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	st, err := state.Load(home)
	if err != nil {
		return err
	}
	if st.AddLocalRoot(abs) {
		return state.Save(home, st)
	}
	return nil
}

// resolveScope maps the --global/--local flags (mutually exclusive) to a Scope
// and the base directory a local-scope link is rooted at. When neither flag is
// given it runs the interactive picker (Global / Local / custom path); on a
// non-TTY the picker refuses and names the flags to pass instead. base is
// ignored for Global scope. cobra enforces that the two flags are not both set.
func resolveScope(global, local bool, cwd string) (agentdir.Scope, string, error) {
	switch {
	case global:
		return agentdir.Global, cwd, nil
	case local:
		return agentdir.Local, cwd, nil
	default:
		sel, err := ui.SelectScope(cwd)
		if err != nil {
			return agentdir.Global, cwd, err
		}
		if sel.Global {
			return agentdir.Global, cwd, nil
		}
		// Anchor a typed (possibly relative) custom path to an absolute base so
		// the link, its report line, and the recorded root all agree.
		base, err := filepath.Abs(sel.Path)
		if err != nil {
			return agentdir.Local, sel.Path, fmt.Errorf("resolve %s: %w", sel.Path, err)
		}
		return agentdir.Local, base, nil
	}
}

// scopeLabel renders the scope for per-agent report lines. Global and a Local
// link rooted at cwd keep their bare names; a Local link rooted elsewhere (the
// custom-path choice) also shows the directory so the output is unambiguous.
func scopeLabel(scope agentdir.Scope, base, cwd string) string {
	if scope == agentdir.Global || base == "" || base == cwd {
		return scope.String()
	}
	return fmt.Sprintf("%s: %s", scope, base)
}

// reportLinkResult prints a styled line per agent describing what Link did.
func reportLinkResult(verb string, res linker.Result, label string) {
	for _, ar := range res.Agents {
		switch ar.Action {
		case linker.ActionCreated:
			ui.Successf("%s %s for %s (%s)", verb, ar.ID, ar.Agent.Name, label)
		case linker.ActionAlreadyLinked:
			ui.Successf("%s already linked for %s (%s)", ar.ID, ar.Agent.Name, label)
		}
	}
}
