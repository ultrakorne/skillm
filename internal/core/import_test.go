package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ultrakorne/skillm/internal/agentdir"
	"github.com/ultrakorne/skillm/internal/lockfile"
	"github.com/ultrakorne/skillm/internal/state"
)

// TestLockEntryMatchesRelativeLocalSource: an older install wrote a local
// Source relative to the project root, while the Registry now records it
// absolute; resolved against the lockfile's root they are the same source,
// so import/update do not warn "different source".
func TestLockEntryMatchesRelativeLocalSource(t *testing.T) {
	root := t.TempDir()
	abs := filepath.Join(root, "skills", "foo")
	entry := &lockfile.Entry{Source: "./skills/foo", SourceType: lockfile.SourceLocal}
	if !lockEntryMatches(state.SkillEntry{Kind: state.KindLocal, Source: abs}, entry, root) {
		t.Error("a relative lockfile source must match the same absolute Registry source")
	}
	if !lockEntryMatches(state.SkillEntry{Kind: state.KindLocal, Source: "./skills/foo"}, entry, root) {
		t.Error("identical relative sources must match")
	}
	other := filepath.Join(t.TempDir(), "skills", "foo")
	if lockEntryMatches(state.SkillEntry{Kind: state.KindLocal, Source: other}, entry, root) {
		t.Error("a different directory must not match")
	}
}

// importFixture returns options for a fresh Home, a project whose
// skills-lock.json names skill alpha from a new git repository, and that
// repository.
func importFixture(t *testing.T) (opts Options, project, repo string) {
	t.Helper()
	home, project, _, _ := localTestSetup(t)
	repo = t.TempDir()
	git := gitRepo(t, repo)
	writeSkill(t, repo, "alpha", "alpha body")
	git("add", "-A")
	git("commit", "-q", "-m", "v1")
	url := "file://" + filepath.ToSlash(repo)
	lf := &lockfile.File{Version: 1, Skills: map[string]*lockfile.Entry{
		"alpha": {Source: url, SourceURL: url, Ref: "main", SourceType: lockfile.SourceGit, SkillPath: "alpha/SKILL.md", ComputedHash: "h"},
		"local": {Source: "../somewhere", SourceType: lockfile.SourceLocal, ComputedHash: "x"},
	}}
	if err := lockfile.Save(project, lf); err != nil {
		t.Fatal(err)
	}
	return Options{Home: home, Cwd: project}, project, repo
}

// TestImportFetchesBeforeLocking: Import fetches without Home's lock and
// writes under it. The lock hook makes the upstream unreachable, so the
// import succeeds only if the fetch happened before the lock was taken.
func TestImportFetchesBeforeLocking(t *testing.T) {
	opts, project, repo := importFixture(t)
	locks := 0
	opts.Lock = func(context.Context) (func(), error) {
		locks++
		if err := os.Rename(repo, repo+".gone"); err != nil {
			t.Fatal(err)
		}
		return func() {}, nil
	}
	rec := &recorder{}

	res, err := Import(context.Background(), opts, rec, ".")
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if locks != 1 {
		t.Fatalf("locks = %d, want one for the write phase", locks)
	}
	if res.Root != project || res.Entries != 2 || !res.ImportedAny() {
		t.Fatalf("result = %+v", res)
	}
	want := map[string]ImportOutcome{"alpha": ImportImported, "local": ImportSkipped}
	for _, s := range res.Skills {
		if want[s.ID] != s.Outcome {
			t.Errorf("%s: outcome %q, want %q", s.ID, s.Outcome, want[s.ID])
		}
		delete(want, s.ID)
	}
	if len(want) != 0 {
		t.Errorf("missing outcomes for %v", want)
	}
	b, err := os.ReadFile(filepath.Join(agentdir.CanonicalSkillDir(project, "alpha"), "SKILL.md"))
	if err != nil || !strings.Contains(string(b), "alpha body") {
		t.Fatalf("project copy: err=%v content=%s", err, b)
	}
	st, _ := state.Load(opts.Home)
	if e, ok := st.Get("alpha"); !ok || e.Ref != "main" || len(e.VendoredAt) != 1 || e.VendoredAt[0] != project {
		t.Fatalf("registry entry = %+v ok=%v", e, ok)
	}
	if len(eventsWith(rec, CodeImported)) != 1 || len(eventsWith(rec, CodeImportSkipped)) != 1 {
		t.Fatalf("events = %+v", rec.events)
	}
}

// TestImportAdoptsSkillInstalledMeanwhile: a skill that another process
// installed from the same source while the import fetched is adopted, not
// imported over it.
func TestImportAdoptsSkillInstalledMeanwhile(t *testing.T) {
	opts, project, repo := importFixture(t)
	url := "file://" + filepath.ToSlash(repo)
	opts.Lock = func(context.Context) (func(), error) {
		st, err := state.Load(opts.Home)
		if err != nil {
			t.Fatal(err)
		}
		st.Upsert(state.SkillEntry{ID: "alpha", Kind: state.KindGit, Source: url, Path: "alpha", Ref: "main", Revision: "recorded"})
		if err := state.Save(opts.Home, st); err != nil {
			t.Fatal(err)
		}
		return func() {}, nil
	}

	res, err := Import(context.Background(), opts, nil, project)
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if len(res.Skills) != 2 || res.Skills[0].ID != "alpha" || res.Skills[0].Outcome != ImportAdopted {
		t.Fatalf("skills = %+v, want alpha adopted", res.Skills)
	}
	st, _ := state.Load(opts.Home)
	if e, _ := st.Get("alpha"); e.Revision != "recorded" {
		t.Fatalf("adopting must keep the recorded entry; revision = %q", e.Revision)
	}
}

// TestImportNoLockfile: a directory without a lockfile imports nothing and
// takes no lock.
func TestImportNoLockfile(t *testing.T) {
	opts := Options{Home: t.TempDir(), Cwd: t.TempDir()}
	opts.Lock = func(context.Context) (func(), error) {
		t.Fatal("nothing to import must not take the lock")
		return nil, nil
	}
	res, err := Import(context.Background(), opts, nil, ".")
	if err != nil || res.Entries != 0 || res.Root != opts.Cwd {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

// TestImportRelativeDirNeedsCwd: with no working directory a relative dir is
// refused instead of being resolved against the process's.
func TestImportRelativeDirNeedsCwd(t *testing.T) {
	if _, err := Import(context.Background(), Options{Home: t.TempDir()}, nil, "."); err == nil {
		t.Fatal("a relative dir with no Cwd must be refused")
	}
}
