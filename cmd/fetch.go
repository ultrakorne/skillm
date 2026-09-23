package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ultrakorne/skillm/internal/config"
	"github.com/ultrakorne/skillm/internal/core"
	"github.com/ultrakorne/skillm/internal/gitx"
	"github.com/ultrakorne/skillm/internal/source"
	"github.com/ultrakorne/skillm/internal/state"
	"github.com/ultrakorne/skillm/internal/store"
	"github.com/ultrakorne/skillm/internal/ui"
)

// This file holds the shared fetch → discover → select → stage pipeline used by
// install's source mode. It clones (git) or reads (local) the source, discovers
// the skills it holds, lets the caller pick which, and materializes each chosen
// skill's content into a caller-owned staging directory — without writing any
// copy or registry entry. Recording an install (the canonical copy, the agent
// links, the registry entry) is the install layer's job, so an entry exists
// only once an install actually lands.

// fetchOpts parameterizes the pipeline's selection knobs, mirroring install's
// source-mode flags.
type fetchOpts struct {
	// As overrides the Skill ID (resolves a collision). Requires a single
	// selected skill.
	As string
	// Ref pins a branch/tag/sha for a git source (default: repo default branch).
	Ref string
	// All selects every discovered skill without prompting.
	All bool
	// SelectArgs names the skills to select (the positional skill_id args). When
	// empty and more than one skill is discovered, the interactive picker runs
	// (or refuses on a non-TTY).
	SelectArgs []string
}

// stagedSkill is one chosen skill ready to install: the registry entry to upsert
// once its install lands, and the directory holding its content (a materialized
// git subdir under the clone temp, or a local source directory read in place).
type stagedSkill struct {
	entry state.SkillEntry
	dir   string
}

// fetchToStage runs the shared fetch → discover → select → stage pipeline. It
// ensures Home (and config) exist, classifies srcArg, discovers the skills it
// holds, selects which to stage, and materializes each chosen skill's content
// into a staging directory. It writes nothing to Home or the registry: it
// returns the staged skills (entry + content dir) for the install layer to copy
// and record, plus a cleanup func the caller must defer to remove any temp
// clone. cleanup is always non-nil (a no-op when there is nothing to clean).
func fetchToStage(cmd *cobra.Command, home, srcArg string, opts fetchOpts) (skills []stagedSkill, cleanup func(), err error) {
	cleanup = func() {}
	if err := store.EnsureHome(home); err != nil {
		return nil, cleanup, err
	}
	// Materialize config.toml with the built-in defaults on first run so it is
	// the visible, hand-editable source of truth for agent locations. Never
	// clobbers an existing file.
	if err := config.EnsureExists(home); err != nil {
		return nil, cleanup, err
	}

	kind, err := source.Classify(srcArg)
	if err != nil {
		return nil, cleanup, err
	}

	switch kind {
	case source.Git:
		// Clone (and record as the Source) the resolved remote, so a GitHub
		// "owner/repo" shorthand is the same Source as its full HTTPS URL.
		return fetchGitToStage(cmd, home, source.GitRemote(srcArg), opts)
	case source.Local:
		return fetchLocalToStage(home, srcArg, opts)
	default:
		return nil, cleanup, fmt.Errorf("unsupported source kind %s", kind)
	}
}

// fetchGitToStage treeless-clones url, discovers the skills it holds, selects
// which to stage, and materializes each chosen skill's subdir into a staging
// directory under the clone temp. The returned cleanup removes that temp once
// the caller has copied the staged content.
func fetchGitToStage(cmd *cobra.Command, home, url string, opts fetchOpts) (skills []stagedSkill, cleanup func(), err error) {
	cleanup = func() {}
	ctx := cmd.Context()

	// Canonicalize before anything records or compares it: a trailing slash is
	// not part of a repo's identity, and storing the URL exactly as typed made
	// the same repo entered two ways read as two sources.
	url = core.CanonicalRemote(url)

	tmp, err := os.MkdirTemp("", "skillm-clone-")
	if err != nil {
		return nil, cleanup, fmt.Errorf("create temp dir: %w", err)
	}
	// The staged content lives under tmp and must outlive this call, so the
	// caller owns cleanup. On any error below we remove tmp ourselves.
	fail := func(e error) (nil_ []stagedSkill, _ func(), _ error) {
		os.RemoveAll(tmp)
		return nil, func() {}, e
	}

	// git clone wants to create the destination itself; give it a non-existent
	// subpath of our temp dir.
	repoDir := filepath.Join(tmp, "repo")

	if err := gitx.TreelessClone(ctx, url, opts.Ref, repoDir); err != nil {
		return fail(err)
	}

	// Resolve the concrete ref we pinned. When the user gave one we keep it; the
	// default branch is recorded otherwise so `check`/`update` know what to fetch.
	pinnedRef := opts.Ref
	if pinnedRef == "" {
		def, err := gitx.DefaultRef(ctx, repoDir)
		if err != nil {
			return fail(fmt.Errorf("resolve default branch of %s: %w", url, err))
		}
		pinnedRef = def
	}

	found, err := source.DiscoverSkills(repoDir)
	if err != nil {
		return fail(err)
	}
	if len(found) == 0 {
		return fail(fmt.Errorf("no skills found in %s: expected at least one directory containing %s", url, "SKILL.md"))
	}

	chosen, err := selectFound(found, opts.SelectArgs, opts.All)
	if err != nil {
		return fail(err)
	}
	if len(chosen) == 0 {
		ui.Warnf("nothing selected; no skills installed")
		return fail(nil)
	}
	if err := core.CheckAsSingle(opts.As, chosen); err != nil {
		return fail(err)
	}

	st, err := state.Load(home)
	if err != nil {
		return fail(err)
	}

	// Detect every different-source collision before materializing anything, so
	// one clash stages nothing (atomic).
	type plannedGit struct {
		fnd     source.Found
		id      string
		subpath string
	}
	plan := make([]plannedGit, 0, len(chosen))
	for _, fnd := range chosen {
		id := core.ChosenID(fnd, opts.As)
		subpath := core.RepoRelSubpath(repoDir, fnd.Dir)
		ident := core.SrcIdentity{Kind: state.KindGit, Source: url, Path: subpath}
		if cerr := core.RegistryCollision(st, id, ident); cerr != nil {
			return fail(cerr)
		}
		plan = append(plan, plannedGit{fnd: fnd, id: id, subpath: subpath})
	}

	staged := make([]stagedSkill, 0, len(plan))
	for _, it := range plan {
		// Compute the per-skill revision (its subdir tree SHA at the pinned ref)
		// before materializing, so we record exactly what we copied.
		rev, err := gitx.SubtreeSHA(ctx, repoDir, pinnedRef, it.subpath)
		if err != nil {
			return fail(fmt.Errorf("read revision of %q: %w", it.id, err))
		}
		stageDir, err := os.MkdirTemp(tmp, "stage-")
		if err != nil {
			return fail(fmt.Errorf("create staging dir: %w", err))
		}
		if err := gitx.MaterializeSubdir(ctx, repoDir, it.subpath, stageDir); err != nil {
			return fail(fmt.Errorf("materialize skill %q: %w", it.id, err))
		}
		fresh := state.SkillEntry{
			ID:          it.id,
			Kind:        state.KindGit,
			Source:      url,
			Path:        it.subpath,
			Ref:         pinnedRef,
			Revision:    rev,
			InstalledAt: time.Now().UTC(),
		}
		staged = append(staged, stagedSkill{entry: core.MergeEntry(st, it.id, fresh), dir: stageDir})
	}
	return staged, func() { os.RemoveAll(tmp) }, nil
}

