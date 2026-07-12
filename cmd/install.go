package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ultrakorne/skillm/internal/agentdir"
	"github.com/ultrakorne/skillm/internal/config"
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
		Long: "install makes skills visible to your agents at one scope: globally via a " +
			"canonical copy in ~/.agents/skills plus symlinks in each enabled agent's own " +
			"skill folder (see config.agents), or locally via a committable copy in the " +
			"project. Pass " +
			"one or more already-installed skill ids, --all to install every registered " +
			"skill, or no arguments to pick interactively from the registered skills — " +
			"handy for adding another scope or project to a skill you already have.\n\n" +
			"The first argument may instead be a Source — a git repository URL or an " +
			"explicitly path-shaped local path (./, ../, /, ~, or a *.git suffix). skillm " +
			"then fetches it (treelessly for git), lets you pick which skills when it is a " +
			"catalog of several (or pass skill ids / --all / --as / --ref), and installs " +
			"the result straight into the chosen scope — fetch, pick, and install in one " +
			"step. A bare name is always a registered id, never a Source. Installing an " +
			"already-registered id from the same Source refreshes it to the fetched " +
			"content; the same id from a different Source is a collision you resolve with " +
			"--as. Installing a bare id copies the skill from its existing global copy when " +
			"there is one, otherwise re-fetches it from its recorded source@ref (which may " +
			"advance the recorded revision).\n\n" +
			"With no scope flag, skillm asks where to install: Global (the agents' " +
			"user-level ~/.<agent>/skills folders), Local (this project), or a custom " +
			"directory you type with Tab path-completion; the chosen scope applies to " +
			"every selected skill. --global or --local skip the prompt; on a " +
			"non-interactive terminal pass skill ids (or --all) together with --global or " +
			"--local. Folders are created if missing.\n\n" +
			"Both scopes write a real copy into a canonical .agents/skills store (read " +
			"natively by Codex, Cursor, Amp, Gemini CLI, and more) and link every other " +
			"enabled agent to it. A Global install puts the copy in ~/.agents/skills and " +
			"absolute symlinks in the agents' user-level folders (e.g. ~/.claude/skills/<id>). " +
			"A Local install puts it in the project's .agents/skills, links agents with " +
			"relative in-repo symlinks (e.g. .claude/skills/<id>), and records it in " +
			"skills-lock.json — all committable, so teammates get working skills on clone, " +
			"and the lockfile is interoperable with vercel's `npx skills` CLI. Re-installing " +
			"something already correct is a no-op; skillm refuses to overwrite anything it " +
			"did not create.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInstall(cmd, args, installFlagGlobal, installFlagLocal, installFlagAll)
		},
	}
	f := c.Flags()
	f.BoolVar(&installFlagGlobal, "global", false, "install into the agents' user-level skill folders")
	f.BoolVar(&installFlagLocal, "local", false, "install into the current directory's project (.agents/skills + agent links)")
	f.BoolVar(&installFlagAll, "all", false, "install every skill (in Home, or in a source catalog); no interactive picker")
	f.StringVar(&installFlagAs, "as", "", "override the Skill ID when installing from a source (resolves a collision; single skill only)")
	f.StringVar(&installFlagRef, "ref", "", "pin a branch, tag, or commit when installing from a git source")
	c.MarkFlagsMutuallyExclusive("global", "local")
	return c
}

