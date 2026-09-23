package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ultrakorne/skillm/internal/agentdir"
	"github.com/ultrakorne/skillm/internal/config"
	"github.com/ultrakorne/skillm/internal/state"
)

// makeCopy writes a minimal canonical copy of id at (scope, base).
func makeCopy(t *testing.T, scope agentdir.Scope, base, id string) {
	t.Helper()
	writeFile(t, filepath.Join(agentdir.CanonicalSkillDirAt(scope, base, id), "SKILL.md"), "body\n")
}

// TestRefreshInstallsDropsEntryWhenLastInstallPruned verifies the model
// invariant "an entry exists only while installed somewhere": when update finds
// a recorded install whose canonical copy has vanished, it prunes the install —
// and if that was the skill's last one, the registry entry is dropped too.
func TestRefreshInstallsDropsEntryWhenLastInstallPruned(t *testing.T) {
	home := t.TempDir()
	// A git skill recorded as installed at a project root whose copy does NOT
	// exist (the project was moved or the files were deleted).
	gone := filepath.Join(t.TempDir(), "gone")
	st := &state.State{Skills: []state.SkillEntry{{
		ID: "alpha", Kind: state.KindGit, Source: "u", Path: "alpha", Ref: "main", Revision: "r",
		VendoredAt: []string{gone},
	}}}
	agents := config.Default().AllAgents()

	r := refreshInstalls(Options{Home: home}, nil, agents, st, []string{"alpha"}, map[string]bool{}, map[string]string{})
	if !r.changed {
		t.Fatal("expected a change (vanished install pruned, entry dropped)")
	}
	if _, ok := st.Get("alpha"); ok {
		t.Fatal("entry must be dropped once its last install is pruned")
	}
	if !r.dropped["alpha"] || len(r.pruned["alpha"]) != 1 || r.pruned["alpha"][0] != gone {
		t.Fatalf("dropped=%v pruned=%v, want alpha dropped with %s pruned", r.dropped, r.pruned, gone)
	}
}

// TestRefreshInstallsKeepsEntryWithRemainingInstall verifies the converse: a
// skill with a still-present global install is NOT dropped when one of its
// project installs is pruned.
func TestRefreshInstallsKeepsEntryWithRemainingInstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", t.TempDir()) // so the global canonical copy lands in the sandbox
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	gone := filepath.Join(t.TempDir(), "gone")
	st := &state.State{Skills: []state.SkillEntry{{
		ID: "alpha", Kind: state.KindGit, Source: "u", Path: "alpha", Ref: "main", Revision: "r",
		Global: true, VendoredAt: []string{gone},
	}}}
	agents := config.Default().AllAgents()

	// Materialize the global canonical copy so the global install survives while
	// the vanished project install is pruned.
	makeCopy(t, agentdir.Global, "", "alpha")

	r := refreshInstalls(Options{Home: home}, nil, agents, st, []string{"alpha"}, map[string]bool{}, map[string]string{})
	e, ok := st.Get("alpha")
	if !ok {
		t.Fatal("entry must survive while the global install remains")
	}
	if len(e.VendoredAt) != 0 {
		t.Fatalf("the vanished project install should be pruned; VendoredAt = %v", e.VendoredAt)
	}
	if !e.Global {
		t.Fatal("the intact global install must stay recorded")
	}
	if r.dropped["alpha"] {
		t.Fatal("alpha must not be reported dropped")
	}
}

// TestClassifyStagingErr pins the rule that keeps an up-to-date skill from
// failing the run: materializing the upstream tree is now unconditional (it is
// what installs are compared against), so its failure must stay fatal only when
// the revision actually advanced and the content is genuinely needed.
func TestClassifyStagingErr(t *testing.T) {
	boom := errors.New("no space left on device")

	advanced := classifyStagingErr(boom, true)
	if errors.Is(advanced, errDriftCheckSkipped) {
		t.Fatal("a staging failure on an advanced revision must stay fatal")
	}
	if !errors.Is(advanced, boom) {
		t.Fatalf("the underlying cause must survive; got %v", advanced)
	}

	unchanged := classifyStagingErr(boom, false)
	if !errors.Is(unchanged, errDriftCheckSkipped) {
		t.Fatalf("a staging failure on an unchanged revision must be non-fatal; got %v", unchanged)
	}
	if !strings.Contains(unchanged.Error(), boom.Error()) {
		t.Fatalf("the reported message must name the cause; got %v", unchanged)
	}
}

