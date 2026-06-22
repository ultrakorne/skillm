package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ultrakorne/skillm/internal/config"
	"github.com/ultrakorne/skillm/internal/gitx"
	"github.com/ultrakorne/skillm/internal/source"
	"github.com/ultrakorne/skillm/internal/state"
	"github.com/ultrakorne/skillm/internal/store"
	"github.com/ultrakorne/skillm/internal/ui"
)

// This file holds the shared fetch → discover → select → add-to-Home pipeline.
// It is the single implementation both `add` (fetch-only) and `install`'s source
// mode (fetch then install) call, so the two never drift. The caller-specific
// difference — what to do when a chosen Skill ID already lives in Home — is
// parameterized via fetchOpts.reuseSameSource (see fetchToHome).

// fetchOpts parameterizes the shared pipeline. The selection knobs (As/Ref/All/
// SelectArgs) mirror `add`'s flags so install's source mode behaves identically;
// ReuseSameSource toggles the install-only same-Source reuse / different-Source
// collision policy.
type fetchOpts struct {
	// As overrides the Skill ID (resolves a collision). Requires a single
	// selected skill, like `add --as`.
	As string
	// Ref pins a branch/tag/sha for a git source (default: repo default branch).
	Ref string
	// All selects every discovered skill without prompting.
	All bool
	// SelectArgs names the skills to select (the positional skill_id args). When
	// empty and more than one skill is discovered, the interactive picker runs
	// (or refuses on a non-TTY). `add` passes at most one; install's source mode
	// passes the whole variadic tail.
	SelectArgs []string
	// ReuseSameSource switches collision handling. When false (`add`), any chosen
	// id already in Home is a hard error (collisionErr). When true (`install`
	// source mode), a chosen id already in Home from the SAME Source is reused
	// without re-fetching (a notice points at `skillm update`), while the same id
	// from a DIFFERENT Source is a collision error — and different-Source clashes
	// are detected across the whole selection before anything is added, so one
	// clash adds nothing (atomic).
	ReuseSameSource bool
}

// srcIdentity is the Source identity of the current fetch, compared against a
// registry entry to decide whether a same-named skill in Home came from here.
type srcIdentity struct {
	kind   string // state.KindGit / state.KindLocal
	source string // git URL, or local source directory
	path   string // git subpath within the repo ("" for local / repo root)
}

// matches reports whether registry entry e was sourced from the same place as
// this fetch: for git the same repo URL and subpath; for local the same source
// directory (compared by absolute, cleaned path so ./foo and /abs/foo agree).
func (s srcIdentity) matches(e state.SkillEntry) bool {
	if e.Kind != s.kind {
		return false
	}
	switch s.kind {
	case state.KindGit:
		return e.Source == s.source && e.Path == s.path
	case state.KindLocal:
		return sameLocalPath(e.Source, s.source)
	default:
		return false
	}
}

// fetchToHome runs the shared fetch → discover → select → add-to-Home pipeline.
// It ensures Home (and config) exist, classifies srcArg, discovers the skills it
// holds, selects which to add, and makes each chosen skill present in Home. It
// returns the ids of the chosen skills — whether freshly added or reused from an
// existing same-Source Home copy — for the caller (install's source mode) to
// then install.
func fetchToHome(cmd *cobra.Command, home, srcArg string, opts fetchOpts) ([]string, error) {
	if err := store.EnsureHome(home); err != nil {
		return nil, err
	}
	// Materialize config.toml with the built-in defaults on first run so it is
	// the visible, hand-editable source of truth for agent locations. Never
	// clobbers an existing file.
	if err := config.EnsureExists(home); err != nil {
		return nil, err
	}

	kind, err := source.Classify(srcArg)
	if err != nil {
		return nil, err
	}

	switch kind {
	case source.Git:
		return fetchFromGit(cmd, home, srcArg, opts)
	case source.Local:
		return fetchFromLocal(home, srcArg, opts)
	default:
		return nil, fmt.Errorf("unsupported source kind %s", kind)
	}
}