func runInstall(cmd *cobra.Command, args []string, global, local, all bool) error {
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

	// Resolve which skills to install and where each one's content comes from.
	// The first argument decides the mode and a Source cannot be mixed with
	// registered ids: a Source-shaped first arg (a git URL or an explicitly
	// path-shaped path) triggers source mode — fetch straight into a staging dir
	// — while a bare name (or no arg) is a registered id whose content is copied
	// from an existing canonical copy or re-fetched. Neither mode writes the
	// registry: installVendored records each entry once its install lands.
	var items []stagedSkill
	var cleanup func()
	if len(args) > 0 && source.LooksLikeSource(args[0]) {
		items, cleanup, err = fetchToStage(cmd, home, args[0], fetchOpts{
			As:         installFlagAs,
			Ref:        installFlagRef,
			All:        all,
			SelectArgs: args[1:],
		})
	} else {
		// --as/--ref only make sense when fetching a source.
		if installFlagAs != "" {
			return errors.New("the --as flag only applies when installing from a source (a git URL or local path)")
		}
		if installFlagRef != "" {
			return errors.New("the --ref flag only applies when installing from a git source")
		}
		items, cleanup, err = resolveIDItems(cmd, home, st, agents, cwd, args, all)
	}
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return nil // the selection step already reported why (nothing registered / nothing picked)
	}

	// One scope applies to every selected skill. Resolved after selection so an
	// interactive run asks "which skills" before "where".
	scope, base, err := resolveInstallTarget(global, local, cwd)
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

	return installVendored(home, st, items, supported, scope, base, scopeLabel(scope, base, cwd))
}

// installVendored materializes each selected skill's install at (scope, base):
// the canonical copy in the scope's .agents/skills store (written from the
// skill's staged/fetched content), a link for every other supported agent, and
// — at Local scope — a skills-lock.json entry. Each skill's registry entry is
// upserted once its copy lands (recording Source/Path/Ref/Revision and the
// vendored root at Local or the Global flag at Global) so update/uninstall/list
// can find it later — an entry exists only for a skill that is installed
// somewhere. Foreign files at the canonical slot are not clobbered silently:
// skillm asks once for the whole batch on a TTY (or refuses on a non-TTY)
// unless --force/--yes was given. A legacy skillm symlink at the slot is
// converted to a copy without asking.
func installVendored(home string, st *state.State, items []stagedSkill, agents []agentdir.Agent, scope agentdir.Scope, base, label string) error {
	force := flagForce || flagYes

	// Pre-scan every canonical slot for foreign entries that would be
	// overwritten, so the question (or the refusal) covers the whole batch once.
	recorded := make(map[string]bool, len(items))
	var conflicts []string
	for _, it := range items {
		id := it.entry.ID
		if scope == agentdir.Local {
			recorded[id] = slices.Contains(st.VendoredRoots(id), base)
		} else {
			recorded[id] = st.IsGlobal(id)
		}
		if c := vendorConflict(home, id, scope, base, recorded[id]); c != "" {
			conflicts = append(conflicts, c)
		}
	}
	if len(conflicts) > 0 && !force {
		if !ui.IsTTY() {
			return fmt.Errorf("refusing to overwrite files skillm did not create:\n  %s\npass --force to overwrite them", strings.Join(conflicts, "\n  "))
		}
		ok, err := ui.Confirm(confirmVendorOverwritePrompt(conflicts))
		if err != nil {
			return err
		}
		force = ok // declined: leave foreign entries untouched, install the rest
	}

	stateDirty := false
	installedAny := false
	var runErr error
	for _, it := range items {
		id := it.entry.ID
		action, err := vendorOne(home, id, it.dir, agents, scope, base, recorded[id], force, label)
		if err != nil {
			runErr = err
			break
		}
		if action == vendorBlocked {
			ui.Warnf("skipped %s: installing here would overwrite files skillm did not create (pass --force)", id)
			continue
		}
		ui.Successf("%s %s in %s (%s)", vendorActionLabel(action), id, canonicalDisplay(scope), label)
		installedAny = true

		// Record the entry now that its copy landed. Upsert first (Source/Path/
		// Ref/Revision, preserving any install markers merged in earlier), then
		// add this scope's marker.
		st.Upsert(it.entry)
		stateDirty = true
		if scope == agentdir.Local {
			st.AddVendoredRoot(id, base)
			if entry, ok := st.Get(id); ok {
				upsertLockEntry(entry, base)
			}
		} else {
			st.SetGlobal(id, true)
		}
	}

	// Remember the project directory so `list`, `update`, and `uninstall` can
	// find this install from anywhere. Done even on a partial failure, so a
	// copy that did land is found.
	if scope == agentdir.Local && installedAny && st.AddLocalRoot(base) {
		stateDirty = true
	}
	if stateDirty {
		if serr := state.Save(home, st); serr != nil {
			ui.Warnf("installed, but could not record the install for `skillm list`/`update`: %v", serr)
		}
	}
	if scope == agentdir.Local && installedAny {
		ui.Hintf("commit %s and %s to share these skills with your team", agentdir.CanonicalLocalRel, "skills-lock.json")
	}
	return runErr
}

