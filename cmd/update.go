package cmd

import (
	"context"
	"errors"
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
			"skills-lock.json (see `skillm import`). An install whose copy no longer matches " +
			"its upstream is re-synced even when the upstream revision has not moved, so a " +
			"copy reverted by git or edited in place is repaired while its install is still " +
			"recorded. Local-path skills have no " +
			"upstream and are not re-fetched, but their installed copies are re-synced from " +
			"the recorded source directory when it still exists and its content has changed. " +
			"An agent link path occupied by something skillm did not create (a skill " +
			"copied in by hand or by another tool) is left alone (with a warning when its " +
			"copy is re-synced); pass " +
			"--force to replace it with skillm's link and take the skill over.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var id string
			if len(args) == 1 {
				id = strings.TrimSpace(args[0])
			}
			return runUpdate(cmd.Context(), flagHome, id, flagForce)
		},
	}
	return c
}

// updateTarget is one git skill considered for update.
type updateTarget struct {
	entry state.SkillEntry
}

func runUpdate(ctx context.Context, homeOverride, id string, force bool) error {
	home, err := store.Home(homeOverride)
	if err != nil {
		return err
	}
	if err := store.EnsureHome(home); err != nil {
		return err
	}
	// Hold Home's lock from load to save so a concurrent skillm process cannot
	// interleave its writes with ours.
	unlock, err := store.Lock(home)
	if err != nil {
		return err
	}
	defer unlock()

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
		stagedByID = map[string]string{} // git skill id → staged upstream content dir
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
			// Recorded for every successful fetch, advanced or not: an unchanged
			// skill still needs its staged tree so drifted installs are repaired.
			if staged != "" {
				stagedByID[t.entry.ID] = staged
			}
			switch {
			case errors.Is(upErr, errDriftCheckSkipped):
				return ui.Result{Level: ui.LevelWarn, Text: fmt.Sprintf("%s is already up to date; %v", t.entry.ID, upErr)}
			case upErr != nil:
				msg := fmt.Sprintf("%s: %v", t.entry.ID, upErr)
				failures = append(failures, msg)
				return ui.Result{Level: ui.LevelError, Text: msg}
			case changed:
				st.Upsert(t.entry)
				dirty = true
				updated = append(updated, t.entry.ID)
				updatedSet[t.entry.ID] = true
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

	// Re-sync Vendored copies. A git skill's copies are refreshed when it was
	// updated above or when they have drifted from the staged upstream content; a
	// local skill's copies are refreshed whenever their content differs from its
	// source directory. Recorded roots whose copies have vanished are reported and
	// pruned. Done after the git pass so updatedSet and stagedByID are complete.
	inScope := make([]string, 0, len(targets)+len(localIDs))
	for _, t := range targets {
		inScope = append(inScope, t.entry.ID)
	}
	inScope = append(inScope, localIDs...)
	// Only enabled agents get links (re)created by the re-sync: a disabled
	// agent's links were removed when it was disabled, and update must not
	// resurrect them. (Uninstall's sweep, by contrast, spans ALL defined
	// agents — removing stale links is safe, creating them is not.)
	pruned, synced := refreshVendoredCopies(home, cfg.EnabledAgents(), st, inScope, updatedSet, stagedByID, force)
	if pruned {
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
	// Only claim nothing was due when nothing was: a re-synced copy is work done,
	// even though no upstream revision advanced.
	if len(updated) == 0 && !synced {
		ui.Successf("Everything is up to date.")
	}
	return nil
}

// refreshVendoredCopies re-syncs and prunes the installs of the skills in ids
// — the Global copy in ~/.agents/skills and the Local copies in every recorded
// project. A git skill's canonical copies are overwritten from its freshly
// staged clone (staged[id]) whenever it was just updated (updated[id] is true)
// or its copies have drifted from that staged content; a local skill's copies
// are overwritten from its recorded source directory whenever their content
// differs from it (so an unchanged skill produces no git churn), and left
// untouched with a one-time warning when that source directory is gone.
// Whenever a copy is rewritten, any missing agent links are recreated, and a
// Local copy's skills-lock.json entry is refreshed too. With force, every
// surviving install's links are (re)made whether or not its copy changed,
// replacing any entry skillm did not create at an agent's link path. A recorded install whose copy has vanished — the project was moved or
// the files were deleted — is reported and pruned; a skill whose last install
// is pruned this way has its registry entry dropped, matching "an entry exists
// only while installed somewhere". It mutates st in place and returns whether
// anything was pruned or dropped (changed, so the caller persists) and whether
// any copy was actually rewritten or link made (synced, so the caller does not claim
// everything was already up to date).
func refreshVendoredCopies(home string, agents []agentdir.Agent, st *state.State, ids []string, updated map[string]bool, staged map[string]string, force bool) (changed, synced bool) {
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}

	var drop []string
	for i := range st.Skills {
		e := &st.Skills[i]
		if !want[e.ID] {
			continue
		}
		isGit := e.Kind == state.KindGit

		// Resolve the content source and whether a refresh is possible: a git
		// skill's freshly staged upstream tree; a local skill's recorded source
		// directory (skipped, with a one-time warning, when it is gone).
		src := ""
		canRefresh := true
		if isGit {
			src = staged[e.ID] // present for every successful fetch, advanced or not
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
			} else {
				refreshed := canRefresh && refreshCopy(src, e.ID, agentdir.CanonicalSkillDirAt(agentdir.Global, "", e.ID), "global", isGit, updated[e.ID])
				if refreshed {
					synced = true
				}
				if (refreshed || force) && linkVendorAgents(home, e.ID, agents, agentdir.Global, "", agentdir.Global.String(), force) {
					synced = true
				}
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
				refreshed := canRefresh && refreshCopy(src, e.ID, agentdir.CanonicalSkillDir(root, e.ID), root, isGit, updated[e.ID])
				if refreshed {
					synced = true
					upsertLockEntry(*e, root)
				}
				if refreshed || force {
					localAgents, _ := splitLocalAliased(agents, root)
					if linkVendorAgents(home, e.ID, localAgents, agentdir.Local, root, scopeLabel(agentdir.Local, root, ""), force) {
						synced = true
					}
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
	return changed, synced
}

// refreshCopy overwrites the canonical copy at target from src when it is due:
// always for a just-updated git skill (src is its staged clone), and otherwise
// on content drift — for a git skill whose revision did not advance (src is the
// staged clone of the unchanged upstream) as much as for a local-path skill
// (src is its recorded source directory). Comparing content, rather than
// trusting the recorded revision, is what repairs an install that was reverted
// or hand-edited out from under state.toml; comparing it first is what keeps an
// already-correct install from producing pointless git churn. An empty src
// means there is nothing to refresh from (the fetch failed, or the skill was
// not in scope), so nothing is written. place names the install in report lines
// (a project root or "global"). It returns whether the copy was rewritten; a
// write failure is warned about and reported as false, leaving the install
// recorded so the next update retries.
func refreshCopy(src, id, target, place string, isGit, updated bool) bool {
	switch {
	case src == "":
		return false
	case isGit && updated:
		if err := store.ReplaceDir(src, target); err != nil {
			ui.Warnf("refresh copy %s: %v", target, err)
			return false
		}
		ui.Successf("refreshed copy of %s (%s)", id, place)
		return true
	case !store.DirContentEqual(src, target):
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

// errDriftCheckSkipped marks a staging failure on a skill whose upstream
// revision had NOT advanced. The skill is genuinely up to date, so the run must
// not fail over it — the only cost is that its installs go unchecked for drift
// this time round, which the next update retries.
var errDriftCheckSkipped = errors.New("could not stage its content, so its installs were not checked for drift")

// classifyStagingErr labels a MaterializeSubdir failure by whether the skill's
// upstream revision advanced. When it did, the content is genuinely needed and
// the error is fatal as before. When it did not, the skill is up to date and
// only the drift check is lost, so the error is wrapped in errDriftCheckSkipped
// and the run must not fail over it — otherwise a transient local failure (a
// lazily-fetched blob the treeless clone cannot realize, a full disk) would
// turn an up-to-date skill into a failed update, which was impossible before
// installs were compared by content. err must be non-nil.
func classifyStagingErr(err error, advanced bool) error {
	if advanced {
		return err
	}
	return fmt.Errorf("%w: %v", errDriftCheckSkipped, err)
}

// updateOne clones the skill's source treeless, materializes the upstream
// subdir into a staging directory, and compares the current upstream subdir
// tree SHA against the recorded revision. changed is true only when the SHA
// advanced — in which case entry's Revision and InstalledAt are rewritten in
// place — but the staged dir is returned either way, because
// refreshVendoredCopies rewrites every install from it and needs it to detect
// an install whose content drifted from the recorded revision. cleanup (always
// non-nil) must be called by the caller once it has copied the content. Nothing
// on disk is touched beyond the temp clone, so a fetch failure leaves every
// existing install intact. A staging failure on a skill that is already at the
// upstream revision is reported as errDriftCheckSkipped, which the caller must
// not treat as a failed update.
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

	// Materialize the upstream subdir into a staging directory — unconditionally,
	// even when the revision already matches. The staged tree is what every
	// install is compared against below, and that comparison is the only way a
	// copy that drifted from its recorded revision is found: the tree SHA lives
	// in state.toml, outside the project's git history, so a `git checkout` that
	// reverts a committed .agents/skills copy leaves the recorded revision ahead
	// of the copy and is invisible to the SHA check alone. The installs are
	// rewritten from the staging dir later (after the whole git pass), so the
	// temp must outlive this call — the caller owns cleanup.
	staged := filepath.Join(tmp, "staged")
	matErr := gitx.MaterializeSubdir(ctx, repoDir, entry.Path, staged)
	if matErr == nil {
		// The staged tree is an independent copy, so the clone is dead weight
		// from here on. Dropping it now matters because the staging dir has to
		// outlive every worker: without this, peak temp usage would be bounded
		// by the number of skills rather than by ui's fan-out, since the
		// unchanged path used to free its whole temp inline.
		os.RemoveAll(repoDir)
	}

	if current == entry.Revision {
		if matErr != nil {
			return false, "", func() { os.RemoveAll(tmp) }, classifyStagingErr(matErr, false)
		}
		return false, staged, func() { os.RemoveAll(tmp) }, nil
	}

	if matErr != nil {
		return fail(classifyStagingErr(matErr, true))
	}

	entry.Revision = current
	entry.InstalledAt = time.Now().UTC()
	return true, staged, func() { os.RemoveAll(tmp) }, nil
}
