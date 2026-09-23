package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ultrakorne/skillm/internal/agentdir"
	"github.com/ultrakorne/skillm/internal/config"
	"github.com/ultrakorne/skillm/internal/core"
	"github.com/ultrakorne/skillm/internal/source"
	"github.com/ultrakorne/skillm/internal/state"
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
		Use:   "install [<url|owner/repo|local-path>] [skill_id...]",
		Short: "Install skills into every enabled agent at the chosen scope",
		Long: "install makes skills visible to your agents at one scope: globally via a " +
			"canonical copy in ~/.agents/skills plus symlinks in each enabled agent's own " +
			"skill folder (see config.agents), or locally via a committable copy in the " +
			"project. Pass " +
			"one or more already-installed skill ids, --all to install every registered " +
			"skill, or no arguments to pick interactively from the registered skills — " +
			"handy for adding another scope or project to a skill you already have.\n\n" +
			"The first argument may instead be a Source — a git repository URL, a GitHub " +
			"owner/repo shorthand (as `npx skills add` takes; it expands to " +
			"https://github.com/owner/repo.git, and loses to a local directory of that " +
			"name when one exists), or an explicitly path-shaped local path (./, ../, /, " +
			"~, or a *.git suffix). skillm " +
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
			"did not create. Pass --force to overwrite it anyway, including taking over an " +
			"agent link path occupied by a skill copied in by hand or by another tool.",
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
	ctx := cmd.Context()
	opts, err := coreOptions(false)
	if err != nil {
		return err
	}
	// Hold Home's lock from load to save so a concurrent skillm process cannot
	// interleave its writes with ours.
	unlock, err := lockHome(ctx, opts.Home, "skillm install")
	if err != nil {
		return err
	}
	defer unlock()

	cfg, err := config.Load(opts.Home)
	if err != nil {
		return err
	}

	// Require at least one enabled agent before anything else — and, crucially,
	// before any network fetch in source mode — so we never fetch, prompt, or
	// resolve a scope that could not link anywhere.
	agents := cfg.EnabledAgents()
	if len(agents) == 0 {
		return fmt.Errorf("no enabled agents in %s; run `skillm agent` to enable at least one", config.Path(opts.Home))
	}

	st, err := state.Load(opts.Home)
	if err != nil {
		return err
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("determine current directory: %w", err)
	}
	opts.Cwd = cwd

	// Resolve which skills to install. The first argument decides the mode and
	// a Source cannot be mixed with registered ids: a Source-shaped first arg (a
	// git URL or an explicitly path-shaped path) triggers source mode — inspect
	// the Source, then pick from its skills — while a bare name (or no arg) is
	// a registered id. core.InstallSkills then does the install either way.
	var req core.InstallRequest
	if len(args) > 0 && source.LooksLikeSource(args[0]) {
		insp, err := core.Inspect(ctx, opts, args[0], installFlagRef)
		if err != nil {
			return err
		}
		defer insp.Close()
		ids, err := selectFound(insp, args[1:], all)
		if err != nil {
			return err
		}
		if len(ids) == 0 {
			ui.Warnf("nothing selected; no skills installed")
			return nil
		}
		// Report a bad selection (a different-source collision, --as on several
		// skills) before asking where to install.
		if err := core.ValidateSourceSelection(opts, insp, ids, installFlagAs); err != nil {
			return installError(err)
		}
		req = core.InstallRequest{Inspection: insp, IDs: ids, As: installFlagAs}
	} else {
		// --as/--ref only make sense when fetching a source.
		if installFlagAs != "" {
			return errors.New("the --as flag only applies when installing from a source (a git URL or local path)")
		}
		if installFlagRef != "" {
			return errors.New("the --ref flag only applies when installing from a git source")
		}
		ids, err := selectInstallIDs(opts.Home, st, agents, cwd, args, all)
		if err != nil || len(ids) == 0 {
			return err // on none, the selection step already reported why
		}
		req = core.InstallRequest{IDs: ids}
	}

	// One scope applies to every selected skill. Resolved after selection so an
	// interactive run asks "which skills" before "where".
	if req.Scope, req.Base, err = resolveInstallTarget(global, local, cwd); err != nil {
		return err
	}

	res, err := installConfirmingOverwrite(ctx, opts, req)
	if req.Scope == agentdir.Local && res.InstalledAny() {
		ui.Hintf("commit %s and %s to share these skills with your team", agentdir.CanonicalLocalRel, "skills-lock.json")
	}
	return installError(err)
}

