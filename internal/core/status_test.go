package core

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/ultrakorne/skillm/internal/config"
	"github.com/ultrakorne/skillm/internal/selfupdate"
	"github.com/ultrakorne/skillm/internal/state"
	"github.com/ultrakorne/skillm/internal/status"
)

var refreshT0 = time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)

// refreshFixture is a Home with a git skill at its upstream Revision
// ("fresh"), one behind it ("behind") and a local skill ("mine").
func refreshFixture(t *testing.T) (opts Options, upstream string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	repo := t.TempDir()
	gitInit(t, repo)
	writeFile(t, filepath.Join(repo, "fresh", "SKILL.md"), "---\nname: fresh\n---\nbody\n")
	writeFile(t, filepath.Join(repo, "behind", "SKILL.md"), "---\nname: behind\n---\nbody\n")
	gitCommit(t, repo, "init")
	branch := defaultBranch(t, repo)
	freshRev := subtreeSHAViaGit(t, repo, branch, "fresh")
	upstream = subtreeSHAViaGit(t, repo, branch, "behind")

	home := t.TempDir()
	st := &state.State{Skills: []state.SkillEntry{
		{ID: "fresh", Kind: state.KindGit, Source: repo, Path: "fresh", Ref: branch, Revision: freshRev},
		{ID: "behind", Kind: state.KindGit, Source: repo, Path: "behind", Ref: branch,
			Revision: "0000000000000000000000000000000000000000"},
		{ID: "mine", Kind: state.KindLocal, Source: "/src/mine"},
	}}
	if err := state.Save(home, st); err != nil {
		t.Fatal(err)
	}
	return Options{Home: home}, upstream
}

// stubSelfErr makes the release lookup fail.
func stubSelfErr(t *testing.T, exe string) {
	t.Helper()
	prevExe, prevLatest := executablePath, latestRelease
	t.Cleanup(func() { executablePath, latestRelease = prevExe, prevLatest })
	executablePath = func() string { return exe }
	latestRelease = func(ctx context.Context, version string) (*selfupdate.Release, error) {
		return nil, errors.New("dial tcp: no route to host")
	}
}

