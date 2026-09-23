package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ultrakorne/skillm/internal/agentdir"
	"github.com/ultrakorne/skillm/internal/config"
	"github.com/ultrakorne/skillm/internal/gitx"
	"github.com/ultrakorne/skillm/internal/state"
	"github.com/ultrakorne/skillm/internal/store"
)

// Event codes Update reports with, beyond the vendor primitives' own. Each
// fetched skill's ItemDone carries its UpdateOutcome as its Code.
const (
	// CodeInstallForgotten: a recorded install whose copy has vanished was
	// forgotten.
	CodeInstallForgotten = "install_forgotten"
	// CodeSourceMissing: a local skill's source directory is gone, so its
	// copies were left as they are.
	CodeSourceMissing = "source_missing"
	// CodeUpdateSkipped: a skill changed in the Registry while its update was
	// fetched (reinstalled from another source, or moved to another Revision
	// by another process), so the fetched content was not written.
	CodeUpdateSkipped = "update_skipped"
	// CodeUpdateFailed: a skill's fetch failed, so it was not updated. It is
	// reported (as an error log event) when the writes start, besides the
	// fetch's own ItemDone row, so a caller that does not follow the rows
	// still learns which skills failed.
	CodeUpdateFailed = "update_failed"
)

// UpdateOutcome is what Update did for one skill.
type UpdateOutcome string

const (
	// OutcomeUpdated: the upstream Revision advanced and the installs were
	// rewritten from it.
	OutcomeUpdated UpdateOutcome = "updated"
	// OutcomeUpToDate: nothing needed doing.
	OutcomeUpToDate UpdateOutcome = "up_to_date"
	// OutcomeSynced: the Revision did not move (or the skill is local), but a
	// copy that had drifted was re-synced or a missing link was made.
	OutcomeSynced UpdateOutcome = "synced"
	// OutcomePruned: every recorded install had vanished, so the skill was
	// forgotten (its Registry entry dropped).
	OutcomePruned UpdateOutcome = "pruned"
	// OutcomeFailed: the skill could not be updated (Err says why).
	OutcomeFailed UpdateOutcome = "failed"
	// OutcomeDriftCheckSkipped: the skill is at its upstream Revision, but its
	// content could not be staged, so its installs were not checked for
	// drift (Err says why). It is not a failure; the next update retries.
	OutcomeDriftCheckSkipped UpdateOutcome = "drift_check_skipped"
)

// UpdateRequest says what Update updates.
type UpdateRequest struct {
	// ID is the one skill to update. Empty updates every skill, after
	// adopting the tracked projects' lockfiles (AutoImportTrackedRoots).
	ID string
}

// UpdatedSkill is what Update did for one skill.
type UpdatedSkill struct {
	ID      string
	Kind    string
	Outcome UpdateOutcome
	// Revision is the skill's recorded Revision afterwards (git skills).
	Revision string
	// Advanced reports that the skill's upstream Revision advanced and was
	// recorded. It stays true when the skill was then pruned (OutcomePruned),
	// so it is what UpdatedAny counts.
	Advanced bool
	// Pruned lists the installs forgotten because their copy had vanished:
	// "global" or a project root.
	Pruned []string
	// Err is the cause of OutcomeFailed or OutcomeDriftCheckSkipped.
	Err error
	// Warnings are the writes that failed for an install that stays recorded:
	// a canonical copy that could not be rewritten, or a skills-lock.json
	// entry that could not be refreshed. Each was also reported as a warning
	// event. They do not change Outcome: an updated or synced skill with
	// Warnings is a partial success, and the next update retries the stale
	// installs (their content no longer matches upstream).
	Warnings []error
}

// UpdateResult is Update's outcome.
type UpdateResult struct {
	// Imported lists what the all-skills adoption sweep did.
	Imported []ImportedSkill
	// Skills has one entry per skill in scope: the fetched git skills in
	// Registry order, then the local ones.
	Skills []UpdatedSkill
	// Synced reports whether any copy was rewritten or any link made.
	Synced bool
}