// confirmVendorOverwritePrompt builds the one-shot confirmation shown before
// an install overwrites files skillm did not create.
func confirmVendorOverwritePrompt(paths []string) string {
	return fmt.Sprintf("These paths exist and were not created by skillm:\n  %s\nOverwrite them with installed copies?",
		strings.Join(paths, "\n  "))
}

// resolveIDItems resolves the id-mode selection into installable items: it picks
// which registered skills to act on (selectInstallIDs), then for each resolves
// where its content comes from (idModeSource) — an existing global canonical
// copy, a local-path skill's source directory, or a fresh re-fetch. It returns
// the items (entry + content dir) and a cleanup func the caller must defer to
// remove any re-fetch temp dirs (always non-nil). An empty result with a nil
// error means the selection step already reported why.
func resolveIDItems(cmd *cobra.Command, home string, st *state.State, agents []agentdir.Agent, cwd string, args []string, all bool) ([]stagedSkill, func(), error) {
	cleanup := func() {}
	ids, err := selectInstallIDs(home, st, agents, cwd, args, all)
	if err != nil || len(ids) == 0 {
		return nil, cleanup, err
	}

	var cleanups []func()
	cleanup = func() {
		for _, c := range cleanups {
			c()
		}
	}
	items := make([]stagedSkill, 0, len(ids))
	for _, id := range ids {
		entry, ok := st.Get(id)
		if !ok {
			// selectInstallIDs validated membership, so this should not happen.
			cleanup()
			return nil, func() {}, fmt.Errorf("skill %q is not registered", id)
		}
		dir, clean, err := idModeSource(cmd.Context(), home, &entry)
		if clean != nil {
			cleanups = append(cleanups, clean)
		}
		if err != nil {
			cleanup()
			return nil, func() {}, err
		}
		items = append(items, stagedSkill{entry: entry, dir: dir})
	}
	return items, cleanup, nil
}

// idModeSource resolves the directory whose content is skill e's, for an
// install-by-id (adding a scope/project to an already-registered skill). In
// order: (1) the canonical global copy when the skill is installed globally and
// that copy exists (no network); (2) a local-path skill's recorded source
// directory when it still exists; (3) otherwise a fresh re-fetch from
// e.Source@e.Ref (git), which materializes the content and may advance e's
// recorded Revision. It returns the content dir and a cleanup func (nil when no
// temp was created). e is mutated in place when a re-fetch advances the revision.
func idModeSource(ctx context.Context, home string, e *state.SkillEntry) (string, func(), error) {
	if e.Global && vendorCopyExists(home, e.ID, agentdir.Global, "") {
		return agentdir.CanonicalSkillDirAt(agentdir.Global, "", e.ID), nil, nil
	}
	if e.Kind == state.KindLocal {
		if dirExists(e.Source) {
			return e.Source, nil, nil
		}
		return "", nil, fmt.Errorf("local skill %q has no global copy and its source %s is gone; reinstall it from a source", e.ID, e.Source)
	}
	// Git skill with no reusable global copy: re-fetch from the pinned source.
	dir, rev, clean, err := refetchSkill(ctx, *e)
	if err != nil {
		return "", nil, fmt.Errorf("re-fetch %q from %s: %w", e.ID, e.Source, err)
	}
	if rev != e.Revision {
		e.Revision = rev
		e.InstalledAt = time.Now().UTC()
	}
	return dir, clean, nil
}

