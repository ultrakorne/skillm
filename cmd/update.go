package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/ultrakorne/skillm/internal/agentdir"
	"github.com/ultrakorne/skillm/internal/config"
	"github.com/ultrakorne/skillm/internal/gitx"
	"github.com/ultrakorne/skillm/internal/state"
	"github.com/ultrakorne/skillm/internal/store"
	"github.com/ultrakorne/skillm/internal/ui"
)

func init() {
	rootCmd.AddCommand(newUpdateCmd())
}

func newUpdateCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "update [skill_id]",
		Short: "Pull the latest revision of outdated git skills into every install",
		Long: "Update re-fetches the upstream revision of git-sourced skills. With no " +
			"argument it updates every outdated git skill; with a skill id it updates that " +
			"one. When a skill's upstream has advanced, its new content is written straight " +
			"into every recorded install from a single clone: the Global install's " +
			"~/.agents/skills copy and each tracked project's Local install — its " +
			".agents/skills copy is rewritten and its skills-lock.json entry refreshed. An " +
			"all-skills update also adopts skills teammates added to any tracked project's " +
			"skills-lock.json (see `skillm import`). Local-path skills have no upstream and " +
			"are not re-fetched, but their installed copies are re-synced from the recorded " +
			"source directory when it still exists and its content has changed.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var id string
			if len(args) == 1 {
				id = strings.TrimSpace(args[0])
			}
			return runUpdate(cmd.Context(), flagHome, id)
		},
	}
	return c
}

// updateTarget is one git skill considered for update.
type updateTarget struct {
	entry state.SkillEntry
}

func runUpdate(ctx context.Context, homeOverride, id string) error {
	home, err := store.Home(homeOverride)
	if err != nil {
		return err
	}
	if err := store.EnsureHome(home); err != nil {
		return err
	}

	st, err := state.Load(home)
	if err != nil {
		return err
	}
	cfg, err := config.Load(home)
	if err != nil {
		return err
	}

	// An all-skills update first adopts whatever teammates added to the tracked
	// projects' skills-lock.json files (e.g. with `npx skills`), so those skills
	// join this and every future update. An explicit-id update stays surgical.
	if id == "" {
		if autoImportTrackedRoots(ctx, home, st, cfg.EnabledAgents()) {
			if err := state.Save(home, st); err != nil {
				return fmt.Errorf("save registry: %w", err)
			}
		}
	}

	// Resolve the git skills to fetch and the local skills in scope, honouring an
	// explicit id. Local skills have no upstream but their Vendored copies are
	// still re-synced from Home below.
	targets, localIDs, err := selectUpdateTargets(st, id)
	if err != nil {
		return err
	}
	if len(targets) == 0 && len(localIDs) == 0 {
		ui.Successf("Nothing to update.")
		return nil
	}

	// Each git target requires its own treeless clone and tree-SHA comparison.
	// That per-skill network/git work is run concurrently (bounded by ui's
	// fan-out) and shown as a live spinner row per skill with an aggregate
	// progress bar underneath. Because the workers run in parallel, the shared
	// bookkeeping below is guarded by mu; the registry is persisted once after.
	var (
		mu         sync.Mutex
		updated    []string
		updatedSet = map[string]bool{}
		stagedByID = map[string]string{} // updated git skill id → staged content dir
		cleanups   []func()
		failures   []string
		dirty      bool // whether the registry needs persisting
	)
	// The staged clones of updated skills are copied into every install by
	// refreshVendoredCopies below, so they must survive until this function
	// returns — clean them all up at the very end.
	defer func() {
		for _, c := range cleanups {
			c()
		}
	}()

	if len(targets) > 0 {
		labels := make([]string, len(targets))
		for i, t := range targets {
			labels[i] = t.entry.ID
		}

		work := func(ctx context.Context, i int) ui.Result {
			t := targets[i] // copy; updateOne mutates t.entry in place
			changed, staged, clean, upErr := updateOne(ctx, &t.entry)

			mu.Lock()
			defer mu.Unlock()
			if clean != nil {
				cleanups = append(cleanups, clean)
			}
			switch {
			case upErr != nil:
				msg := fmt.Sprintf("%s: %v", t.entry.ID, upErr)
				failures = append(failures, msg)
				return ui.Result{Level: ui.LevelError, Text: msg}
			case changed:
				st.Upsert(t.entry)
				dirty = true
				updated = append(updated, t.entry.ID)
				updatedSet[t.entry.ID] = true
				stagedByID[t.entry.ID] = staged
				return ui.Result{Level: ui.LevelSuccess, Text: fmt.Sprintf("Updated %s.", t.entry.ID)}
			default:
				return ui.Result{Level: ui.LevelSuccess, Text: fmt.Sprintf("%s is already up to date.", t.entry.ID)}
			}
		}

		// The rows themselves report each skill's outcome (Updated / up to date /
		// failed), so there is no separate per-skill print pass afterwards.
		ui.RunChecklistProgress(ctx, labels, work)
		if err := ctx.Err(); err != nil {
			return err
		}
	}

	// Re-sync Vendored copies. A git skill's copies are refreshed only if it was
	// actually updated above; a local skill's copies are refreshed whenever their
	// content differs from Home. Recorded roots whose copies have vanished are
	// reported and pruned. Done after the git pass so updatedSet is complete.
	inScope := make([]string, 0, len(targets)+len(localIDs))
	for _, t := range targets {
		inScope = append(inScope, t.entry.ID)
	}
	inScope = append(inScope, localIDs...)
	// Only enabled agents get links (re)created by the re-sync: a disabled
	// agent's links were removed when it was disabled, and update must not
	// resurrect them. (Uninstall's sweep, by contrast, spans ALL defined
	// agents — removing stale links is safe, creating them is not.)
	if refreshVendoredCopies(home, cfg.EnabledAgents(), st, inScope, updatedSet, stagedByID) {
		dirty = true
	}

	// Persist registry changes once, after all updates, so a mid-batch failure
	// does not lose successfully-updated revisions.
	if dirty {
		if err := state.Save(home, st); err != nil {
			return fmt.Errorf("save registry: %w", err)
		}
	}

	if len(failures) > 0 {
		if len(failures) == 1 {
			return fmt.Errorf("update failed: %s", failures[0])
		}
		return fmt.Errorf("%d skills failed to update", len(failures))
	}
	if len(updated) == 0 {
		ui.Successf("Everything is up to date.")
	}
	return nil
}

