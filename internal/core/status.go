package core

import (
	"context"
	"fmt"
	"time"

	"github.com/ultrakorne/skillm/internal/config"
	"github.com/ultrakorne/skillm/internal/selfupdate"
	"github.com/ultrakorne/skillm/internal/state"
	"github.com/ultrakorne/skillm/internal/status"
	"github.com/ultrakorne/skillm/internal/store"
)

// Event codes of the refresh cache (status.json).
const (
	// CodeSelfCheckFailed: refresh could not look up the latest skillm
	// release; the cache records the error and no self update.
	CodeSelfCheckFailed = "self_check_failed"
	// CodeStatusUnreadable: status.json could not be read; it is treated as
	// never written (the next refresh rewrites it).
	CodeStatusUnreadable = "status_unreadable"
	// CodeStatusNotSaved: a command's change was done, but status.json could
	// not be brought in line with it (the next refresh does).
	CodeStatusNotSaved = "status_not_saved"
)

// RefreshRequest says how Refresh runs.
type RefreshRequest struct {
	// Version is the running skillm's build version, which the self-update
	// status is judged for.
	Version string
	// Now is the time the refresh runs at: the cache's checked_at and the
	// base of its next_due_at. Tests inject it.
	Now time.Time
	// IfDue runs the check only when it is due (status.Due): refresh is
	// enabled in config.toml and the interval has passed. Otherwise Refresh
	// returns the cache unchanged, without any network request.
	IfDue bool
}

// StatusResult is the refresh cache as a caller sees it.
type StatusResult struct {
	// Status is the cache; never nil (status.New() when no refresh ever
	// ran). Its self-update status is judged for the running version.
	Status *status.File
	// Stale reports that the cache is older than the refresh interval.
	Stale bool
	// Refreshed reports that Refresh ran a check and rewrote the cache.
	Refreshed bool
}

// Refresh runs a Check of every skill and a self-update lookup (CheckSelf)
// and writes the outcome to the refresh cache, <home>/status.json. It
// reports the Check's events to rep, and a failed self lookup as a warning
// (the cache records it; it never shows as an update).
//
// The lookups run without Home's lock; the cache is then written under it
// (opts.Lock), merged with a fresh read of the Registry, so a skill another
// process changed meanwhile keeps the row that process recorded, a skill
// uninstalled meanwhile is dropped, and one installed meanwhile keeps its
// row. A failed Check (a broken Registry, a cancellation) writes nothing and
// returns its error. The next refresh is due one interval after req.Now, or
// status.RetryAfterFailure after it when every lookup failed.
func Refresh(ctx context.Context, opts Options, rep Reporter, req RefreshRequest) (StatusResult, error) {
	rep = nopIfNil(rep)
	cfg, err := config.Load(opts.Home)
	if err != nil {
		return StatusResult{}, err
	}
	interval := refreshInterval(cfg)
	if req.IfDue {
		prev := loadStatus(opts.Home, rep)
		if !status.Due(prev, req.Now, cfg.RefreshEnabled(), interval, selfupdate.Display(req.Version)) {
			return cachedStatus(prev, req.Version, req.Now, interval), nil
		}
	}

	started := status.Stamp(req.Now)
	checked, err := Check(ctx, opts, rep)
	if err != nil {
		return StatusResult{}, err
	}
	self, selfErr := CheckSelf(ctx, req.Version)
	if err := ctx.Err(); err != nil {
		return StatusResult{}, err
	}
	if selfErr != nil {
		rep.Event(logEvent(LevelWarn, "", CodeSelfCheckFailed, selfErr.Error()))
	}

	unlock, err := opts.lock(ctx)
	if err != nil {
		return StatusResult{}, err
	}
	defer unlock()
	st, err := state.Load(opts.Home)
	if err != nil {
		return StatusResult{}, err
	}
	// An unreadable cache was reported above (with IfDue) or is simply
	// replaced.
	prev, _ := status.Load(opts.Home)

	f := status.New()
	f.CheckedAt = started
	rows := make(map[string]CheckedSkill, len(checked.Skills))
	for _, cs := range checked.Skills {
		if cs.Status != "" {
			rows[cs.ID] = cs
		}
	}
	failed, succeeded := 0, 0
	for _, e := range st.Skills {
		cs, ok := rows[e.ID]
		if ok && e.Kind == state.KindGit {
			if cs.Status == StatusError {
				failed++
			} else {
				succeeded++
			}
		}
		if row, keep := refreshedRow(e, cs, ok, prev); keep {
			f.Skills = append(f.Skills, row)
		}
	}
	f.Self = selfRow(self, selfErr)
	if self.Method != MethodDev {
		if selfErr != nil {
			failed++
		} else {
			succeeded++
		}
	}
	f.NextDueAt = started.Add(interval)
	if failed > 0 && succeeded == 0 {
		f.NextDueAt = started.Add(min(interval, status.RetryAfterFailure))
	}
	if err := store.EnsureHome(opts.Home); err != nil {
		return StatusResult{}, err
	}
	if err := status.Save(opts.Home, f); err != nil {
		return StatusResult{}, fmt.Errorf("save %s: %w", status.FileName, err)
	}
	return StatusResult{Status: f, Stale: status.Stale(f, req.Now, interval), Refreshed: true}, nil
}