// TestRefreshInstallsForceTakesOverAgentDir: a skill copied by hand into an
// agent folder blocks skillm's link on a plain update, and Force replaces it
// with the link even though the canonical copy is already in sync.
func TestRefreshInstallsForceTakesOverAgentDir(t *testing.T) {
	home, root, _, _ := localTestSetup(t)
	claude := agentdir.Agent{Name: "claude", Global: filepath.Join(t.TempDir(), ".claude", "skills"), Local: ".claude/skills"}
	agents := []agentdir.Agent{claude}
	st := &state.State{Skills: []state.SkillEntry{{
		ID: "alpha", Kind: state.KindGit, Source: "u", Path: "alpha", Ref: "main", Revision: "r",
		VendoredAt: []string{root},
	}}}
	makeCopy(t, agentdir.Local, root, "alpha")
	lp, _ := agentdir.LinkPath(claude, agentdir.Local, root, "alpha")
	if err := os.MkdirAll(lp, 0o755); err != nil {
		t.Fatal(err)
	}

	// Copy in sync, no Force: the foreign dir stays.
	staged := map[string]string{"alpha": agentdir.CanonicalSkillDir(root, "alpha")}
	refreshInstalls(Options{Home: home}, nil, agents, st, []string{"alpha"}, map[string]bool{}, staged)
	if fi, err := os.Lstat(lp); err != nil || fi.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("plain update must leave the foreign dir alone (err=%v)", err)
	}

	r := refreshInstalls(Options{Home: home, Force: true}, nil, agents, st, []string{"alpha"}, map[string]bool{}, staged)
	if !r.synced || !r.syncedIDs["alpha"] {
		t.Fatal("a takeover is work done: synced must be true so update does not claim everything was up to date")
	}
	fi, err := os.Lstat(lp)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("Force must replace the foreign dir with a link (err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(lp, "SKILL.md")); err != nil {
		t.Fatalf("link does not resolve to the canonical copy: %v", err)
	}
}

// updateFixture installs skill alpha globally from a fresh git repository,
// then commits a v2 of it upstream. It returns the options (Home, a
// sandboxed HOME), the repository and a git runner in it.
func updateFixture(t *testing.T) (opts Options, repo string, git func(args ...string) string) {
	t.Helper()
	home, cwd, _, _ := localTestSetup(t)
	repo = t.TempDir()
	git = gitRepo(t, repo)
	writeSkill(t, repo, "alpha", "alpha v1")
	git("add", "-A")
	git("commit", "-q", "-m", "v1")

	opts = Options{Home: home, Cwd: cwd}
	insp, err := Inspect(context.Background(), opts, "file://"+filepath.ToSlash(repo), "")
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	defer insp.Close()
	if _, err := InstallSkills(context.Background(), opts, nil, InstallRequest{Inspection: insp, IDs: []string{"alpha"}, Scope: agentdir.Global}); err != nil {
		t.Fatalf("InstallSkills: %v", err)
	}

	writeSkill(t, repo, "alpha", "alpha v2")
	git("commit", "-q", "-am", "v2")
	return opts, repo, git
}

