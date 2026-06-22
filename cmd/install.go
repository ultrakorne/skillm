package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ultrakorne/skillm/internal/agentdir"
	"github.com/ultrakorne/skillm/internal/config"
	"github.com/ultrakorne/skillm/internal/linker"
	"github.com/ultrakorne/skillm/internal/source"
	"github.com/ultrakorne/skillm/internal/state"
	"github.com/ultrakorne/skillm/internal/store"
	"github.com/ultrakorne/skillm/internal/ui"
)

// install-command flags. Several scope helpers below (scopeLabel,
// splitLocalAliased) live in this file but, because the cmd package is shared,
// are reused by uninstall.go, list.go, and agent.go.
var (
	installFlagGlobal bool
	installFlagLocal  bool
	installFlagAll    bool
	installFlagCopy   bool
	installFlagAs     string
	installFlagRef    string
)

func init() {
	rootCmd.AddCommand(newInstallCmd())
}

func newInstallCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "install [<url|local-path>] [skill_id...]",
		Short: "Install skills into every enabled agent at the chosen scope",
		Long: "install makes skills visible to your agents by symlinking them from Home " +
			"into every enabled agent's skill folder (see config.agents) at one scope. Pass " +
			"one or more in-Home skill ids, --all to install every skill in Home, or no " +
			"arguments to pick interactively from the skills in Home.\n\n" +
			"The first argument may instead be a Source — a git repository URL or an " +
			"explicitly path-shaped local path (./, ../, /, ~, or a *.git suffix). skillm " +
			"then fetches it (treelessly for git), lets you pick which skills when it is a " +
			"catalog of several (or pass skill ids / --all / --as / --ref as with `add`), " +
			"adds them to Home, and installs the result — fetch, pick, and install in one " +
			"step. A bare name is always an in-Home id, never a Source. A skill already in " +
			"Home from the same Source is installed from the existing copy without " +
			"re-fetching (run `skillm update` to refresh); the same id from a different " +
			"Source is a collision you resolve with --as.\n\n" +
			"With no scope flag, skillm asks where to install: Global (the agents' " +
			"user-level ~/.<agent>/skills folders), Local (this directory's " +
			"<cwd>/.<agent>/skills folders), or a custom directory you type with Tab " +
			"path-completion; the chosen scope applies to every selected skill. --global or " +
			"--local skip the prompt; on a non-interactive terminal pass skill ids (or " +
			"--all) together with --global or --local. Folders are created if missing. " +
			"Re-installing an already-correct symlink is a no-op; skillm refuses to overwrite " +
			"anything it did not create. For a local install, --copy (or the interactive " +
			"prompt) vendors a real copy of the skill into each agent's project folder instead " +
			"of a symlink, so it can be committed to the project's git repo and shared with " +
			"teammates; --copy is rejected at global scope.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInstall(cmd, args, installFlagGlobal, installFlagLocal, installFlagAll, installFlagCopy)
		},
	}
	f := c.Flags()
	f.BoolVar(&installFlagGlobal, "global", false, "install into the agents' user-level skill folders")
	f.BoolVar(&installFlagLocal, "local", false, "install into the current directory's project skill folders")
	f.BoolVar(&installFlagAll, "all", false, "install every skill (in Home, or in a source catalog); no interactive picker")
	f.BoolVar(&installFlagCopy, "copy", false, "vendor a real copy into the project (commit to git) instead of a symlink; local scope only")
	f.StringVar(&installFlagAs, "as", "", "override the Skill ID when installing from a source (resolves a collision; single skill only)")
	f.StringVar(&installFlagRef, "ref", "", "pin a branch, tag, or commit when installing from a git source")
	c.MarkFlagsMutuallyExclusive("global", "local")
	return c
}