// TestRefreshWritesCache: a refresh records every skill's status, the self
// status and the badge, and the next one is due an interval later; --if-due
// before then looks nothing up.
func TestRefreshWritesCache(t *testing.T) {
	opts, _ := refreshFixture(t)
	lookups, _ := stubSelf(t, plainExe, "v0.5.0")
	locked := 0
	opts.Lock = func(ctx context.Context) (func(), error) { locked++; return func() {}, nil }

	res, err := Refresh(context.Background(), opts, nil, RefreshRequest{Version: "0.4.0", Now: refreshT0})
	if err != nil {
		t.Fatal(err)
	}
	f := res.Status
	if !res.Refreshed || res.Stale || locked != 1 || *lookups != 1 {
		t.Fatalf("refreshed=%v stale=%v locked=%d lookups=%d", res.Refreshed, res.Stale, locked, *lookups)
	}
	want := map[string]string{"fresh": status.SkillUpToDate, "behind": status.SkillUpdateAvailable, "mine": status.SkillLocal}
	if len(f.Skills) != 3 || f.Skills[0].ID != "fresh" || f.Skills[2].ID != "mine" {
		t.Fatalf("skills = %+v", f.Skills)
	}
	for _, s := range f.Skills {
		if s.Status != want[s.ID] {
			t.Errorf("%s = %s, want %s", s.ID, s.Status, want[s.ID])
		}
	}
	if f.Updates != 1 || !f.Badge || f.Self == nil || !f.Self.Available || f.Self.Latest != "0.5.0" || !f.Self.Eligible {
		t.Fatalf("cache = %+v self = %+v", f, f.Self)
	}
	if !f.CheckedAt.Equal(refreshT0) || !f.NextDueAt.Equal(refreshT0.Add(24*time.Hour)) {
		t.Fatalf("checked_at %v next_due_at %v", f.CheckedAt, f.NextDueAt)
	}
	onDisk, err := status.Load(opts.Home)
	if err != nil || onDisk == nil || onDisk.Updates != 1 {
		t.Fatalf("cache on disk = %+v, %v", onDisk, err)
	}

	// Not due yet: the cache as it is, no lookup, no lock.
	res, err = Refresh(context.Background(), opts, nil, RefreshRequest{Version: "0.4.0", Now: refreshT0.Add(23 * time.Hour), IfDue: true})
	if err != nil || res.Refreshed || *lookups != 1 || locked != 1 || !res.Status.Badge {
		t.Fatalf("not due: refreshed=%v lookups=%d locked=%d err=%v", res.Refreshed, *lookups, locked, err)
	}
	// Due.
	res, err = Refresh(context.Background(), opts, nil, RefreshRequest{Version: "0.4.0", Now: refreshT0.Add(24 * time.Hour), IfDue: true})
	if err != nil || !res.Refreshed || *lookups != 2 {
		t.Fatalf("due: refreshed=%v lookups=%d err=%v", res.Refreshed, *lookups, err)
	}

	// Scheduled checks off: never due.
	cfg := config.Default()
	if err := cfg.Set(config.KeyRefreshEnabled, "false"); err != nil {
		t.Fatal(err)
	}
	if err := config.Save(opts.Home, cfg); err != nil {
		t.Fatal(err)
	}
	res, err = Refresh(context.Background(), opts, nil, RefreshRequest{Version: "0.4.0", Now: refreshT0.Add(30 * 24 * time.Hour), IfDue: true})
	if err != nil || res.Refreshed || !res.Stale {
		t.Fatalf("disabled: refreshed=%v stale=%v err=%v", res.Refreshed, res.Stale, err)
	}
	// A manual refresh still runs.
	if res, err = Refresh(context.Background(), opts, nil, RefreshRequest{Version: "0.4.0", Now: refreshT0.Add(30 * 24 * time.Hour)}); err != nil || !res.Refreshed {
		t.Fatalf("manual refresh with checks off: refreshed=%v err=%v", res.Refreshed, err)
	}
}