// UpdatedAny reports whether any skill's upstream Revision advanced, even
// when that skill was then pruned.
func (r UpdateResult) UpdatedAny() bool {
	for _, s := range r.Skills {
		if s.Advanced {
			return true
		}
	}
	return false
}

// UnknownSkillError means the skill to update is not in the Registry.
type UnknownSkillError struct {
	ID string
}

func (e *UnknownSkillError) Error() string {
	return fmt.Sprintf("skill %q is not in the registry", e.ID)
}

// UpdateFailedError means at least one skill failed to update. The others
// were updated, and the Registry saved, before it was returned.
type UpdateFailedError struct {
	// IDs are the failed skills, in the order they were reported.
	IDs []string
	// Failures has one "<id>: <cause>" line per failed skill.
	Failures []string
}

func (e *UpdateFailedError) Error() string {
	if len(e.Failures) == 1 {
		return "update failed: " + e.Failures[0]
	}
	return fmt.Sprintf("%d skills failed to update", len(e.Failures))
}

// Update pulls the current upstream Revision of git skills into every
// recorded install, and re-syncs local skills' installs from their source
// directories. A git skill's installs are rewritten when its Revision
// advanced, or when their content drifted from the upstream tree; a local
// skill's when their content differs from its source. A recorded install
// whose copy has vanished is forgotten, and a skill left with no install is
// dropped from the Registry. With opts.Force, every surviving install's agent
// links are (re)made, taking over foreign entries at the link paths.
//
// An all-skills update (empty req.ID) first runs AutoImportTrackedRoots.
// Then it fetches every git skill concurrently without Home's lock: rep gets
// an EventBatch naming them, then an ItemStart and an ItemDone per skill
// whose Code is its UpdateOutcome so far. The writes then run under the lock
// (opts.Lock) against a fresh read of config and the Registry, so a skill
// changed by another process meanwhile is judged by its current entry: one
// that process moved to the Revision fetched here is only checked for drift,
// and one it reinstalled from another source or moved to a different Revision
// fails with "run update again" rather than being rolled back.
//
// Cancellation before the writes returns ctx's error with nothing written
// (beyond what the adoption sweep already saved). Every failed skill is also
// reported as a warn or error log event naming it (a failed fetch with code
// CodeUpdateFailed, a skill changed meanwhile with CodeUpdateSkipped). One or
// more failed skills is an *UpdateFailedError, returned after the others
// were written; an
// unknown req.ID is an *UnknownSkillError.
func Update(ctx context.Context, opts Options, rep Reporter, req UpdateRequest) (UpdateResult, error) {
	rep = serialized(rep)
	var res UpdateResult
	if err := store.EnsureHome(opts.Home); err != nil {
		return res, err
	}
	if req.ID == "" {
		imported, err := AutoImportTrackedRoots(ctx, opts, rep)
		res.Imported = imported
		if err != nil {
			return res, err
		}
	}

	// Fetch against the Registry as it is now; the write phase re-reads it.
	if _, err := config.Load(opts.Home); err != nil {
		return res, err
	}
	snap, err := state.Load(opts.Home)
	if err != nil {
		return res, err
	}
	targets, locals, err := selectUpdateTargets(snap, req.ID)
	if err != nil {
		return res, err
	}
	if len(targets) == 0 && len(locals) == 0 {
		return res, nil
	}

	fetched := fetchUpdates(ctx, rep, targets)
	defer func() {
		for _, f := range fetched {
			f.cleanup()
		}
	}()
	if err := ctx.Err(); err != nil {
		return res, err
	}

	unlock, err := opts.lock(ctx)
	if err != nil {
		return res, err
	}
	defer unlock()
	cfg, err := config.Load(opts.Home)
	if err != nil {
		return res, err
	}
	st, err := state.Load(opts.Home)
	if err != nil {
		return res, err
	}

	// Judge every fetch against the skill's current entry.
	var failures, failedIDs []string
	dirty := false
	updated := map[string]bool{}
	staged := map[string]string{}
	skills := make([]UpdatedSkill, 0, len(targets)+len(locals))
	for i, t := range targets {
		f := fetched[i]
		us := UpdatedSkill{ID: t.ID, Kind: t.Kind, Revision: t.Revision}
		e, ok := st.Get(t.ID)
		switch {
		case f.err != nil && !f.driftSkipped:
			us.Outcome, us.Err = OutcomeFailed, f.err
			rep.Event(logEvent(LevelError, t.ID, CodeUpdateFailed, fmt.Sprintf("%s: %v", t.ID, f.err)))
			failures = append(failures, fmt.Sprintf("%s: %v", t.ID, f.err))
			failedIDs = append(failedIDs, t.ID)
		case !ok:
			// Uninstalled meanwhile: there is nothing left to update.
			us.Outcome = OutcomeUpToDate
		case !sameTracking(e, t):
			err := errors.New("it was reinstalled from another source while its update was fetched; run update again")
			rep.Event(logEvent(LevelWarn, t.ID, CodeUpdateSkipped, fmt.Sprintf("skipped %s: %v", t.ID, err)))
			us.Outcome, us.Err = OutcomeFailed, err
			failures = append(failures, fmt.Sprintf("%s: %v", t.ID, err))
			failedIDs = append(failedIDs, t.ID)
		case f.err != nil:
			us.Outcome, us.Err, us.Revision = OutcomeDriftCheckSkipped, f.err, e.Revision
		case e.Revision != t.Revision && f.revision != e.Revision:
			// Another process moved the skill to a Revision other than the one
			// fetched here. Which of the two is newer upstream is unknown, so
			// the fetched tree is neither recorded nor used to repair drift:
			// either could roll the skill back over what the other process
			// wrote. It fails rather than claiming "up to date" because it may
			// not be; running update again settles it.
			err := errors.New("it was changed by another process while its update was fetched; run update again")
			rep.Event(logEvent(LevelWarn, t.ID, CodeUpdateSkipped, fmt.Sprintf("skipped %s: %v", t.ID, err)))
			us.Outcome, us.Err, us.Revision = OutcomeFailed, err, e.Revision
			failures = append(failures, fmt.Sprintf("%s: %v", t.ID, err))
			failedIDs = append(failedIDs, t.ID)
		default:
			// Staged for every successful fetch, advanced or not: an unchanged
			// skill still needs its tree so drifted installs are repaired.
			staged[t.ID] = f.staged
			us.Outcome, us.Revision = OutcomeUpToDate, e.Revision
			if f.revision != e.Revision {
				e.Revision = f.revision
				e.InstalledAt = time.Now().UTC()
				st.Upsert(e)
				dirty = true
				updated[t.ID] = true
				us.Outcome, us.Revision, us.Advanced = OutcomeUpdated, f.revision, true
			}
		}
		skills = append(skills, us)
	}
	for _, id := range locals {
		skills = append(skills, UpdatedSkill{ID: id, Kind: state.KindLocal, Outcome: OutcomeUpToDate})
	}

	// Re-sync the installs. Only enabled agents get links (re)made: a disabled
	// agent's links were removed when it was disabled, and update must not
	// resurrect them. (Uninstall's sweep, by contrast, spans ALL defined
	// agents: removing stale links is safe, creating them is not.)
	ids := make([]string, len(skills))
	for i, s := range skills {
		ids[i] = s.ID
	}
	ref := refreshInstalls(opts, rep, cfg.EnabledAgents(), st, ids, updated, staged)
	if ref.changed {
		dirty = true
	}
	res.Synced = ref.synced
	for i := range skills {
		s := &skills[i]
		s.Pruned = ref.pruned[s.ID]
		s.Warnings = ref.warnings[s.ID]
		switch {
		case s.Outcome == OutcomeFailed:
		case ref.dropped[s.ID]:
			s.Outcome = OutcomePruned
		case s.Outcome == OutcomeUpToDate && ref.syncedIDs[s.ID]:
			s.Outcome = OutcomeSynced
		}
	}
	res.Skills = skills

	// Persist once, after every skill, so a failure part-way does not lose
	// the revisions already updated.
	if dirty {
		if err := state.Save(opts.Home, st); err != nil {
			return res, fmt.Errorf("save registry: %w", err)
		}
	}
	recordUpdates(opts.Home, rep, st, skills)
	if len(failures) > 0 {
		return res, &UpdateFailedError{IDs: failedIDs, Failures: failures}
	}
	return res, nil
}

