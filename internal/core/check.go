package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ultrakorne/skillm/internal/config"
	"github.com/ultrakorne/skillm/internal/gitx"
	"github.com/ultrakorne/skillm/internal/state"
)

// CheckStatus is one skill's upstream status as found by Check.
type CheckStatus string

const (
	// StatusUpToDate: the upstream Revision matches the recorded one.
	StatusUpToDate CheckStatus = "up_to_date"
	// StatusUpdateAvailable: the upstream Revision differs from the recorded one.
	StatusUpdateAvailable CheckStatus = "update_available"
	// StatusUntracked: the source was fetched but the skill's subdir is gone.
	StatusUntracked CheckStatus = "untracked"
	// StatusLocal: a local skill, which has no upstream to check.
	StatusLocal CheckStatus = "local"
	// StatusError: the upstream Revision could not be determined (Err says why).
	StatusError CheckStatus = "error"
)

// CheckedSkill is one row of a CheckResult. Status is empty for a skill whose
// check was interrupted by cancellation.
type CheckedSkill struct {
	ID           string
	Kind         string
	Status       CheckStatus
	InstalledRev string
	UpstreamRev  string
	// Err is the lookup failure behind StatusUntracked or StatusError.
	Err error
}

// CheckResult lists every registered skill in Registry order.
type CheckResult struct {
	Skills []CheckedSkill
}

// Check compares the upstream Revision of every git skill in the Registry
// with the recorded one. It is read-only and takes no Home lock. Skills are
// checked concurrently; rep gets an EventBatch listing them, then an
// ItemStart and an ItemDone per skill. On cancellation it returns the
// partial result together with ctx's error.
func Check(ctx context.Context, opts Options, rep Reporter) (CheckResult, error) {
	// Config is loaded only to fail the same way as the rest of the CLI on a
	// broken config.toml; check itself needs no agent data.
	if _, err := config.Load(opts.Home); err != nil {
		return CheckResult{}, err
	}
	st, err := state.Load(opts.Home)
	if err != nil {
		return CheckResult{}, err
	}

	res := CheckResult{Skills: make([]CheckedSkill, len(st.Skills))}
	if len(st.Skills) == 0 {
		return res, nil
	}
	rep = serialized(rep)

	ids := make([]string, len(st.Skills))
	for i, e := range st.Skills {
		ids[i] = e.ID
		res.Skills[i] = CheckedSkill{ID: e.ID, Kind: e.Kind, InstalledRev: e.Revision}
	}
	rep.Event(Event{Type: EventBatch, Items: ids})

	FanOut(ctx, len(st.Skills), func(i int) {
		e := st.Skills[i]
		rep.Event(Event{Type: EventItemStart, Index: i, Skill: e.ID})
		cs := checkOne(ctx, e)
		if cerr := ctx.Err(); cerr != nil && errors.Is(cs.Err, cerr) {
			// Interrupted, not failed: leave the row unresolved.
			return
		}
		res.Skills[i] = cs
		ev := checkEvent(e, cs)
		ev.Index = i
		rep.Event(ev)
	})
	return res, ctx.Err()
}

// checkOne determines skill e's upstream status.
func checkOne(ctx context.Context, e state.SkillEntry) CheckedSkill {
	cs := CheckedSkill{ID: e.ID, Kind: e.Kind, InstalledRev: e.Revision}
	if e.Kind != state.KindGit {
		cs.Status = StatusLocal
		return cs
	}
	cur, err := upstreamRevision(ctx, e)
	var notFound *gitx.NotFoundError
	switch {
	case errors.As(err, &notFound):
		cs.Status, cs.Err = StatusUntracked, err
	case err != nil:
		cs.Status, cs.Err = StatusError, err
	case cur == e.Revision:
		cs.Status, cs.UpstreamRev = StatusUpToDate, cur
	default:
		cs.Status, cs.UpstreamRev = StatusUpdateAvailable, cur
	}
	return cs
}

// checkEvent is the ItemDone event for skill e's check result. Its Text is the
// line `skillm check` prints; a failed lookup keeps the CLI's historic
// "untracked" wording whatever the cause, and the cause is in cs.Err.
func checkEvent(e state.SkillEntry, cs CheckedSkill) Event {
	ev := Event{Type: EventItemDone, Skill: e.ID, Code: string(cs.Status)}
	switch cs.Status {
	case StatusLocal:
		ev.Level = LevelWarn
		ev.Text = fmt.Sprintf("%s: local skill — no upstream (edit its source dir and run `skillm update` to re-sync)", e.ID)
	case StatusUpdateAvailable:
		ev.Level = LevelWarn
		ev.Text = fmt.Sprintf("%s: update available (%s)", e.ID, SourceLabel(e.Kind, e.Source, e.Path))
	case StatusUntracked, StatusError:
		ev.Level = LevelError
		ev.Text = fmt.Sprintf("%s: untracked — its subdir was not found upstream (%s)", e.ID, SourceLabel(e.Kind, e.Source, e.Path))
	default:
		ev.Level = LevelSuccess
		ev.Text = fmt.Sprintf("%s: up-to-date", e.ID)
	}
	return ev
}

// upstreamRevision treeless-clones e's source at its pinned ref into a
// temporary directory and returns the current git tree SHA of the skill's
// subdir. The clone is always removed before returning.
func upstreamRevision(ctx context.Context, e state.SkillEntry) (string, error) {
	tmp, err := os.MkdirTemp("", "skillm-check-")
	if err != nil {
		return "", fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmp)

	// git clone creates the destination itself; hand it a non-existent child.
	dest := filepath.Join(tmp, "repo")
	if err := gitx.TreelessClone(ctx, e.Source, e.Ref, dest); err != nil {
		return "", err
	}

	ref := e.Ref
	if ref == "" {
		// No pinned ref: compare against the repository's default branch tip.
		ref, err = gitx.DefaultRef(ctx, dest)
		if err != nil {
			return "", err
		}
	}
	return gitx.SubtreeSHA(ctx, dest, ref, e.Path)
}

// SourceLabel renders a skill's Source for display: the git URL with the
// subpath appended for catalog repos, or the local origin path.
func SourceLabel(kind, source, path string) string {
	if kind == state.KindGit && path != "" {
		return source + "//" + path
	}
	return source
}
