package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ultrakorne/skillm/internal/agentdir"
	"github.com/ultrakorne/skillm/internal/config"
	"github.com/ultrakorne/skillm/internal/gitx"
	"github.com/ultrakorne/skillm/internal/linker"
	"github.com/ultrakorne/skillm/internal/source"
	"github.com/ultrakorne/skillm/internal/state"
	"github.com/ultrakorne/skillm/internal/store"
	"github.com/ultrakorne/skillm/internal/ui"
)

// add-specific flags, bound on the command in newAddCmd.
var (
	addAs     string // --as:     override the Skill ID
	addRef    string // --ref:    pin a branch/tag/sha (git sources)
	addAll    bool   // --all:    add every discovered skill without prompting
	addGlobal bool   // --global: also link the added skills at global scope
	addLocal  bool   // --local:  also link the added skills at local scope
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
			"branch, tag, or commit. By default add only fetches into Home; pass " +
			"--global or --local to also link the added skills at that scope.",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAdd(cmd, args)
		},
	}

	f := c.Flags()
	f.StringVar(&addAs, "as", "", "override the Skill ID (resolves a collision)")
	f.StringVar(&addRef, "ref", "", "pin a branch, tag, or commit (git sources; default: repo default branch)")
	f.BoolVar(&addAll, "all", false, "add every skill discovered in the source without prompting")
	f.BoolVar(&addGlobal, "global", false, "after adding, link the skill(s) at global scope")
	f.BoolVar(&addLocal, "local", false, "after adding, link the skill(s) at local scope")
	c.MarkFlagsMutuallyExclusive("global", "local")

	return c
}

func runAdd(cmd *cobra.Command, args []string) error {
	srcArg := args[0]
	var selectArg string
	if len(args) == 2 {
		selectArg = args[1]
	}

	// Resolve where things link to (only if --global/--local was given).
	linkScope, doLink, err := addLinkScope()
	if err != nil {
		return err
	}

	home, err := store.Home(flagHome)
	if err != nil {
		return err
	}
	if err := store.EnsureHome(home); err != nil {
		return err
	}

	kind, err := source.Classify(srcArg)
	if err != nil {
		return err
	}

	var added []string
	switch kind {
	case source.Git:
		added, err = addFromGit(cmd, home, srcArg, selectArg)
	case source.Local:
		added, err = addFromLocal(home, srcArg, selectArg)
	default:
		return fmt.Errorf("unsupported source kind %s", kind)
	}
	if err != nil {
		return err
	}
	if len(added) == 0 {
		// addFromGit/addFromLocal already reported why (e.g. nothing selected).
		return nil
	}

	if doLink {
		if err := linkAdded(home, added, linkScope); err != nil {
			return err
		}
	}
	return nil
}

// addLinkScope resolves whether the caller asked to link after adding, and at
// which scope. Bare `add` is fetch-only (doLink == false). add does not fall
// back to config.default_scope: only an explicit --global/--local triggers a
// link (PLAN §3 add).
func addLinkScope() (scope agentdir.Scope, doLink bool, err error) {
	switch {
	case addGlobal && addLocal:
		// Guarded by MarkFlagsMutuallyExclusive, but keep a defensive check.
		return agentdir.Global, false, errors.New("cannot use both --global and --local")
	case addGlobal:
		return agentdir.Global, true, nil
	case addLocal:
		return agentdir.Local, true, nil
	default:
		return agentdir.Global, false, nil
	}
}