// fetchFromGit treeless-clones url, discovers the skills it holds, selects which
// to add, and ensures each chosen skill is present in Home with a git registry
// entry. It returns the ids of the chosen skills (added or reused).
func fetchFromGit(cmd *cobra.Command, home, url string, opts fetchOpts) ([]string, error) {
	ctx := cmd.Context()

	tmp, err := os.MkdirTemp("", "skillm-clone-")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmp)

	// git clone wants to create the destination itself; give it a non-existent
	// subpath of our temp dir.
	repoDir := filepath.Join(tmp, "repo")

	if err := gitx.TreelessClone(ctx, url, opts.Ref, repoDir); err != nil {
		return nil, err
	}

	// Resolve the concrete ref we pinned. When the user gave one we keep it; the
	// default branch is recorded otherwise so `check`/`update` know what to fetch.
	pinnedRef := opts.Ref
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

	chosen, err := selectFound(found, opts.SelectArgs, opts.All)
	if err != nil {
		return nil, err
	}
	if len(chosen) == 0 {
		ui.Warnf("nothing selected; no skills added")
		return nil, nil
	}
	if err := checkAsSingle(opts.As, chosen); err != nil {
		return nil, err
	}

	st, err := state.Load(home)
	if err != nil {
		return nil, err
	}

	// Plan every chosen skill, deciding add vs reuse. Different-Source collisions
	// are detected here, before anything is materialized, so one clash adds
	// nothing (atomic).
	type gitItem struct {
		fnd     source.Found
		id      string
		subpath string
		reuse   bool
	}
	plan := make([]gitItem, 0, len(chosen))
	for _, fnd := range chosen {
		id := chosenID(fnd, opts.As)
		subpath := repoRelSubpath(repoDir, fnd.Dir)
		ident := srcIdentity{kind: state.KindGit, source: url, path: subpath}
		reuse, cerr := collisionCheck(st, home, id, ident, opts.ReuseSameSource)
		if cerr != nil {
			return nil, cerr
		}
		plan = append(plan, gitItem{fnd: fnd, id: id, subpath: subpath, reuse: reuse})
	}

	ids := make([]string, 0, len(plan))
	for _, it := range plan {
		if it.reuse {
			reuseNotice(it.id)
			ids = append(ids, it.id)
			continue
		}

		// Compute the per-skill revision (its subdir tree SHA at the pinned ref)
		// before materializing, so we record exactly what we copied.
		rev, err := gitx.SubtreeSHA(ctx, repoDir, pinnedRef, it.subpath)
		if err != nil {
			return ids, fmt.Errorf("read revision of %q: %w", it.id, err)
		}

		// Materialize the skill's subdir into a staging dir, then copy it into
		// Home under its id. Staging keeps store.AddSkillDir's collision and
		// cleanup guarantees intact.
		stage, err := os.MkdirTemp(tmp, "stage-")
		if err != nil {
			return ids, fmt.Errorf("create staging dir: %w", err)
		}
		if err := gitx.MaterializeSubdir(ctx, repoDir, it.subpath, stage); err != nil {
			return ids, fmt.Errorf("materialize skill %q: %w", it.id, err)
		}
		if err := store.AddSkillDir(home, it.id, stage); err != nil {
			return ids, err
		}

		st.Upsert(state.SkillEntry{
			ID:          it.id,
			Kind:        state.KindGit,
			Source:      url,
			Path:        it.subpath,
			Ref:         pinnedRef,
			Revision:    rev,
			InstalledAt: time.Now().UTC(),
		})
		if err := state.Save(home, st); err != nil {
			// Roll back the Home copy so registry and disk stay consistent.
			_ = store.RemoveSkillDir(home, it.id)
			return ids, err
		}

		ui.Successf("added %s (from %s)", it.id, url)
		ids = append(ids, it.id)
	}
	return ids, nil
}