// TestRefreshFailedLookups: a failed lookup is an error, never current, and
// no badge; when every lookup failed the next check is due in an hour.
func TestRefreshFailedLookups(t *testing.T) {
	home := t.TempDir()
	st := &state.State{Skills: []state.SkillEntry{
		{ID: "gone", Kind: state.KindGit, Source: filepath.Join(t.TempDir(), "missing.git"), Ref: "main", Revision: "abc"},
	}}
	if err := state.Save(home, st); err != nil {
		t.Fatal(err)
	}
	stubSelfErr(t, plainExe)
	rec := &recorder{}
	res, err := Refresh(context.Background(), Options{Home: home}, rec, RefreshRequest{Version: "0.4.0", Now: refreshT0})
	if err != nil {
		t.Fatal(err)
	}
	f := res.Status
	if len(f.Skills) != 1 || f.Skills[0].Status != status.SkillError || f.Skills[0].Error == "" {
		t.Fatalf("skills = %+v", f.Skills)
	}
	if f.Badge || f.Self.Available || f.Self.Error == "" || len(f.Errors) != 2 {
		t.Fatalf("cache = %+v self = %+v", f, f.Self)
	}
	if !f.NextDueAt.Equal(refreshT0.Add(status.RetryAfterFailure)) {
		t.Fatalf("next_due_at = %v, want an hour after %v", f.NextDueAt, refreshT0)
	}
	warned := false
	for _, ev := range rec.events {
		if ev.Code == CodeSelfCheckFailed && ev.Level == LevelWarn {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("no self_check_failed warning in %+v", rec.events)
	}
}

// TestRefreshCancelledWritesNothing: a cancelled refresh leaves no cache.
func TestRefreshCancelledWritesNothing(t *testing.T) {
	opts, _ := refreshFixture(t)
	stubSelf(t, plainExe, "v0.4.0")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Refresh(ctx, opts, nil, RefreshRequest{Version: "0.4.0", Now: refreshT0}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if f, err := status.Load(opts.Home); f != nil || err != nil {
		t.Fatalf("cache written after cancel: %+v, %v", f, err)
	}
}

// TestRefreshedRow covers the merge with changes other processes made while
// the lookups ran.
func TestRefreshedRow(t *testing.T) {
	e := state.SkillEntry{ID: "a", Kind: state.KindGit, Source: "https://example.com/r.git", Path: "a", Ref: "main", Revision: "new"}
	// checkedAs is the Check result for e as it was when it had Revision rev.
	checkedAs := func(rev, upstream string) CheckedSkill {
		was := e
		was.Revision = rev
		return CheckedSkill{ID: "a", Kind: state.KindGit, Status: StatusUpdateAvailable, InstalledRev: rev, UpstreamRev: upstream, entry: was}
	}
	checkedOld := checkedAs("old", "up")
	prev := status.New()
	prev.Skills = []status.Skill{{ID: "a", Status: status.SkillUpToDate, InstalledRev: "new", UpstreamRev: "new"}}

	// Checked against the current Revision: the check's row.
	if row, ok := refreshedRow(e, checkedAs("new", "up"), true, nil); !ok || row.Status != status.SkillUpdateAvailable {
		t.Fatalf("current: %+v %v", row, ok)
	}
	// Reinstalled meanwhile from another ref whose tree is the same: the
	// check judged the old branch, so the reinstall's row wins, or none.
	other := e
	other.Ref = "dev"
	if row, ok := refreshedRow(other, checkedAs("new", "up"), true, prev); !ok || row.Status != status.SkillUpToDate {
		t.Fatalf("ref changed meanwhile, recorded: %+v %v", row, ok)
	}
	if _, ok := refreshedRow(other, checkedAs("new", "up"), true, nil); ok {
		t.Fatal("a check of another ref must not give the skill a row")
	}
	upOther := other
	upOther.Revision = "up"
	if _, ok := refreshedRow(upOther, checkedAs("new", "up"), true, nil); ok {
		t.Fatal("another ref's upstream Revision must not make the skill up to date")
	}
	// Changed meanwhile: the row the updater recorded wins.
	if row, ok := refreshedRow(e, checkedOld, true, prev); !ok || row.Status != status.SkillUpToDate {
		t.Fatalf("changed meanwhile, recorded: %+v %v", row, ok)
	}
	// Changed meanwhile to the upstream Revision found: up to date.
	moved := e
	moved.Revision = "up"
	if row, ok := refreshedRow(moved, checkedOld, true, nil); !ok || row.Status != status.SkillUpToDate {
		t.Fatalf("changed to upstream: %+v %v", row, ok)
	}
	// Changed meanwhile to something else, nothing recorded: no row.
	if _, ok := refreshedRow(e, checkedOld, true, nil); ok {
		t.Fatal("changed meanwhile to an unknown Revision must not keep the stale row")
	}
	// Installed meanwhile (not checked): the recorded row, else none.
	if row, ok := refreshedRow(e, CheckedSkill{}, false, prev); !ok || row.InstalledRev != "new" {
		t.Fatalf("installed meanwhile: %+v %v", row, ok)
	}
	if _, ok := refreshedRow(e, CheckedSkill{}, false, nil); ok {
		t.Fatal("unchecked skill with no recorded row must have no row")
	}
}

// saveCache writes a cache with the given rows and self status.
func saveCache(t *testing.T, home string, self *status.Self, rows ...status.Skill) {
	t.Helper()
	f := status.New()
	f.CheckedAt, f.NextDueAt = refreshT0, refreshT0.Add(24*time.Hour)
	f.Skills, f.Self = rows, self
	if err := status.Save(home, f); err != nil {
		t.Fatal(err)
	}
}

func loadCache(t *testing.T, home string) *status.File {
	t.Helper()
	f, err := status.Load(home)
	if err != nil || f == nil {
		t.Fatalf("load cache: %+v, %v", f, err)
	}
	return f
}

// TestRecordStatusAfterCommands: update, install and uninstall keep the
// cache in line, so the badge clears without another check.
func TestRecordStatusAfterCommands(t *testing.T) {
	home := t.TempDir()
	rec := &recorder{}

	// No cache: nothing is written.
	st := &state.State{Skills: []state.SkillEntry{{ID: "a", Kind: state.KindGit, Revision: "2"}}}
	recordUpdates(home, rec, st, []UpdatedSkill{{ID: "a", Outcome: OutcomeUpdated}})
	if f, _ := status.Load(home); f != nil {
		t.Fatalf("cache created by an update: %+v", f)
	}

	saveCache(t, home, &status.Self{Current: "0.4.0", Method: "binary"},
		status.Skill{ID: "a", Status: status.SkillUpdateAvailable, InstalledRev: "1", UpstreamRev: "2"},
		status.Skill{ID: "b", Status: status.SkillUpdateAvailable, InstalledRev: "1", UpstreamRev: "2"},
		status.Skill{ID: "c", Status: status.SkillUpdateAvailable, InstalledRev: "1", UpstreamRev: "2"},
	)
	st = &state.State{Skills: []state.SkillEntry{
		{ID: "a", Kind: state.KindGit, Revision: "2"},
		{ID: "b", Kind: state.KindGit, Revision: "1"},
		{ID: "c", Kind: state.KindGit, Revision: "1"},
	}}
	// a updated; b failed (still behind).
	recordUpdates(home, rec, st, []UpdatedSkill{
		{ID: "a", Kind: state.KindGit, Outcome: OutcomeUpdated, Revision: "2"},
		{ID: "b", Kind: state.KindGit, Outcome: OutcomeFailed, Revision: "1"},
	})
	f := loadCache(t, home)
	if s, _ := f.Skill("a"); s.Status != status.SkillUpToDate || s.InstalledRev != "2" {
		t.Fatalf("updated a = %+v", s)
	}
	if s, _ := f.Skill("b"); s.Status != status.SkillUpdateAvailable {
		t.Fatalf("failed b must stay behind: %+v", s)
	}
	if f.Updates != 2 || !f.Badge || !f.CheckedAt.Equal(refreshT0) {
		t.Fatalf("after update: %+v", f)
	}

	// b uninstalled: its row goes.
	st.Remove("b")
	recordUninstalls(home, rec, st)
	f = loadCache(t, home)
	if _, ok := f.Skill("b"); ok || f.Updates != 1 {
		t.Fatalf("after uninstall: %+v", f)
	}

	// c reinstalled from its source (fresh), a new local skill d installed,
	// and an id-mode install of a that copied without re-fetching.
	st.Upsert(state.SkillEntry{ID: "c", Kind: state.KindGit, Revision: "3"})
	st.Upsert(state.SkillEntry{ID: "d", Kind: state.KindLocal})
	recordInstalls(home, rec, st, []InstalledSkill{{ID: "c", Action: VendorRefreshed}, {ID: "d", Action: VendorWrote}}, true)
	recordInstalls(home, rec, st, []InstalledSkill{{ID: "a", Action: VendorWrote}}, false)
	f = loadCache(t, home)
	if s, _ := f.Skill("c"); s.Status != status.SkillUpToDate || s.InstalledRev != "3" {
		t.Fatalf("reinstalled c = %+v", s)
	}
	if s, _ := f.Skill("d"); s.Status != status.SkillLocal {
		t.Fatalf("installed d = %+v", s)
	}
	if s, _ := f.Skill("a"); s.Status != status.SkillUpToDate {
		t.Fatalf("id-mode a = %+v", s)
	}
	if f.Badge || f.Updates != 0 {
		t.Fatalf("badge must clear once nothing is behind: %+v", f)
	}
	if len(rec.events) != 0 {
		t.Fatalf("unexpected events: %+v", rec.events)
	}

	// An id-mode install whose Revision did not move keeps a behind row.
	saveCache(t, home, nil, status.Skill{ID: "a", Status: status.SkillUpdateAvailable, InstalledRev: "2", UpstreamRev: "5"})
	recordInstalls(home, rec, st, []InstalledSkill{{ID: "a", Action: VendorWrote}}, false)
	if s, _ := loadCache(t, home).Skill("a"); s.Status != status.SkillUpdateAvailable {
		t.Fatalf("copy without re-fetch must stay behind: %+v", s)
	}
	// An id-mode install that re-fetched from the source at the same
	// Revision clears a failed lookup's row: the fetch just succeeded.
	saveCache(t, home, nil, status.Skill{ID: "a", Status: status.SkillError, InstalledRev: "2", Error: "offline"})
	recordInstalls(home, rec, st, []InstalledSkill{{ID: "a", Action: VendorWrote, refetched: true}}, false)
	if f := loadCache(t, home); len(f.Errors) != 0 {
		t.Fatalf("re-fetched a keeps its error: %+v", f)
	} else if s, _ := f.Skill("a"); s.Status != status.SkillUpToDate || s.InstalledRev != "2" {
		t.Fatalf("re-fetched a = %+v", s)
	}
}

// TestRecordSelfAndReadStatus: an upgrade clears the self badge, and a cache
// written by an older skillm is re-judged for the running one.
func TestRecordSelfAndReadStatus(t *testing.T) {
	home := t.TempDir()
	stubSelf(t, bundledExe, "v0.5.0")
	saveCache(t, home, &status.Self{Current: "0.4.0", Latest: "0.5.0", Available: true, Method: "bundled"})

	res, err := ReadStatus(Options{Home: home}, nil, "0.4.0", refreshT0.Add(time.Hour))
	if err != nil || !res.Status.Badge || res.Stale {
		t.Fatalf("same version: %+v stale=%v err=%v", res.Status, res.Stale, err)
	}
	// The app upgraded its bundle to 0.5.0 since the last refresh.
	res, err = ReadStatus(Options{Home: home}, nil, "0.5.0", refreshT0.Add(25*time.Hour))
	if err != nil || res.Status.Badge || res.Status.Self.Available || res.Status.Self.Current != "0.5.0" || !res.Stale {
		t.Fatalf("upgraded app: %+v self=%+v stale=%v err=%v", res.Status, res.Status.Self, res.Stale, err)
	}
	// A source build never offers an upgrade.
	res, _ = ReadStatus(Options{Home: home}, nil, "dev", refreshT0)
	if res.Status.Self.Available || res.Status.Self.Latest != "" || res.Status.Self.Method != string(MethodDev) {
		t.Fatalf("dev build: %+v", res.Status.Self)
	}

	// `skillm upgrade` records what it installed.
	RecordSelf(Options{Home: home}, nil, SelfStatus{Current: "0.5.0", Latest: "0.5.0", Method: MethodBinary})
	if f := loadCache(t, home); f.Badge || f.Self.Current != "0.5.0" {
		t.Fatalf("after upgrade: %+v self=%+v", f, f.Self)
	}

	// Never refreshed: an empty, stale status.
	res, err = ReadStatus(Options{Home: t.TempDir()}, nil, "0.4.0", refreshT0)
	if err != nil || !res.Stale || res.Status.Self != nil || res.Status.Skills == nil {
		t.Fatalf("never refreshed: %+v stale=%v err=%v", res.Status, res.Stale, err)
	}
}

// TestReadStatusSharedHome: a skillm inside an app bundle and a terminal
// install at the same version share one Home; each reads the self entry as its own.
func TestReadStatusSharedHome(t *testing.T) {
	home := t.TempDir()
	saveCache(t, home, &status.Self{Current: "0.4.0", Latest: "0.5.0", Available: true, Eligible: true, Method: "binary", Executable: plainExe})

	stubSelf(t, bundledExe, "v0.5.0")
	res, err := ReadStatus(Options{Home: home}, nil, "0.4.0", refreshT0)
	if err != nil {
		t.Fatal(err)
	}
	if s := res.Status.Self; s.Method != string(MethodBundled) || s.Executable != bundledExe || s.Eligible || !s.Available || !res.Status.Badge {
		t.Fatalf("bundled reader: %+v", s)
	}

	stubSelf(t, plainExe, "v0.5.0")
	saveCache(t, home, &status.Self{Current: "0.4.0", Latest: "0.5.0", Available: true, Method: "bundled", Executable: bundledExe})
	res, _ = ReadStatus(Options{Home: home}, nil, "0.4.0", refreshT0)
	if s := res.Status.Self; s.Method != string(MethodBinary) || s.Executable != plainExe || !s.Eligible {
		t.Fatalf("terminal reader: %+v", s)
	}
}

// TestRefreshSharedHomeNotDue: two skillm versions sharing one Home, each on
// its own schedule, do not make each other's scheduled refresh due: only the
// first one checks until the interval passes.
func TestRefreshSharedHomeNotDue(t *testing.T) {
	opts, _ := refreshFixture(t)
	lookups, _ := stubSelf(t, plainExe, "v0.5.0")
	for i, v := range []string{"0.4.0", "0.5.0", "0.4.0", "0.5.0"} {
		res, err := Refresh(context.Background(), opts, nil, RefreshRequest{Version: v, Now: refreshT0.Add(time.Duration(i) * time.Minute), IfDue: true})
		if err != nil {
			t.Fatal(err)
		}
		if res.Refreshed != (i == 0) {
			t.Fatalf("call %d (%s): refreshed = %v", i, v, res.Refreshed)
		}
		if res.Status.Self.Current != v {
			t.Fatalf("call %d (%s): self judged for %s", i, v, res.Status.Self.Current)
		}
	}
	if *lookups != 1 {
		t.Fatalf("lookups = %d, want 1", *lookups)
	}
}

// TestRefreshKeepsNewerCache: a refresh that finishes after one that started
// later keeps the later one's cache; a cache dated in the future (the clock
// went back) is replaced.
func TestRefreshKeepsNewerCache(t *testing.T) {
	opts, _ := refreshFixture(t)
	stubSelf(t, plainExe, "v0.4.0")
	newer := status.New()
	newer.CheckedAt, newer.NextDueAt = refreshT0.Add(time.Second), refreshT0.Add(24*time.Hour+time.Second)
	newer.Skills = []status.Skill{{ID: "fresh", Status: status.SkillUpdateAvailable, InstalledRev: "x", UpstreamRev: "y"}}
	opts.Lock = func(ctx context.Context) (func(), error) {
		// The other refresh saves while this one's lookups run.
		if err := status.Save(opts.Home, newer); err != nil {
			t.Fatal(err)
		}
		return func() {}, nil
	}
	res, err := Refresh(context.Background(), opts, nil, RefreshRequest{Version: "0.4.0", Now: refreshT0})
	if err != nil || !res.Refreshed {
		t.Fatalf("refreshed=%v err=%v", res.Refreshed, err)
	}
	f := loadCache(t, opts.Home)
	if !f.CheckedAt.Equal(newer.CheckedAt) || f.Updates != 1 || !res.Status.CheckedAt.Equal(newer.CheckedAt) {
		t.Fatalf("newer cache replaced: disk %+v result %+v", f, res.Status)
	}

	newer.CheckedAt = refreshT0.Add(48 * time.Hour)
	if _, err := Refresh(context.Background(), opts, nil, RefreshRequest{Version: "0.4.0", Now: refreshT0}); err != nil {
		t.Fatal(err)
	}
	if f := loadCache(t, opts.Home); !f.CheckedAt.Equal(refreshT0) || len(f.Skills) != 3 {
		t.Fatalf("future-dated cache kept: %+v", f)
	}
}