// ReadStatus returns the refresh cache offline, with its self-update status
// judged for the running version (so an app upgraded since the last refresh
// shows no stale "upgrade available"), and whether it is older than the
// refresh interval at now. An unreadable cache is reported to rep and read
// as never written.
func ReadStatus(opts Options, rep Reporter, version string, now time.Time) (StatusResult, error) {
	cfg, err := config.Load(opts.Home)
	if err != nil {
		return StatusResult{}, err
	}
	return cachedStatus(loadStatus(opts.Home, nopIfNil(rep)), version, now, refreshInterval(cfg)), nil
}

// RecordSelf writes self (a CheckSelf result, or the release an upgrade just
// installed) into the refresh cache, if there is one. The caller holds
// Home's lock. It changes nothing when no refresh ever ran.
func RecordSelf(opts Options, rep Reporter, self SelfStatus) {
	recordStatus(opts.Home, nopIfNil(rep), func(f *status.File) {
		f.Self = selfRow(self, nil)
	})
}

// refreshInterval is config's refresh interval.
func refreshInterval(cfg *config.Config) time.Duration {
	return time.Duration(cfg.RefreshIntervalHours()) * time.Hour
}

// cachedStatus is the StatusResult for the cache f (nil: never written).
func cachedStatus(f *status.File, version string, now time.Time, interval time.Duration) StatusResult {
	if f == nil {
		f = status.New()
	}
	adjustSelf(f, version)
	return StatusResult{Status: f, Stale: status.Stale(f, now, interval)}
}

// loadStatus reads the cache in home; an unreadable one is reported to rep
// and read as nil (never written).
func loadStatus(home string, rep Reporter) *status.File {
	f, err := status.Load(home)
	if err != nil {
		rep.Event(logEvent(LevelWarn, "", CodeStatusUnreadable, err.Error()))
		return nil
	}
	return f
}

// refreshedRow is the cache row for Registry entry e after a refresh whose
// Check found cs (ok false: e was not checked, e.g. it was installed
// meanwhile). A row the Check judged against a Revision e no longer has
// (another process changed the skill meanwhile) gives way to prev's row when
// prev judged e's current Revision; failing that, it is kept only if the
// Check's upstream Revision is e's (then it is up to date). keep is false
// when there is nothing to say about e.
func refreshedRow(e state.SkillEntry, cs CheckedSkill, ok bool, prev *status.File) (status.Skill, bool) {
	if e.Kind != state.KindGit {
		return status.Skill{ID: e.ID, Status: status.SkillLocal}, true
	}
	if ok && cs.Kind == state.KindGit && cs.InstalledRev == e.Revision {
		return checkedRow(cs), true
	}
	if prev != nil {
		if p, found := prev.Skill(e.ID); found && p.InstalledRev == e.Revision {
			return p, true
		}
	}
	if ok && cs.UpstreamRev != "" && cs.UpstreamRev == e.Revision {
		return upToDateRow(e), true
	}
	return status.Skill{}, false
}

// checkedRow is the cache row for a Check result.
func checkedRow(cs CheckedSkill) status.Skill {
	row := status.Skill{ID: cs.ID, Status: string(cs.Status), InstalledRev: cs.InstalledRev, UpstreamRev: cs.UpstreamRev}
	if cs.Err != nil {
		row.Error = cs.Err.Error()
	}
	return row
}

// upToDateRow is the row of a git skill whose recorded Revision was just
// fetched from upstream.
func upToDateRow(e state.SkillEntry) status.Skill {
	return status.Skill{ID: e.ID, Status: status.SkillUpToDate, InstalledRev: e.Revision, UpstreamRev: e.Revision}
}

