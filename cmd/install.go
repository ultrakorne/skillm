package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ultrakorne/skillm/internal/agentdir"
	"github.com/ultrakorne/skillm/internal/config"
	"github.com/ultrakorne/skillm/internal/linker"
	"github.com/ultrakorne/skillm/internal/state"
	"github.com/ultrakorne/skillm/internal/store"
	"github.com/ultrakorne/skillm/internal/ui"
)

// install-command flags. The scope helpers below (resolveScope, scopeLabel,
// splitByScope, addLocalRoot) live in this file but, because the cmd package is
// shared, they are reused by uninstall.go and add.go.
var (
	installFlagGlobal bool
	installFlagLocal  bool
	installFlagAll    bool
)

func init() {
	rootCmd.AddCommand(newInstallCmd())
}

func newInstallCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "install [skill_id...]",
		Short: "Install skills into every enabled agent at the chosen scope",
		Long: "install makes skills visible to your agents by symlinking them from Home " +
			"into every enabled agent's skill folder (see config.agents) at one scope. Pass " +
			"one or more skill ids, --all to install every skill in Home, or no arguments to " +
			"pick interactively from the skills in Home. With no flag, skillm asks where to " +
			"install: Global (the agents' user-level ~/.<agent>/skills folders), Local (this " +
			"directory's <cwd>/.<agent>/skills folders), or a custom directory you type with " +
			"Tab path-completion; the chosen scope applies to every selected skill. --global " +
			"or --local skip the prompt; on a non-interactive terminal pass skill ids (or " +
			"--all) together with --global or --local. Folders are created if missing. " +
			"Re-installing an already-correct symlink is a no-op; skillm refuses to overwrite " +
			"anything it did not create.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInstall(args, installFlagGlobal, installFlagLocal, installFlagAll)
		},
	}
	f := c.Flags()
	f.BoolVar(&installFlagGlobal, "global", false, "install into the agents' user-level skill folders")
	f.BoolVar(&installFlagLocal, "local", false, "install into the current directory's project skill folders")
	f.BoolVar(&installFlagAll, "all", false, "install every skill in Home (no interactive picker)")
	c.MarkFlagsMutuallyExclusive("global", "local")
	return c
}

func runInstall(args []string, global, local, all bool) error {
	home, err := store.Home(flagHome)
	if err != nil {
		return err
	}

	cfg, err := config.Load(home)
	if err != nil {
		return err
	}

	// Require at least one enabled agent before anything else, so we never
	// prompt for a selection or a scope that could not link anywhere.
	agents := cfg.EnabledAgents()
	if len(agents) == 0 {
		return fmt.Errorf("no enabled agents in %s; run `skillm agent` to enable at least one", config.Path(home))
	}

	st, err := state.Load(home)
	if err != nil {
		return err
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("determine current directory: %w", err)
	}

	// Resolve which skills to install: explicit ids (validated against Home),
	// --all (every skill in Home), or an interactive multiselect (which annotates
	// each skill with where it is already installed — see installedMark).
	ids, err := selectInstallIDs(home, st, agents, cwd, args, all)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil // selectInstallIDs already reported why (empty Home / nothing picked)
	}

	// One scope applies to every selected skill. Resolved after selection so an
	// interactive run asks "which skills" before "where".
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
	// At local scope, an agent whose local folder *is* its global folder at base
	// (the canonical case: running from home) has no real local scope here. Drop
	// such agents so we never write a "local" link that silently means global,
	// nor record base as a tracked local root.
	if scope == agentdir.Local {
		real, aliased := splitLocalAliased(supported, base)
		for _, a := range aliased {
			ui.Warnf("skipped %s: local scope here resolves to its global skill folder", a.Name)
		}
		if len(real) == 0 && len(aliased) > 0 {
			return fmt.Errorf("local scope resolves to the global skill folder here (%s); run from a project directory or use --global", base)
		}
		supported = real
	}
	if len(supported) == 0 {
		return fmt.Errorf("no enabled agent has a %s location; define one in %s", scope, config.Path(home))
	}

	label := scopeLabel(scope, base, cwd)
	linkedAnywhere := false
	var runErr error
	for _, id := range ids {
		res, err := linker.Link(home, id, supported, scope, base)
		// Report whatever succeeded before any refusal, then stop on the error.
		reportInstallResult(res, label)
		if linkedAny(res) {
			linkedAnywhere = true
		}
		if err != nil {
			runErr = err
			break
		}
	}

	// Remember the project directory so `list` and `uninstall` can find these
	// links from anywhere, not just from within base. Global links need no
	// record. Done even on a partial failure, so a link that did land is found.
	if scope == agentdir.Local && linkedAnywhere {
		if rerr := addLocalRoot(home, base); rerr != nil {
			ui.Warnf("installed, but could not record %s for `skillm list`: %v", base, rerr)
		}
	}
	return runErr
}