func runInstall(cmd *cobra.Command, args []string, global, local, all, vendor bool) error {
	// Pure-flag guard, checked before any work — config load, network fetch, or
	// Home mutation: --copy is local-only. Hoisted here so an impossible flag
	// combo fails fast in source mode too, before the fetch (matching the old
	// fetch-only `add` path). resolveInstallTarget below still enforces it as the
	// authority for the interactive path.
	if vendor && global {
		return errors.New("--copy is only valid for a local install; drop --global (a global install is always a symlink)")
	}

	home, err := store.Home(flagHome)
	if err != nil {
		return err
	}

	cfg, err := config.Load(home)
	if err != nil {
		return err
	}

	// Require at least one enabled agent before anything else — and, crucially,
	// before any network fetch in source mode — so we never fetch, prompt, or
	// resolve a scope that could not link anywhere.
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

	// Resolve which skills to install. The first argument decides the mode and a
	// Source cannot be mixed with in-Home ids: a Source-shaped first arg (a git
	// URL or an explicitly path-shaped path) triggers source mode — fetch into
	// Home, then install — while a bare name (or no arg) is an in-Home id.
	var ids []string
	if len(args) > 0 && source.LooksLikeSource(args[0]) {
		ids, err = fetchToHome(cmd, home, args[0], fetchOpts{
			As:              installFlagAs,
			Ref:             installFlagRef,
			All:             all,
			SelectArgs:      args[1:],
			ReuseSameSource: true,
		})
		if err != nil {
			return err
		}
		// fetchToHome wrote new registry entries via its own State; reload so the
		// install pipeline below (vendored-root recording in particular) sees them.
		st, err = state.Load(home)
		if err != nil {
			return err
		}
	} else {
		// --as/--ref only make sense when fetching a source.
		if installFlagAs != "" {
			return errors.New("the --as flag only applies when installing from a source (a git URL or local path)")
		}
		if installFlagRef != "" {
			return errors.New("the --ref flag only applies when installing from a git source")
		}
		// Explicit ids (validated against Home), --all (every skill in Home), or an
		// interactive multiselect (which annotates each skill with where it is
		// already installed — see installedMark).
		ids, err = selectInstallIDs(home, st, agents, cwd, args, all)
		if err != nil {
			return err
		}
	}
	if len(ids) == 0 {
		return nil // the selection step already reported why (empty Home / nothing picked)
	}

	// One scope (and method) applies to every selected skill. Resolved after
	// selection so an interactive run asks "which skills" before "where/how".
	scope, base, doVendor, err := resolveInstallTarget(global, local, vendor, cwd)
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

	if doVendor {
		return installVendored(home, st, ids, supported, base, scopeLabel(scope, base, cwd))
	}

	label := scopeLabel(scope, base, cwd)
	linkedAnywhere := false
	stateDirty := false
	var runErr error
	for _, id := range ids {
		// Convert a Vendored copy back to a symlink: a plain local install over a
		// recorded copy removes the committed files first so Link can take the
		// path, and drops the vendored root. (skillm's own symlink / absent path
		// are handled by Link directly.)
		if scope == agentdir.Local && slices.Contains(st.VendoredRoots(id), base) {
			removed, rerr := vendorRemove(home, id, supported, base)
			if rerr != nil {
				ui.Warnf("%v", rerr)
			}
			for _, a := range removed {
				ui.Successf("removed copy of %s for %s (%s)", id, a.Name, label)
			}
			if st.RemoveVendoredRoot(id, base) {
				stateDirty = true
			}
		}

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
		if st.AddLocalRoot(base) {
			stateDirty = true
		}
	}
	if stateDirty {
		if err := state.Save(home, st); err != nil {
			ui.Warnf("installed, but could not record %s for `skillm list`: %v", base, err)
		}
	}
	return runErr
}