// globalBody reads the global canonical copy of alpha.
func globalBody(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(agentdir.CanonicalSkillDirAt(agentdir.Global, "", "alpha"), "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestUpdateFetchesBeforeLocking: Update fetches without Home's lock and
// writes under it. The lock hook makes the upstream unreachable, so the
// update succeeds only if every fetch happened before the lock was taken.
func TestUpdateFetchesBeforeLocking(t *testing.T) {
	opts, repo, git := updateFixture(t)
	v2 := git("rev-parse", "HEAD:alpha")
	locks, unlocks := 0, 0
	opts.Lock = func(context.Context) (func(), error) {
		locks++
		if err := os.Rename(repo, repo+".gone"); err != nil {
			t.Fatal(err)
		}
		return func() { unlocks++ }, nil
	}
	rec := &recorder{}

	res, err := Update(context.Background(), opts, rec, UpdateRequest{ID: "alpha"})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if locks != 1 || unlocks != 1 {
		t.Fatalf("locks=%d unlocks=%d, want one lock for the write phase", locks, unlocks)
	}
	if len(res.Skills) != 1 || res.Skills[0].Outcome != OutcomeUpdated || res.Skills[0].Revision != v2 {
		t.Fatalf("result = %+v, want alpha updated to %s", res.Skills, v2)
	}
	if !strings.Contains(globalBody(t), "alpha v2") {
		t.Fatalf("global copy not updated:\n%s", globalBody(t))
	}
	st, _ := state.Load(opts.Home)
	if e, _ := st.Get("alpha"); e.Revision != v2 || !e.Global {
		t.Fatalf("registry entry = %+v, want revision %s, global", e, v2)
	}
	var types []EventType
	for _, ev := range rec.events {
		if ev.Type != EventLog {
			types = append(types, ev.Type)
		}
	}
	last := rec.events[len(rec.events)-1]
	if len(types) != 3 || types[0] != EventBatch || types[1] != EventItemStart || types[2] != EventItemDone {
		t.Fatalf("event types = %v, want batch, item_start, item_done", types)
	}
	for _, ev := range rec.events {
		if ev.Type == EventItemDone && (ev.Code != string(OutcomeUpdated) || ev.Text != "Updated alpha.") {
			t.Fatalf("ItemDone = %+v", ev)
		}
	}
	if last.Type != EventLog || last.Code != CodeCopyRefreshed {
		t.Fatalf("the copy refresh must be reported after the fetches; last event = %+v", last)
	}
}

// TestUpdateJudgesAgainstCurrentEntry: a skill another process updated while
// this update fetched is not "updated" again, and its entry is kept as that
// process wrote it.
func TestUpdateJudgesAgainstCurrentEntry(t *testing.T) {
	opts, _, git := updateFixture(t)
	v2 := git("rev-parse", "HEAD:alpha")
	opts.Lock = func(context.Context) (func(), error) {
		st, err := state.Load(opts.Home)
		if err != nil {
			t.Fatal(err)
		}
		e, _ := st.Get("alpha")
		e.Revision = v2
		st.Upsert(e)
		if err := state.Save(opts.Home, st); err != nil {
			t.Fatal(err)
		}
		return func() {}, nil
	}

	res, err := Update(context.Background(), opts, nil, UpdateRequest{ID: "alpha"})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	// The copy still holds v1, so the drift check re-syncs it.
	if len(res.Skills) != 1 || res.Skills[0].Outcome != OutcomeSynced {
		t.Fatalf("result = %+v, want alpha synced (not updated again)", res.Skills)
	}
	if !strings.Contains(globalBody(t), "alpha v2") {
		t.Fatalf("global copy not synced:\n%s", globalBody(t))
	}
}

// TestUpdateSkipsReinstalledSkill: a skill reinstalled from another source
// while its update was fetched is not overwritten with the old source's
// content; the update reports it as failed.
func TestUpdateSkipsReinstalledSkill(t *testing.T) {
	opts, _, _ := updateFixture(t)
	opts.Lock = func(context.Context) (func(), error) {
		st, err := state.Load(opts.Home)
		if err != nil {
			t.Fatal(err)
		}
		e, _ := st.Get("alpha")
		e.Source = "file:///elsewhere"
		st.Upsert(e)
		if err := state.Save(opts.Home, st); err != nil {
			t.Fatal(err)
		}
		return func() {}, nil
	}

	res, err := Update(context.Background(), opts, nil, UpdateRequest{ID: "alpha"})
	var failed *UpdateFailedError
	if !errors.As(err, &failed) || len(failed.Failures) != 1 {
		t.Fatalf("err = %v, want an UpdateFailedError for alpha", err)
	}
	if len(res.Skills) != 1 || res.Skills[0].Outcome != OutcomeFailed {
		t.Fatalf("result = %+v, want alpha failed", res.Skills)
	}
	if !strings.Contains(globalBody(t), "alpha v1") {
		t.Fatalf("a reinstalled skill's copy must not be overwritten:\n%s", globalBody(t))
	}
}

// TestUpdateUnknownID: an id that is not registered is an
// *UnknownSkillError, and nothing is locked.
func TestUpdateUnknownID(t *testing.T) {
	opts := Options{Home: t.TempDir()}
	opts.Lock = func(context.Context) (func(), error) {
		t.Fatal("an unknown id must not take the lock")
		return nil, nil
	}
	_, err := Update(context.Background(), opts, nil, UpdateRequest{ID: "nope"})
	var unknown *UnknownSkillError
	if !errors.As(err, &unknown) || unknown.ID != "nope" {
		t.Fatalf("err = %v, want an UnknownSkillError for nope", err)
	}
}

// TestUpdateCancelledWritesNothing: a cancelled update returns ctx's error
// without taking the lock or writing anything.
func TestUpdateCancelledWritesNothing(t *testing.T) {
	opts, _, _ := updateFixture(t)
	opts.Lock = func(context.Context) (func(), error) {
		t.Fatal("a cancelled update must not take the lock")
		return nil, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Update(ctx, opts, nil, UpdateRequest{ID: "alpha"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if !strings.Contains(globalBody(t), "alpha v1") {
		t.Fatalf("a cancelled update must not write:\n%s", globalBody(t))
	}
}