// selectInstallIDs resolves which skills `install` should act on:
//
//   - explicit ids: each must already be in Home; if any is not, it errors and
//     names all the unknown ones (atomic — nothing is installed);
//   - --all: every skill registered in Home, in registry order;
//   - neither: an interactive multiselect over every skill in Home, each
//     annotated with where it is already installed (which refuses on a non-TTY,
//     naming the skill_id / --all escape hatch).
//
// It returns an empty slice and no error when there is nothing to do, having
// already told the user why (empty Home, or an empty interactive selection).
func selectInstallIDs(home string, st *state.State, agents []agentdir.Agent, cwd string, args []string, all bool) ([]string, error) {
	if len(args) > 0 {
		if all {
			return nil, errors.New("pass either skill ids or --all, not both")
		}
		return validateInHome(home, args)
	}

	registered := registeredIDs(st)
	if len(registered) == 0 {
		ui.Warnf("no skills in Home; run `skillm add` first")
		return nil, nil
	}
	if all {
		return registered, nil
	}

	opts := make([]ui.Option, 0, len(registered))
	for _, id := range registered {
		opts = append(opts, ui.Option{Label: id + installedMark(home, id, agents, cwd), Value: id})
	}
	ids, err := ui.SelectSkills("Select skills to install", opts)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		ui.Warnf("nothing selected; no skills installed")
		return nil, nil
	}
	return ids, nil
}

// validateInHome returns ids unchanged when every id names a skill present in
// Home, or an error naming the unknown ids — so passing one wrong id makes the
// whole command a no-op rather than a partial install.
func validateInHome(home string, ids []string) ([]string, error) {
	var missing []string
	for _, id := range ids {
		if !store.Exists(home, id) {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("not in Home: %s; add them first with `skillm add`", strings.Join(missing, ", "))
	}
	return ids, nil
}

// installedMark returns a short annotation for the interactive install picker
// describing where a skill is already installed: " (installed: global)",
// " (installed: local)", or both. "Installed" here means linked at the global
// scope or at the local scope of the current directory — the two places the
// scope choices (Global / this folder) would act on. A skill linked only in some
// OTHER project directory is deliberately treated as not installed, so the mark
// reflects what installing from here would change. Returns "" when neither
// applies.
func installedMark(home, id string, agents []agentdir.Agent, cwd string) string {
	var where []string
	if len(scanLinkNames(home, id, agents, agentdir.Global, "")) > 0 {
		where = append(where, "global")
	}
	// Only count a local install for agents whose local folder is distinct from
	// their global one at cwd; otherwise a global link from home would also be
	// reported as local (the two folders are the same on disk).
	localAgents, _ := splitLocalAliased(agents, cwd)
	if len(scanLinkNames(home, id, localAgents, agentdir.Local, cwd)) > 0 {
		where = append(where, "local")
	}
	if len(where) == 0 {
		return ""
	}
	return " (installed: " + strings.Join(where, ", ") + ")"
}

// registeredIDs returns the ids of every skill in the registry, in registry
// order — the candidate set for `--all` and the interactive pickers (shared by
// install and uninstall).
func registeredIDs(st *state.State) []string {
	ids := make([]string, 0, len(st.Skills))
	for _, e := range st.Skills {
		ids = append(ids, e.ID)
	}
	return ids
}

// splitByScope partitions agents into those that define a skill folder at scope
// (supported) and those that do not (skipped). Callers link only the supported
// ones and warn about the rest; an agent may legitimately have no folder at a
// given scope (e.g. a global-only agent installed locally).
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

// splitLocalAliased partitions agents by whether each has a *usable* local
// skill folder at base. An agent's local scope is real when its local folder
// resolves to a different directory than its global folder; it is aliased when
// the two coincide (see agentdir.LocalAliasesGlobal), the canonical case being
// base == home, where e.g. <base>/.claude/skills *is* ~/.claude/skills. Callers
// pass agents already known to support a local folder; an agent without a
// global folder cannot alias and falls into real. This is how every local
// scan/write site avoids treating the global folder as if it were local.
func splitLocalAliased(agents []agentdir.Agent, base string) (real, aliased []agentdir.Agent) {
	for _, a := range agents {
		if agentdir.LocalAliasesGlobal(a, base) {
			aliased = append(aliased, a)
		} else {
			real = append(real, a)
		}
	}
	return real, aliased
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

// reportInstallResult prints a styled line per agent describing what install did.
func reportInstallResult(res linker.Result, label string) {
	for _, ar := range res.Agents {
		switch ar.Action {
		case linker.ActionCreated:
			ui.Successf("installed %s for %s (%s)", ar.ID, ar.Agent.Name, label)
		case linker.ActionAlreadyLinked:
			ui.Successf("%s already installed for %s (%s)", ar.ID, ar.Agent.Name, label)
		}
	}
}