// selfRow is the cache's self entry for a CheckSelf result and its error.
func selfRow(s SelfStatus, err error) *status.Self {
	row := &status.Self{
		Current:    s.Current,
		Latest:     s.Latest,
		Available:  s.Available,
		Eligible:   s.Eligible,
		Method:     string(s.Method),
		Executable: s.Executable,
	}
	if err != nil {
		row.Latest, row.Available, row.Eligible = "", false, false
		row.Error = err.Error()
	}
	return row
}

// adjustSelf re-judges f's self entry for the running version when the cache
// was written by another skillm (the app upgraded its bundle, say): the
// latest release found then is compared with the running version, offline.
func adjustSelf(f *status.File, version string) {
	cur := selfupdate.Display(version)
	if f.Self == nil || f.Self.Current == cur {
		return
	}
	method, exe := SelfMethod(version)
	s := f.Self
	s.Current, s.Method, s.Executable = cur, string(method), exe
	s.Available = method != MethodDev && s.Latest != "" && selfupdate.IsNewer(s.Latest, version)
	if method == MethodDev {
		s.Latest = ""
	}
	s.Eligible = s.Available && method == MethodBinary
	f.Recount()
}

// recordStatus brings the refresh cache in home in line with a change a
// command just made, under the Home lock its caller holds: change edits the
// cache, which is then saved. It does nothing when no refresh ever ran. A
// failure never fails the command: it is reported as a warning, and the next
// refresh rewrites the cache.
func recordStatus(home string, rep Reporter, change func(f *status.File)) {
	f, err := status.Load(home)
	if err != nil {
		rep.Event(logEvent(LevelWarn, "", CodeStatusNotSaved, fmt.Sprintf("could not update %s: %v", status.FileName, err)))
		return
	}
	if f == nil {
		return
	}
	change(f)
	if err := status.Save(home, f); err != nil {
		rep.Event(logEvent(LevelWarn, "", CodeStatusNotSaved, fmt.Sprintf("could not update %s: %v", status.FileName, err)))
	}
}

// retainRegistered drops the cache rows of skills no longer in st and puts
// the rest in Registry order.
func retainRegistered(f *status.File, st *state.State) {
	rows := make([]status.Skill, 0, len(f.Skills))
	for _, e := range st.Skills {
		if row, ok := f.Skill(e.ID); ok {
			rows = append(rows, row)
		}
	}
	f.Skills = rows
}

// recordInstalls updates the cache rows of the skills an install just
// landed. fresh says their content was fetched from upstream just now (an
// install from a Source), so a git skill is up to date. Otherwise (id mode)
// a skill whose Revision moved was re-fetched, so it is up to date too, and
// one whose Revision did not move keeps its row.
func recordInstalls(home string, rep Reporter, st *state.State, skills []InstalledSkill, fresh bool) {
	recordStatus(home, rep, func(f *status.File) {
		for _, s := range skills {
			if s.Action == VendorBlocked {
				continue
			}
			e, ok := st.Get(s.ID)
			if !ok {
				continue
			}
			prev, had := f.Skill(e.ID)
			switch {
			case e.Kind != state.KindGit:
				f.SetSkill(status.Skill{ID: e.ID, Status: status.SkillLocal})
			case fresh || (had && prev.InstalledRev != e.Revision):
				f.SetSkill(upToDateRow(e))
			}
		}
		retainRegistered(f, st)
	})
}

// recordUpdates updates the cache rows after an Update: a skill whose fetch
// succeeded is at its upstream Revision now, a local one stays local, and a
// failed one keeps its row (an update that could not be applied is still
// due). Skills Update dropped lose their row.
func recordUpdates(home string, rep Reporter, st *state.State, skills []UpdatedSkill) {
	recordStatus(home, rep, func(f *status.File) {
		for _, s := range skills {
			e, ok := st.Get(s.ID)
			if !ok || s.Outcome == OutcomeFailed {
				continue
			}
			if e.Kind != state.KindGit {
				f.SetSkill(status.Skill{ID: e.ID, Status: status.SkillLocal})
				continue
			}
			f.SetSkill(upToDateRow(e))
		}
		retainRegistered(f, st)
	})
}

// recordUninstalls drops the cache rows of the skills no longer in st.
func recordUninstalls(home string, rep Reporter, st *state.State) {
	recordStatus(home, rep, func(f *status.File) { retainRegistered(f, st) })
}