// addFromGit treeless-clones srcArg, discovers the skills it holds, selects
// which to add (selectArg / --all / interactive picker), and copies each into
// Home with a git registry entry. It returns the ids actually added.
func addFromGit(cmd *cobra.Command, home, url, selectArg string) ([]string, error) {
	ctx := cmd.Context()

	tmp, err := os.MkdirTemp("", "skillm-clone-")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmp)

	// git clone wants to create the destination itself; give it a non-existent
	// subpath of our temp dir.
	repoDir := filepath.Join(tmp, "repo")

	if err := gitx.TreelessClone(ctx, url, addRef, repoDir); err != nil {
		return nil, err
	}

	// Resolve the concrete ref we pinned. When the user gave one we keep it; the
	// default branch is recorded otherwise so `check`/`update` know what to fetch.
	pinnedRef := addRef
	if pinnedRef == "" {
		def, err := gitx.DefaultRef(ctx, repoDir)
		if err != nil {
			return nil, fmt.Errorf("resolve default branch of %s: %w", url, err)
		}
		pinnedRef = def
	}

	found, err := source.DiscoverSkills(repoDir)
	if err != nil {
		return nil, err
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("no skills found in %s: expected at least one directory containing %s", url, "SKILL.md")
	}

	chosen, err := selectFound(found, selectArg)
	if err != nil {
		return nil, err
	}
	if len(chosen) == 0 {
		ui.Warnf("nothing selected; no skills added")
		return nil, nil
	}

	if addAs != "" && len(chosen) > 1 {
		return nil, errors.New("--as overrides a single Skill ID but more than one skill was selected; pick one skill_id or drop --as")
	}

	st, err := state.Load(home)
	if err != nil {
		return nil, err
	}

	var added []string
	for _, fnd := range chosen {
		id := fnd.Id
		if addAs != "" {
			id = addAs
		}

		if store.Exists(home, id) {
			return added, collisionErr(id)
		}

		// Compute the per-skill revision (its subdir tree SHA at the pinned ref)
		// before materializing, so we record exactly what we copied.
		subpath := repoRelSubpath(repoDir, fnd.Dir)
		rev, err := gitx.SubtreeSHA(ctx, repoDir, pinnedRef, subpath)
		if err != nil {
			return added, fmt.Errorf("read revision of %q: %w", id, err)
		}

		// Materialize the skill's subdir into a staging dir, then copy it into
		// Home under its id. Staging keeps store.AddSkillDir's collision and
		// cleanup guarantees intact.
		stage, err := os.MkdirTemp(tmp, "stage-")
		if err != nil {
			return added, fmt.Errorf("create staging dir: %w", err)
		}
		if err := gitx.MaterializeSubdir(ctx, repoDir, subpath, stage); err != nil {
			return added, fmt.Errorf("materialize skill %q: %w", id, err)
		}
		if err := store.AddSkillDir(home, id, stage); err != nil {
			return added, err
		}

		st.Upsert(state.SkillEntry{
			ID:          id,
			Kind:        state.KindGit,
			Source:      url,
			Path:        subpath,
			Ref:         pinnedRef,
			Revision:    rev,
			InstalledAt: time.Now().UTC(),
		})
		if err := state.Save(home, st); err != nil {
			// Roll back the Home copy so registry and disk stay consistent.
			_ = store.RemoveSkillDir(home, id)
			return added, err
		}

		ui.Successf("added %s (from %s)", id, url)
		added = append(added, id)
	}
	return added, nil
}

// addFromLocal copies a skill (or a selected skill from a local catalog
// directory) into Home as kind=local. Local skills carry no ref/revision and
// are not update-tracked (PLAN §3, CONTEXT "Local skill").
func addFromLocal(home, path, selectArg string) ([]string, error) {
	found, err := source.DiscoverSkills(path)
	if err != nil {
		return nil, err
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("no skills found in %s: expected a directory containing %s", path, "SKILL.md")
	}

	chosen, err := selectFound(found, selectArg)
	if err != nil {
		return nil, err
	}
	if len(chosen) == 0 {
		ui.Warnf("nothing selected; no skills added")
		return nil, nil
	}

	if addAs != "" && len(chosen) > 1 {
		return nil, errors.New("--as overrides a single Skill ID but more than one skill was selected; pick one skill_id or drop --as")
	}

	st, err := state.Load(home)
	if err != nil {
		return nil, err
	}

	var added []string
	for _, fnd := range chosen {
		id := fnd.Id
		if addAs != "" {
			id = addAs
		}

		if store.Exists(home, id) {
			return added, collisionErr(id)
		}

		if err := store.AddSkillDir(home, id, fnd.Dir); err != nil {
			return added, err
		}

		st.Upsert(state.SkillEntry{
			ID:          id,
			Kind:        state.KindLocal,
			Source:      fnd.Dir,
			InstalledAt: time.Now().UTC(),
		})
		if err := state.Save(home, st); err != nil {
			_ = store.RemoveSkillDir(home, id)
			return added, err
		}

		ui.Successf("added %s (local copy of %s)", id, fnd.Dir)
		added = append(added, id)
	}
	return added, nil
}