// fetchLocalToStage stages a skill (or a selected skill from a local catalog
// directory) as kind=local. Local skills carry no ref/revision and are not
// update-tracked; their content is read from the source directory in place, so
// there is nothing to clean up.
func fetchLocalToStage(home, path string, opts fetchOpts) (skills []stagedSkill, cleanup func(), err error) {
	cleanup = func() {}

	found, err := source.DiscoverSkills(path)
	if err != nil {
		return nil, cleanup, err
	}
	if len(found) == 0 {
		return nil, cleanup, fmt.Errorf("no skills found in %s: expected a directory containing %s", path, "SKILL.md")
	}

	chosen, err := selectFound(found, opts.SelectArgs, opts.All)
	if err != nil {
		return nil, cleanup, err
	}
	if len(chosen) == 0 {
		ui.Warnf("nothing selected; no skills installed")
		return nil, cleanup, nil
	}
	if err := core.CheckAsSingle(opts.As, chosen); err != nil {
		return nil, cleanup, err
	}

	st, err := state.Load(home)
	if err != nil {
		return nil, cleanup, err
	}

	staged := make([]stagedSkill, 0, len(chosen))
	for _, fnd := range chosen {
		id := core.ChosenID(fnd, opts.As)
		ident := core.SrcIdentity{Kind: state.KindLocal, Source: fnd.Dir}
		if cerr := core.RegistryCollision(st, id, ident); cerr != nil {
			return nil, cleanup, cerr
		}
		fresh := state.SkillEntry{
			ID:          id,
			Kind:        state.KindLocal,
			Source:      fnd.Dir,
			InstalledAt: time.Now().UTC(),
		}
		staged = append(staged, stagedSkill{entry: core.MergeEntry(st, id, fresh), dir: fnd.Dir})
	}
	return staged, cleanup, nil
}

// selectFound resolves which of the discovered skills to stage.
//
//   - selectArgs given: stage exactly those skills, in discovery order; an id
//     that is not in the source is an error naming all the unknown ones (atomic).
//   - exactly one skill found (and no selectArgs): stage it without prompting.
//   - --all given: stage every discovered skill.
//   - otherwise: show the interactive picker (which refuses on a non-TTY with a
//     message naming skill_id / --all).
func selectFound(found []source.Found, selectArgs []string, all bool) ([]source.Found, error) {
	if len(selectArgs) > 0 {
		byID := make(map[string]bool, len(found))
		for _, f := range found {
			byID[f.Id] = true
		}
		want := make(map[string]bool, len(selectArgs))
		var missing []string
		for _, a := range selectArgs {
			if !byID[a] {
				missing = append(missing, a)
			}
			want[a] = true
		}
		if len(missing) > 0 {
			return nil, fmt.Errorf("not found in source: %s; available: %s", strings.Join(missing, ", "), foundIDs(found))
		}
		var chosen []source.Found
		for _, f := range found {
			if want[f.Id] {
				chosen = append(chosen, f)
			}
		}
		return chosen, nil
	}

	if len(found) == 1 {
		return found, nil
	}

	if all {
		return found, nil
	}

	opts := make([]ui.Option, 0, len(found))
	for _, f := range found {
		// Label with the skill id only: descriptions wrap to the next line in
		// the picker and clutter the selection.
		opts = append(opts, ui.Option{Label: f.Id, Value: f.Id})
	}

	ids, err := ui.SelectSkills("Select skills to install", opts)
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

// foundIDs joins the discovered skill ids for use in error messages.
func foundIDs(found []source.Found) string {
	ids := make([]string, 0, len(found))
	for _, f := range found {
		ids = append(ids, f.Id)
	}
	return strings.Join(ids, ", ")
}