// selectInstallIDs resolves which registered skills `install` should act on:
//
//   - explicit ids: each must already be registered; if any is not, it errors
//     and names all the unknown ones (atomic — nothing is installed);
//   - --all: every registered skill, in registry order;
//   - neither: an interactive multiselect over every registered skill, each
//     annotated with where it is already installed (which refuses on a non-TTY,
//     naming the skill_id / --all escape hatch).
//
// It returns an empty slice and no error when there is nothing to do, having
// already told the user why (nothing registered, or an empty interactive
// selection).
func selectInstallIDs(home string, st *state.State, agents []agentdir.Agent, cwd string, args []string, all bool) ([]string, error) {
	if len(args) > 0 {
		if all {
			return nil, errors.New("pass either skill ids or --all, not both")
		}
		return validateRegistered(st, args)
	}

	registered := registeredIDs(st)
	if len(registered) == 0 {
		ui.Warnf("no skills installed yet; run `skillm install <url|path>` to fetch and install one")
		return nil, nil
	}
	if all {
		return registered, nil
	}

	opts := make([]ui.Option, 0, len(registered))
	for _, id := range registered {
		opts = append(opts, ui.Option{Label: id + installedMark(home, id, agents, cwd, st.IsGlobal(id)), Value: id})
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

// validateRegistered returns ids unchanged when every id names a registered
// skill, or an error naming the unknown ids — so passing one wrong id makes the
// whole command a no-op rather than a partial install.
func validateRegistered(st *state.State, ids []string) ([]string, error) {
	var missing []string
	for _, id := range ids {
		if _, ok := st.Get(id); !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("not installed: %s; install them from a source first (`skillm install <url|path> %s`)", strings.Join(missing, ", "), strings.Join(missing, " "))
	}
	return ids, nil
}

// dirExists reports whether p is an existing directory.
func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// installedMark returns a short annotation for the interactive install picker
// describing where a skill is already installed: " (installed: global)",
// " (installed: local)", or both. "Installed" here means installed at the
// global scope (a recorded canonical copy or an agent link) or at the local
// scope of the current directory — the two places the scope choices (Global /
// this folder) would act on. A skill installed only in some OTHER project
// directory is deliberately treated as not installed, so the mark reflects
// what installing from here would change. Returns "" when neither applies.
func installedMark(home, id string, agents []agentdir.Agent, cwd string, globalRecorded bool) string {
	var where []string
	if (globalRecorded && vendorCopyExists(home, id, agentdir.Global, "")) ||
		len(scanLinkNames(home, id, agents, agentdir.Global, "")) > 0 {
		where = append(where, "global")
	}
	// Only count a local install for agents whose local folder is distinct from
	// their global one at cwd; otherwise a global link from home would also be
	// reported as local (the two folders are the same on disk). The canonical
	// copy itself also counts — a local install may have no links at all when
	// only .agents-native agents are enabled.
	localAgents, _ := splitLocalAliased(agents, cwd)
	if len(scanLinkNames(home, id, localAgents, agentdir.Local, cwd)) > 0 || localCopyExists(home, id, cwd) {
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

// resolveInstallTarget maps the --global/--local flags to a Scope and the base
// directory a local install is rooted at. When no scope flag is given it runs
// the interactive picker (Global / Local / custom path); on a non-TTY the
// picker refuses and names the flags to pass instead. base is ignored for
// Global scope. cobra enforces that --global/--local are not both set.
func resolveInstallTarget(global, local bool, cwd string) (scope agentdir.Scope, base string, err error) {
	switch {
	case global:
		return agentdir.Global, cwd, nil
	case local:
		abs, aerr := filepath.Abs(cwd)
		if aerr != nil {
			abs = cwd
		}
		return agentdir.Local, abs, nil
	default:
		sel, serr := ui.SelectScope(cwd)
		if serr != nil {
			return agentdir.Global, cwd, serr
		}
		if sel.Global {
			return agentdir.Global, cwd, nil
		}
		// Anchor a typed (possibly relative) custom path to an absolute base so
		// the copy, its report line, and the recorded root all agree.
		b, aerr := filepath.Abs(sel.Path)
		if aerr != nil {
			return agentdir.Local, sel.Path, fmt.Errorf("resolve %s: %w", sel.Path, aerr)
		}
		return agentdir.Local, b, nil
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