// installConfirmingOverwrite runs core.InstallSkills. When it stops at files
// skillm did not create, it asks once for the whole batch on a TTY — "yes"
// retries with Yes (which, unlike --force, never takes over agent link paths:
// the question only listed the canonical slots), "no" retries skipping those
// skills — or refuses on a non-TTY. --force/--yes never get here.
func installConfirmingOverwrite(ctx context.Context, opts core.Options, req core.InstallRequest) (core.InstallResult, error) {
	res, err := core.InstallSkills(ctx, opts, termLog, req)
	var foreign *core.ForeignFilesError
	if !errors.As(err, &foreign) {
		return res, err
	}
	if !ui.IsTTY() {
		return res, fmt.Errorf("refusing to overwrite files skillm did not create:\n  %s\npass --force to overwrite them", strings.Join(foreign.Paths, "\n  "))
	}
	ok, err := ui.Confirm(confirmVendorOverwritePrompt(foreign.Paths))
	if err != nil {
		return res, err
	}
	if ok {
		opts.Yes = true
	} else {
		req.SkipForeign = true // leave foreign entries untouched, install the rest
	}
	// The skipped-agent notices were printed before the question.
	return core.InstallSkills(ctx, opts, dropCodes{rep: termLog, codes: []string{core.CodeAgentSkipped}}, req)
}

// installError adds the CLI's flag advice to the install errors a flag
// resolves; core's own text never names a flag.
func installError(err error) error {
	var collision *core.SourceCollisionError
	var aliased *core.LocalScopeAliasedError
	switch {
	case errors.As(err, &collision):
		return fmt.Errorf("%w; pass `--as <name>` to install this one under a different id", err)
	case errors.Is(err, core.ErrAsMultiple):
		return errors.New("--as overrides a single Skill ID but more than one skill was selected; pick one skill_id or drop --as")
	case errors.As(err, &aliased):
		return fmt.Errorf("%w; run from a project directory or use --global", err)
	}
	return err
}

// confirmVendorOverwritePrompt builds the one-shot confirmation shown before
// an install overwrites files skillm did not create.
func confirmVendorOverwritePrompt(paths []string) string {
	return fmt.Sprintf("These paths exist and were not created by skillm:\n  %s\nOverwrite them with installed copies?",
		strings.Join(paths, "\n  "))
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
	if (globalRecorded && core.CopyExists(home, id, agentdir.Global, "")) ||
		len(scanLinkNames(home, id, agents, agentdir.Global, "")) > 0 {
		where = append(where, "global")
	}
	// Only count a local install for agents whose local folder is distinct from
	// their global one at cwd; otherwise a global link from home would also be
	// reported as local (the two folders are the same on disk). The canonical
	// copy itself also counts — a local install may have no links at all when
	// only .agents-native agents are enabled.
	localAgents, _ := splitLocalAliased(agents, cwd)
	if len(scanLinkNames(home, id, localAgents, agentdir.Local, cwd)) > 0 || core.LocalCopyExists(home, id, cwd) {
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

// splitLocalAliased partitions agents by whether each has a usable local
// skill folder at base (see core.SplitLocalAliased).
var splitLocalAliased = core.SplitLocalAliased

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

// scopeLabel renders the scope for per-agent report lines (see
// core.ScopeLabel).
var scopeLabel = core.ScopeLabel