// refreshVendoredCopies re-syncs and prunes the installs of the skills in ids
// — the Global copy in ~/.agents/skills and the Local copies in every recorded
// project. A git skill's canonical copies are overwritten from its freshly
// staged clone (staged[id]) only when it was just updated (updated[id] is
// true); a local skill's copies are overwritten from its recorded source
// directory whenever their content differs from it (so an unchanged skill
// produces no git churn), and left untouched with a one-time warning when that
// source directory is gone. Whenever a copy is rewritten, any missing agent
// links are recreated, and a Local copy's skills-lock.json entry is refreshed
// too. A recorded install whose copy has vanished — the project was moved or
// the files were deleted — is reported and pruned; a skill whose last install
// is pruned this way has its registry entry dropped, matching "an entry exists
// only while installed somewhere". It mutates st in place and returns whether
// anything was pruned or dropped (so the caller persists).
func refreshVendoredCopies(home string, agents []agentdir.Agent, st *state.State, ids []string, updated map[string]bool, staged map[string]string) bool {
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}

	changed := false
	var drop []string
	for i := range st.Skills {
		e := &st.Skills[i]
		if !want[e.ID] {
			continue
		}
		isGit := e.Kind == state.KindGit

		// Resolve the content source and whether a refresh is possible: a
		// just-updated git skill's staged clone; a local skill's recorded source
		// directory (skipped, with a one-time warning, when it is gone).
		src := ""
		canRefresh := true
		if isGit {
			src = staged[e.ID] // present only when the skill was updated
		} else {
			src = e.Source
			if !dirExists(src) {
				ui.Warnf("%s is a local skill whose source %s is gone; leaving its installed copies as-is", e.ID, src)
				canRefresh = false
			}
		}

		if e.Global {
			if !vendorCopyExists(home, e.ID, agentdir.Global, "") {
				ui.Warnf("forgetting global install of %s: no copy remains in %s", e.ID, canonicalDisplay(agentdir.Global))
				e.Global = false
				changed = true
			} else if canRefresh && refreshCopy(src, e.ID, agentdir.CanonicalSkillDirAt(agentdir.Global, "", e.ID), "global", isGit, updated[e.ID]) {
				linkVendorAgents(home, e.ID, agents, agentdir.Global, "", agentdir.Global.String())
			}
		}

		if len(e.VendoredAt) > 0 {
			kept := make([]string, 0, len(e.VendoredAt))
			for _, root := range e.VendoredAt {
				if !localCopyExists(home, e.ID, root) {
					ui.Warnf("forgetting local install of %s: no copy remains in %s", e.ID, root)
					changed = true
					continue // prune
				}
				if canRefresh && refreshCopy(src, e.ID, agentdir.CanonicalSkillDir(root, e.ID), root, isGit, updated[e.ID]) {
					localAgents, _ := splitLocalAliased(agents, root)
					linkVendorAgents(home, e.ID, localAgents, agentdir.Local, root, scopeLabel(agentdir.Local, root, ""))
					upsertLockEntry(*e, root)
				}
				kept = append(kept, root)
			}

			if len(kept) == 0 {
				e.VendoredAt = nil
			} else {
				e.VendoredAt = kept
			}
		}

		// The last install was just pruned: drop the entry so the registry never
		// lists a skill that is installed nowhere.
		if !e.Global && len(e.VendoredAt) == 0 {
			drop = append(drop, e.ID)
		}
	}

	for _, id := range drop {
		if st.Remove(id) {
			changed = true
		}
	}
	return changed
}