// sameTracking reports whether the Registry entry e still tracks the same
// upstream as the entry t an update was fetched for.
func sameTracking(e, t state.SkillEntry) bool {
	return e.Kind == t.Kind && e.Source == t.Source && e.Path == t.Path && e.Ref == t.Ref
}

// selectUpdateTargets resolves which skills to process: the git skills to fetch
// (targets) and the local skills in scope (locals, whose installs are
// re-synced but which have no upstream to fetch). With an empty id it returns
// every git skill and every local skill; with an explicit id it returns just
// that one in the appropriate bucket, and an *UnknownSkillError when it is not
// registered.
func selectUpdateTargets(st *state.State, id string) (targets []state.SkillEntry, locals []string, err error) {
	if id != "" {
		entry, ok := st.Get(id)
		if !ok {
			return nil, nil, &UnknownSkillError{ID: id}
		}
		if entry.Kind != state.KindGit {
			return nil, []string{id}, nil
		}
		return []state.SkillEntry{entry}, nil, nil
	}
	for _, e := range st.Skills {
		if e.Kind == state.KindGit {
			targets = append(targets, e)
			continue
		}
		locals = append(locals, e.ID)
	}
	return targets, locals, nil
}

// fetchedUpdate is one git skill's fetch: its upstream Revision and staged
// content, or why they could not be had.
type fetchedUpdate struct {
	revision string
	staged   string
	// driftSkipped marks an err that only cost the drift check (see
	// classifyStagingErr); the skill itself is up to date.
	driftSkipped bool
	err          error
	cleanup      func()
}

