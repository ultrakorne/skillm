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
		Short: "Pull the latest revision of outdated git skills into Home",
		Long: "Update re-fetches the upstream revision of git-sourced skills into Home, " +
			"overwriting the Home copy. With no argument it updates every outdated git skill; " +
			"with a skill id it updates that one. Because agents see skills through symlinks " +
			"into Home, every link updates automatically — no relinking is needed. Local " +
			"skills have no upstream and are skipped (edit them in Home directly).",
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

	// Resolve the set of git skills to consider, honouring an explicit id.
	targets, err := selectUpdateTargets(st, id)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		ui.Successf("Nothing to update.")
		return nil
	}

	// Each target requires its own treeless clone and tree-SHA comparison. That
	// per-skill network/git work is run concurrently (bounded by ui's fan-out)
	// and shown as a live spinner row per skill with an aggregate progress bar
	// underneath. Because the workers run in parallel, the shared bookkeeping
	// below is guarded by mu; the registry is persisted once after they finish.
	var (
		mu       sync.Mutex
		updated  []string
		failures []string
		dirty    bool // whether the registry needs persisting
	)

	labels := make([]string, len(targets))
	for i, t := range targets {
		labels[i] = t.entry.ID
	}

	work := func(ctx context.Context, i int) ui.Result {
		t := targets[i] // copy; updateOne mutates t.entry in place
		changed, upErr := updateOne(ctx, home, &t.entry)

		mu.Lock()
		defer mu.Unlock()
		switch {
		case upErr != nil:
			msg := fmt.Sprintf("%s: %v", t.entry.ID, upErr)
			failures = append(failures, msg)
			return ui.Result{Level: ui.LevelError, Text: msg}
		case changed:
			st.Upsert(t.entry)
			dirty = true
			updated = append(updated, t.entry.ID)
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

// selectUpdateTargets resolves which git skills to process. With an empty id it
// returns every git-sourced skill (local skills are noted and skipped). With an
// explicit id it returns just that skill, erroring if it is unknown and noting
// (without erroring) when it is a local skill that cannot be updated.
func selectUpdateTargets(st *state.State, id string) ([]updateTarget, error) {
	if id != "" {
		entry, ok := st.Get(id)
		if !ok {
			return nil, fmt.Errorf("skill %q is not in the registry; run `skillm list` to see installed skills", id)
		}
		if entry.Kind != state.KindGit {
			ui.Warnf("%s is a local skill and has no upstream — edit it in Home directly.", id)
			return nil, nil
		}
		return []updateTarget{{entry: entry}}, nil
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
	for _, l := range locals {
		ui.Warnf("%s is a local skill and has no upstream — edit it in Home directly.", l)
	}
	return targets, nil
}

// updateOne clones the skill's source treeless, compares the current upstream
// subdir tree SHA against the recorded revision, and — when they differ —
// overwrites the Home copy with the upstream content and rewrites entry's
// Revision and InstalledAt in place. It returns whether the skill was changed.
// The Home copy is only replaced after the new content has been materialized
// into a temp directory, so a fetch failure never destroys the existing copy.
func updateOne(ctx context.Context, home string, entry *state.SkillEntry) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}

	tmp, err := os.MkdirTemp("", "skillm-update-clone-")
	if err != nil {
		return false, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmp)

	repoDir := filepath.Join(tmp, "repo")
	if err := gitx.TreelessClone(ctx, entry.Source, entry.Ref, repoDir); err != nil {
		return false, err
	}

	// Resolve the ref to compare against. An empty stored ref means the source's
	// default branch was pinned; resolve it from the clone.
	ref := entry.Ref
	if ref == "" {
		ref, err = gitx.DefaultRef(ctx, repoDir)
		if err != nil {
			return false, err
		}
	}

	current, err := gitx.SubtreeSHA(ctx, repoDir, ref, entry.Path)
	if err != nil {
		return false, fmt.Errorf("the skill's subdirectory %q is no longer present upstream (untracked): %w", entry.Path, err)
	}

	if current == entry.Revision {
		return false, nil
	}

	// Materialize the upstream subdir into a staging directory before touching
	// the existing Home copy, so a failure here leaves Home untouched.
	staged := filepath.Join(tmp, "staged")
	if err := gitx.MaterializeSubdir(ctx, repoDir, entry.Path, staged); err != nil {
		return false, err
	}

	// Replace the Home copy: remove the old directory, then copy the staged
	// content in under the same id. store.AddSkillDir refuses to overwrite an
	// existing id, so the prior copy must be removed first.
	if err := store.RemoveSkillDir(home, entry.ID); err != nil {
		return false, err
	}
	if err := store.AddSkillDir(home, entry.ID, staged); err != nil {
		return false, fmt.Errorf("install updated skill %q into Home: %w", entry.ID, err)
	}

	entry.Revision = current
	entry.InstalledAt = time.Now().UTC()
	return true, nil
}