// selectFound resolves which of the discovered skills to add.
//
//   - One skill found: add it (no prompt). A selectArg, if given, must match it.
//   - selectArg given: add the single skill whose id matches it.
//   - --all given: add every discovered skill.
//   - Otherwise: show the interactive picker (which refuses on a non-TTY with a
//     message naming skill_id / --all).
func selectFound(found []source.Found, selectArg string) ([]source.Found, error) {
	if selectArg != "" {
		for _, f := range found {
			if f.Id == selectArg {
				return []source.Found{f}, nil
			}
		}
		return nil, fmt.Errorf("no skill %q in source; available: %s", selectArg, foundIDs(found))
	}

	if len(found) == 1 {
		return found, nil
	}

	if addAll {
		return found, nil
	}

	opts := make([]ui.Option, 0, len(found))
	for _, f := range found {
		label := f.Id
		if d := f.Skill.Description; d != "" {
			label = fmt.Sprintf("%s — %s", f.Id, d)
		}
		opts = append(opts, ui.Option{Label: label, Value: f.Id})
	}

	ids, err := ui.SelectSkills("Select skills to add", opts)
	if err != nil {
		return nil, err
	}

	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	var chosen []source.Found
	for _, f := range found {
		if want[f.Id] {
			chosen = append(chosen, f)
		}
	}
	return chosen, nil
}

// linkAdded links every freshly added skill into the enabled agents at scope,
// reusing the linker the `link` command uses. It reports per-skill outcomes and
// returns the first error encountered.
func linkAdded(home string, ids []string, scope agentdir.Scope) error {
	cfg, err := config.Load(home)
	if err != nil {
		return err
	}
	agents := agentdir.Enabled(cfg.Agents)
	if len(agents) == 0 {
		ui.Warnf("no agents enabled (see `skillm agent`); skills added but not linked")
		return nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("determine working directory: %w", err)
	}

	for _, id := range ids {
		res, err := linker.Link(home, id, agents, scope, cwd)
		// Report whatever succeeded before any refusal.
		for _, ar := range res.Agents {
			if ar.Action == linker.ActionCreated {
				ui.Successf("linked %s into %s (%s)", id, ar.Agent.Name, scope)
			}
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// collisionErr builds the standard "already exists" error, suggesting the two
// escape hatches from PLAN §3 (update or --as).
func collisionErr(id string) error {
	return fmt.Errorf("skill %q already exists in Home; run `skillm update %s` to refresh it, or pass `--as <name>` to add it under a different id", id, id)
}

// repoRelSubpath returns dir expressed relative to repoDir, using forward
// slashes — the form SubtreeSHA/MaterializeSubdir expect. The repo root yields
// "".
func repoRelSubpath(repoDir, dir string) string {
	rel, err := filepath.Rel(repoDir, dir)
	if err != nil || rel == "." {
		return ""
	}
	return filepath.ToSlash(rel)
}

// foundIDs joins the discovered skill ids for use in error messages.
func foundIDs(found []source.Found) string {
	ids := make([]string, 0, len(found))
	for _, f := range found {
		ids = append(ids, f.Id)
	}
	return strings.Join(ids, ", ")
}