// refreshCopy overwrites the canonical copy at target from src when it is due:
// always for a just-updated git skill (src is its staged clone), on content
// drift for a local-path skill (src is its recorded source directory). place
// names the install in report lines (a project root or "global"). It returns
// whether the copy was rewritten; a write failure is warned about and reported
// as false, leaving the install recorded so the next update retries.
func refreshCopy(src, id, target, place string, isGit, updated bool) bool {
	switch {
	case isGit && updated:
		if err := store.ReplaceDir(src, target); err != nil {
			ui.Warnf("refresh copy %s: %v", target, err)
			return false
		}
		ui.Successf("refreshed copy of %s (%s)", id, place)
		return true
	case !isGit && !store.DirContentEqual(src, target):
		if err := store.ReplaceDir(src, target); err != nil {
			ui.Warnf("sync copy %s: %v", target, err)
			return false
		}
		ui.Successf("synced copy of %s (%s)", id, place)
		return true
	}
	return false
}

// selectUpdateTargets resolves which skills to process: the git skills to fetch
// (targets) and the local skills in scope (localIDs, whose Vendored copies are
// re-synced but which have no upstream to fetch). With an empty id it returns
// every git skill and every local skill; with an explicit id it returns just
// that one in the appropriate bucket, erroring only if the id is unknown.
func selectUpdateTargets(st *state.State, id string) ([]updateTarget, []string, error) {
	if id != "" {
		entry, ok := st.Get(id)
		if !ok {
			return nil, nil, fmt.Errorf("skill %q is not in the registry; run `skillm list` to see installed skills", id)
		}
		if entry.Kind != state.KindGit {
			return nil, []string{id}, nil
		}
		return []updateTarget{{entry: entry}}, nil, nil
	}

	var targets []updateTarget
	var locals []string
	for _, e := range st.Skills {
		if e.Kind == state.KindGit {
			targets = append(targets, updateTarget{entry: e})
			continue
		}
		locals = append(locals, e.ID)
	}
	return targets, locals, nil
}

// updateOne clones the skill's source treeless and compares the current
// upstream subdir tree SHA against the recorded revision. When they differ it
// materializes the upstream content into a staging directory, rewrites entry's
// Revision and InstalledAt in place, and returns changed=true with the staged
// dir (the source refreshVendoredCopies rewrites every install from) plus a
// cleanup func the caller must call once it has copied the content. cleanup is
// always non-nil. Nothing on disk is touched beyond the temp clone, so a fetch
// failure leaves every existing install intact.
func updateOne(ctx context.Context, entry *state.SkillEntry) (changed bool, stagedDir string, cleanup func(), err error) {
	cleanup = func() {}
	if err := ctx.Err(); err != nil {
		return false, "", cleanup, err
	}

	tmp, err := os.MkdirTemp("", "skillm-update-clone-")
	if err != nil {
		return false, "", cleanup, fmt.Errorf("create temp dir: %w", err)
	}
	fail := func(e error) (bool, string, func(), error) {
		os.RemoveAll(tmp)
		return false, "", func() {}, e
	}

	repoDir := filepath.Join(tmp, "repo")
	if err := gitx.TreelessClone(ctx, entry.Source, entry.Ref, repoDir); err != nil {
		return fail(err)
	}

	// Resolve the ref to compare against. An empty stored ref means the source's
	// default branch was pinned; resolve it from the clone.
	ref := entry.Ref
	if ref == "" {
		ref, err = gitx.DefaultRef(ctx, repoDir)
		if err != nil {
			return fail(err)
		}
	}

	current, err := gitx.SubtreeSHA(ctx, repoDir, ref, entry.Path)
	if err != nil {
		return fail(fmt.Errorf("the skill's subdirectory %q is no longer present upstream (untracked): %w", entry.Path, err))
	}

	if current == entry.Revision {
		os.RemoveAll(tmp)
		return false, "", func() {}, nil
	}

	// Materialize the upstream subdir into a staging directory. The installs are
	// rewritten from it later (after the whole git pass), so the temp must outlive
	// this call — the caller owns cleanup.
	staged := filepath.Join(tmp, "staged")
	if err := gitx.MaterializeSubdir(ctx, repoDir, entry.Path, staged); err != nil {
		return fail(err)
	}

	entry.Revision = current
	entry.InstalledAt = time.Now().UTC()
	return true, staged, func() { os.RemoveAll(tmp) }, nil
}