// fetchFromLocal copies a skill (or a selected skill from a local catalog
// directory) into Home as kind=local. Local skills carry no ref/revision and are
// not update-tracked (PLAN §3, CONTEXT "Local skill"). It returns the ids of the
// chosen skills (added or reused).
func fetchFromLocal(home, path string, opts fetchOpts) ([]string, error) {
	found, err := source.DiscoverSkills(path)
	if err != nil {
		return nil, err
	}
	if len(found) == 0 {
		return nil, fmt.Errorf("no skills found in %s: expected a directory containing %s", path, "SKILL.md")
	}

	chosen, err := selectFound(found, opts.SelectArgs, opts.All)
	if err != nil {
		return nil, err
	}
	if len(chosen) == 0 {
		ui.Warnf("nothing selected; no skills added")
		return nil, nil
	}
	if err := checkAsSingle(opts.As, chosen); err != nil {
		return nil, err
	}

	st, err := state.Load(home)
	if err != nil {
		return nil, err
	}

	type localItem struct {
		fnd   source.Found
		id    string
		reuse bool
	}
	plan := make([]localItem, 0, len(chosen))
	for _, fnd := range chosen {
		id := chosenID(fnd, opts.As)
		ident := srcIdentity{kind: state.KindLocal, source: fnd.Dir}
		reuse, cerr := collisionCheck(st, home, id, ident, opts.ReuseSameSource)
		if cerr != nil {
			return nil, cerr
		}
		plan = append(plan, localItem{fnd: fnd, id: id, reuse: reuse})
	}

	ids := make([]string, 0, len(plan))
	for _, it := range plan {
		if it.reuse {
			reuseNotice(it.id)
			ids = append(ids, it.id)
			continue
		}

		if err := store.AddSkillDir(home, it.id, it.fnd.Dir); err != nil {
			return ids, err
		}

		st.Upsert(state.SkillEntry{
			ID:          it.id,
			Kind:        state.KindLocal,
			Source:      it.fnd.Dir,
			InstalledAt: time.Now().UTC(),
		})
		if err := state.Save(home, st); err != nil {
			_ = store.RemoveSkillDir(home, it.id)
			return ids, err
		}

		ui.Successf("added %s (local copy of %s)", it.id, it.fnd.Dir)
		ids = append(ids, it.id)
	}
	return ids, nil
}

// collisionCheck decides what the pipeline should do with a chosen skill id
// whose Source identity is ident:
//
//   - not in Home → fresh add (reuse=false, no error);
//   - in Home, add mode (reuseMode=false) → a hard collision error;
//   - in Home from the SAME Source, install source mode → reuse=true (install the
//     existing Home copy without re-fetching);
//   - in Home from a DIFFERENT Source, install source mode → a collision error.
func collisionCheck(st *state.State, home, id string, ident srcIdentity, reuseMode bool) (reuse bool, err error) {
	if !store.Exists(home, id) {
		return false, nil
	}
	if !reuseMode {
		return false, collisionErr(id)
	}
	if e, ok := st.Get(id); ok && ident.matches(e) {
		return true, nil
	}
	return false, differentSourceErr(id)
}

// reuseNotice tells the user a chosen skill was already in Home from the same
// Source, so it was installed from the existing copy rather than re-fetched.
func reuseNotice(id string) {
	ui.Hintf("%s already in Home from this source; installing the existing copy — run `skillm update %s` to refresh", id, id)
}

// chosenID returns the Skill ID to use for a discovered skill: its own id, or
// the --as override when one was given (validated single by checkAsSingle).
func chosenID(fnd source.Found, as string) string {
	if as != "" {
		return as
	}
	return fnd.Id
}

// checkAsSingle enforces that --as is only used when exactly one skill was
// selected, since it renames a single skill.
func checkAsSingle(as string, chosen []source.Found) error {
	if as != "" && len(chosen) > 1 {
		return errors.New("--as overrides a single Skill ID but more than one skill was selected; pick one skill_id or drop --as")
	}
	return nil
}

// selectFound resolves which of the discovered skills to add.
//
//   - selectArgs given: add exactly those skills, in discovery order; an id that
//     is not in the source is an error naming all the unknown ones (atomic).
//   - exactly one skill found (and no selectArgs): add it without prompting.
//   - --all given: add every discovered skill.
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

// collisionErr builds the standard "already exists" error, suggesting the two
// escape hatches from PLAN §3 (update or --as).
func collisionErr(id string) error {
	return fmt.Errorf("skill %q already exists in Home; run `skillm update %s` to refresh it, or pass `--as <name>` to add it under a different id", id, id)
}

// differentSourceErr is the install-source-mode collision: the id is already in
// Home but came from a different Source, so reusing it would be wrong. The user
// renames this one with --as (PLAN §3 install, "Source collision").
func differentSourceErr(id string) error {
	return fmt.Errorf("skill %q already exists in Home from a different source; pass `--as <name>` to add this one under a different id", id)
}

// sameLocalPath reports whether two local source paths refer to the same
// directory, comparing absolute, cleaned forms so "./foo" and "/abs/foo" agree.
func sameLocalPath(a, b string) bool {
	return absClean(a) == absClean(b)
}

// absClean returns p as an absolute, cleaned path, falling back to a plain clean
// when the working directory cannot be resolved.
func absClean(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return filepath.Clean(p)
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