// installVendored writes a Vendored copy of each selected skill into the
// supported agents' project folders at base, recording each base as a vendored
// root so update/uninstall/list can find the copies later. Foreign files at a
// target are not clobbered silently: skillm asks once for the whole batch on a
// TTY (or refuses on a non-TTY) unless --force/--yes was given. skillm's own
// symlink at a target is converted to a copy without asking.
func installVendored(home string, st *state.State, ids []string, agents []agentdir.Agent, base, label string) error {
	force := flagForce || flagYes

	// Pre-scan every target across all skills for foreign entries that would be
	// overwritten, so the question (or the refusal) covers the whole batch once.
	recorded := make(map[string]bool, len(ids))
	var conflicts []string
	for _, id := range ids {
		recorded[id] = slices.Contains(st.VendoredRoots(id), base)
		conflicts = append(conflicts, vendorConflicts(home, id, agents, base, recorded[id])...)
	}
	if len(conflicts) > 0 && !force {
		if !ui.IsTTY() {
			return fmt.Errorf("refusing to overwrite files skillm did not create:\n  %s\npass --force to overwrite them", strings.Join(conflicts, "\n  "))
		}
		ok, err := ui.Confirm(confirmVendorOverwritePrompt(conflicts))
		if err != nil {
			return err
		}
		force = ok // declined: leave foreign entries untouched, vendor the rest
	}

	stateDirty := false
	vendoredAny := false
	var runErr error
	for _, id := range ids {
		outcomes, err := vendorApply(home, id, agents, base, recorded[id], force)
		// vendorApply is atomic per base: a skill is either fully vendored across
		// its agents or fully blocked, so the root is recorded only when every
		// slot is skillm's — never while a skipped foreign dir sits at one slot.
		touched, blocked := reportVendorResult(id, outcomes, label)
		if touched {
			vendoredAny = true
			if st.AddVendoredRoot(id, base) {
				stateDirty = true
			}
		} else if blocked {
			ui.Warnf("skipped %s: vendoring here would overwrite files skillm did not create (pass --force)", id)
		}
		if err != nil {
			runErr = err
			break
		}
	}

	if stateDirty {
		if serr := state.Save(home, st); serr != nil {
			ui.Warnf("vendored, but could not record roots for `skillm list`/`update`: %v", serr)
		}
	}
	if vendoredAny {
		ui.Hintf("commit %s to share these skills with your team", base)
	}
	return runErr
}

// reportVendorResult prints a success line per agent that got a copy and reports
// whether any copy now exists (touched) and whether any slot was blocked. Because
// vendorApply is atomic per base, an id's outcomes are either all touched or all
// blocked; the caller prints a single skip notice for the blocked case (the
// per-slot paths are not individually foreign, so they are not named here).
func reportVendorResult(id string, outcomes []vendorOutcome, label string) (touched, blocked bool) {
	for _, o := range outcomes {
		if o.touched() {
			ui.Successf("%s %s for %s (%s)", vendorActionLabel(o.Action), id, o.Agent.Name, label)
			touched = true
		} else {
			blocked = true
		}
	}
	return touched, blocked
}

// confirmVendorOverwritePrompt builds the one-shot confirmation shown before a
// vendor install overwrites files skillm did not create.
func confirmVendorOverwritePrompt(paths []string) string {
	return fmt.Sprintf("These paths exist and were not created by skillm:\n  %s\nOverwrite them with vendored copies?",
		strings.Join(paths, "\n  "))
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

// resolveInstallTarget maps the --global/--local/--copy flags to a Scope, the
// base directory a local install is rooted at, and whether to vendor a copy
// rather than symlink. --copy is local-only: combined with --global it is an
// error, and given alone (no scope flag) it implies --local. When no scope flag
// is given it runs the interactive picker (Global / Local / custom path), which
// itself asks symlink-vs-copy after a local choice; on a non-TTY the picker
// refuses and names the flags to pass instead. base is ignored and vendoring is
// always false for Global scope. cobra enforces that --global/--local are not
// both set.
func resolveInstallTarget(global, local, copyFlag bool, cwd string) (scope agentdir.Scope, base string, vendor bool, err error) {
	if copyFlag && global {
		return agentdir.Global, cwd, false, errors.New("--copy is only valid for a local install; drop --global (a global install is always a symlink)")
	}
	switch {
	case global:
		return agentdir.Global, cwd, false, nil
	case local || copyFlag: // --copy with no scope flag implies --local
		abs, aerr := filepath.Abs(cwd)
		if aerr != nil {
			abs = cwd
		}
		return agentdir.Local, abs, copyFlag, nil
	default:
		sel, serr := ui.SelectScope(cwd)
		if serr != nil {
			return agentdir.Global, cwd, false, serr
		}
		if sel.Global {
			return agentdir.Global, cwd, false, nil
		}
		// Anchor a typed (possibly relative) custom path to an absolute base so
		// the link/copy, its report line, and the recorded root all agree.
		b, aerr := filepath.Abs(sel.Path)
		if aerr != nil {
			return agentdir.Local, sel.Path, sel.Copy, fmt.Errorf("resolve %s: %w", sel.Path, aerr)
		}
		return agentdir.Local, b, sel.Copy, nil
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