// fetchUpdates fetches every target concurrently (bounded by FanOut),
// reporting an EventBatch, then an ItemStart and an ItemDone per target. A
// fetch interrupted by cancellation gets no ItemDone. The staged dirs
// outlive this call; the caller runs every cleanup.
func fetchUpdates(ctx context.Context, rep Reporter, targets []state.SkillEntry) []fetchedUpdate {
	fetched := make([]fetchedUpdate, len(targets))
	for i := range fetched {
		fetched[i].cleanup = func() {}
	}
	if len(targets) == 0 {
		return fetched
	}
	ids := make([]string, len(targets))
	for i, t := range targets {
		ids[i] = t.ID
	}
	rep.Event(Event{Type: EventBatch, Items: ids})
	FanOut(ctx, len(targets), func(i int) {
		t := targets[i]
		rep.Event(Event{Type: EventItemStart, Index: i, Skill: t.ID})
		f := fetchUpdate(ctx, t)
		fetched[i] = f
		if f.err != nil && ctx.Err() != nil {
			return // interrupted, not failed: leave the row unresolved
		}
		ev := fetchEvent(t, f)
		ev.Index = i
		rep.Event(ev)
	})
	return fetched
}

// fetchEvent is the ItemDone event for target t's fetch: the row `skillm
// update` shows while the fetches run, judged against the Registry as it was
// read before them.
func fetchEvent(t state.SkillEntry, f fetchedUpdate) Event {
	ev := Event{Type: EventItemDone, Skill: t.ID}
	switch {
	case f.driftSkipped:
		ev.Level, ev.Code = LevelWarn, string(OutcomeDriftCheckSkipped)
		ev.Text = fmt.Sprintf("%s is already up to date; %v", t.ID, f.err)
	case f.err != nil:
		ev.Level, ev.Code = LevelError, string(OutcomeFailed)
		ev.Text = fmt.Sprintf("%s: %v", t.ID, f.err)
	case f.revision != t.Revision:
		ev.Level, ev.Code = LevelSuccess, string(OutcomeUpdated)
		ev.Text = fmt.Sprintf("Updated %s.", t.ID)
	default:
		ev.Level, ev.Code = LevelSuccess, string(OutcomeUpToDate)
		ev.Text = fmt.Sprintf("%s is already up to date.", t.ID)
	}
	return ev
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

// fetchUpdate clones git skill e's source treeless, reads the current tree
// SHA of its subdir and materializes that subdir into a staging directory —
// unconditionally, even when the revision already matches, because the staged
// tree is what every install is compared against, and that comparison is the
// only way a copy that drifted from its recorded revision is found (the tree
// SHA lives in state.toml, outside the project's git history, so a `git
// checkout` that reverts a committed .agents/skills copy leaves the recorded
// revision ahead of the copy and is invisible to the SHA check alone).
// Nothing on disk is touched beyond the temp clone, so a fetch failure leaves
// every install intact. A staging failure on a skill already at the upstream
// revision is marked driftSkipped, which is not a failed update.
func fetchUpdate(ctx context.Context, e state.SkillEntry) fetchedUpdate {
	f := fetchedUpdate{cleanup: func() {}}
	if err := ctx.Err(); err != nil {
		f.err = err
		return f
	}

	tmp, err := os.MkdirTemp("", "skillm-update-clone-")
	if err != nil {
		f.err = fmt.Errorf("create temp dir: %w", err)
		return f
	}
	fail := func(err error) fetchedUpdate {
		os.RemoveAll(tmp)
		f.err = err
		return f
	}

	repoDir := filepath.Join(tmp, "repo")
	if err := gitx.TreelessClone(ctx, e.Source, e.Ref, repoDir); err != nil {
		return fail(err)
	}

	// Resolve the ref to compare against. An empty stored ref means the source's
	// default branch was pinned; resolve it from the clone.
	ref := e.Ref
	if ref == "" {
		ref, err = gitx.DefaultRef(ctx, repoDir)
		if err != nil {
			return fail(err)
		}
	}

	current, err := gitx.SubtreeSHA(ctx, repoDir, ref, e.Path)
	if err != nil {
		return fail(fmt.Errorf("the skill's subdirectory %q is no longer present upstream (untracked): %w", e.Path, err))
	}

	// The installs are rewritten from the staging dir after every fetch is
	// done, so the temp outlives this call: the caller owns cleanup.
	staged := filepath.Join(tmp, "staged")
	matErr := gitx.MaterializeSubdir(ctx, repoDir, e.Path, staged)
	if matErr == nil {
		// The staged tree is an independent copy, so the clone is dead weight
		// from here on. Dropping it now matters because the staging dir has to
		// outlive every worker: without this, peak temp usage would be bounded
		// by the number of skills rather than by FanOut's concurrency.
		os.RemoveAll(repoDir)
	}

	advanced := current != e.Revision
	if matErr != nil {
		if advanced {
			return fail(classifyStagingErr(matErr, true))
		}
		f.cleanup = func() { os.RemoveAll(tmp) }
		f.revision, f.driftSkipped, f.err = current, true, classifyStagingErr(matErr, false)
		return f
	}
	f.cleanup = func() { os.RemoveAll(tmp) }
	f.revision, f.staged = current, staged
	return f
}

// refreshed is what refreshInstalls did.
type refreshed struct {
	// changed: an install was forgotten or an entry dropped (st changed).
	changed bool
	// synced: a copy was rewritten or a link made.
	synced bool
	// syncedIDs are the skills a copy was rewritten or a link made for.
	syncedIDs map[string]bool
	// pruned lists each skill's forgotten installs ("global" or a root).
	pruned map[string][]string
	// dropped are the skills whose last install was forgotten.
	dropped map[string]bool
	// warnings are each skill's failed writes (already reported as events)
	// for installs that stay recorded.
	warnings map[string][]error
}

// refreshInstalls re-syncs and prunes the installs of the skills in ids — the
// Global copy in ~/.agents/skills and the Local copies in every recorded
// project. A git skill's canonical copies are overwritten from its freshly
// staged clone (staged[id]) whenever it was just updated (updated[id]) or its
// copies have drifted from that staged content; a local skill's copies are
// overwritten from its recorded source directory whenever their content
// differs from it (so an unchanged skill produces no git churn), and left
// untouched with a one-time warning when that source directory is gone.
// Whenever a copy is rewritten, any missing agent links are recreated, and a
// Local copy's skills-lock.json entry is refreshed too. With opts.Force,
// every surviving install's links are (re)made whether or not its copy
// changed, replacing any entry skillm did not create at an agent's link path.
// A recorded install whose copy has vanished — the project was moved or the
// files were deleted — is reported and forgotten; a skill whose last install
// is forgotten this way has its registry entry dropped, matching "an entry
// exists only while installed somewhere". A failed copy or skills-lock.json
// write is reported as it happens and collected in the result's warnings. It
// mutates st in place; the caller
// holds Home's lock and persists st when the result says it changed.
func refreshInstalls(opts Options, rep Reporter, agents []agentdir.Agent, st *state.State, ids []string, updated map[string]bool, staged map[string]string) refreshed {
	rep = nopIfNil(rep)
	r := refreshed{syncedIDs: map[string]bool{}, pruned: map[string][]string{}, dropped: map[string]bool{}, warnings: map[string][]error{}}
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	synced := func(id string) {
		r.synced = true
		r.syncedIDs[id] = true
	}
	warn := func(id string, err error) {
		if err != nil {
			r.warnings[id] = append(r.warnings[id], err)
		}
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
			var ok bool
			if src, ok = localSource(*e, opts.Cwd); !ok {
				rep.Event(logEvent(LevelWarn, e.ID, CodeSourceMissing,
					fmt.Sprintf("%s is a local skill whose source %s is gone; leaving its installed copies as-is", e.ID, e.Source)))
				canRefresh = false
			}
		}

		if e.Global {
			if !CopyExists(opts.Home, e.ID, agentdir.Global, "") {
				rep.Event(logEvent(LevelWarn, e.ID, CodeInstallForgotten,
					fmt.Sprintf("forgetting global install of %s: no copy remains in %s", e.ID, CanonicalDisplay(agentdir.Global))))
				e.Global = false
				r.changed = true
				r.pruned[e.ID] = append(r.pruned[e.ID], agentdir.Global.String())
			} else {
				rewritten := false
				if canRefresh {
					// A failed write is already reported; the install stays recorded.
					var err error
					rewritten, err = RefreshCopy(rep, src, e.ID, agentdir.CanonicalSkillDirAt(agentdir.Global, "", e.ID), "global", isGit, updated[e.ID])
					warn(e.ID, err)
				}
				if rewritten {
					synced(e.ID)
				}
				if (rewritten || opts.Force) && LinkVendorAgents(rep, opts.Home, e.ID, agents, agentdir.Global, "", agentdir.Global.String(), opts.Force) {
					synced(e.ID)
				}
			}
		}

		if len(e.VendoredAt) > 0 {
			kept := make([]string, 0, len(e.VendoredAt))
			for _, root := range e.VendoredAt {
				if !LocalCopyExists(opts.Home, e.ID, root) {
					rep.Event(logEvent(LevelWarn, e.ID, CodeInstallForgotten,
						fmt.Sprintf("forgetting local install of %s: no copy remains in %s", e.ID, root)))
					r.changed = true
					r.pruned[e.ID] = append(r.pruned[e.ID], root)
					continue // prune
				}
				rewritten := false
				if canRefresh {
					var err error
					rewritten, err = RefreshCopy(rep, src, e.ID, agentdir.CanonicalSkillDir(root, e.ID), root, isGit, updated[e.ID])
					warn(e.ID, err)
				}
				if rewritten {
					synced(e.ID)
					warn(e.ID, UpsertLockEntry(rep, *e, root))
				}
				if rewritten || opts.Force {
					localAgents, _ := SplitLocalAliased(agents, root)
					if LinkVendorAgents(rep, opts.Home, e.ID, localAgents, agentdir.Local, root, ScopeLabel(agentdir.Local, root, ""), opts.Force) {
						synced(e.ID)
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
			r.changed = true
			r.dropped[id] = true
		}
	}
	return r
}
