package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ultrakorne/skillm/internal/lockfile"
	"github.com/ultrakorne/skillm/internal/state"
	"github.com/ultrakorne/skillm/internal/store"
)

// writeLockfile writes a skills-lock.json at root with the given entries — the
// shape a teammate's tool (skillm or vercel's `npx skills`) would have
// committed.
func writeLockfile(t *testing.T, root string, entries map[string]*lockfile.Entry) {
	t.Helper()
	f := &lockfile.File{Version: 1, Skills: entries}
	if err := lockfile.Save(root, f); err != nil {
		t.Fatalf("write lockfile: %v", err)
	}
}

// TestImportAdoptsLockfile drives `skillm import` against a repo whose
// skills-lock.json was committed by someone else: the git source is fetched
// into Home at the locked ref, the root is recorded, the missing canonical
// copy is restored, and agent links are created.
func TestImportAdoptsLockfile(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	bin := skillmBinary(t)
	_, url := initSkillRepo(t)
	e := env{home: t.TempDir(), userDir: t.TempDir(), bin: bin}

	// A "cloned repo": lockfile only, no .agents/skills copies (e.g. the
	// teammate gitignored them, or the clone is partial).
	project := evalProject(t, t.TempDir())
	writeLockfile(t, project, map[string]*lockfile.Entry{
		"alpha": {
			Source:       url,
			SourceURL:    url,
			Ref:          "main",
			SourceType:   lockfile.SourceGit,
			SkillPath:    "alpha/SKILL.md",
			ComputedHash: "whatever",
		},
		"not-importable": {
			Source:       "../somewhere",
			SourceType:   lockfile.SourceLocal,
			ComputedHash: "x",
		},
	})

	out := e.runIn(t, project, "import")
	if !strings.Contains(out, "imported alpha") {
		t.Fatalf("import: expected 'imported alpha', got:\n%s", out)
	}
	if !strings.Contains(out, "not-importable") {
		t.Fatalf("import should report the skipped local-path entry:\n%s", out)
	}

	// Home holds alpha with full git tracking.
	if !store.Exists(e.home, "alpha") {
		t.Fatal("alpha not fetched into Home")
	}
	entry, ok := loadState(t, e).Get("alpha")
	if !ok || entry.Kind != state.KindGit || entry.Ref != "main" || entry.Path != "alpha" || len(entry.Revision) < 7 {
		t.Fatalf("alpha registry entry incomplete: %+v ok=%v", entry, ok)
	}
	// The root is recorded and the install materialized.
	if len(entry.VendoredAt) != 1 || entry.VendoredAt[0] != project {
		t.Fatalf("vendored_at = %v, want [%s]", entry.VendoredAt, project)
	}
	if got := loadState(t, e).LocalRoots; len(got) != 1 || got[0] != project {
		t.Fatalf("local_roots = %v, want [%s]", got, project)
	}
	assertVendoredCopy(t, filepath.Join(project, ".agents", "skills", "alpha"), "alpha body")
	assertLinkResolvesToCanonical(t, project, filepath.Join(project, ".claude", "skills", "alpha"), "alpha")

	// Re-running import is a quiet no-op.
	out = e.runIn(t, project, "import")
	if !strings.Contains(out, "nothing new to import") {
		t.Fatalf("second import should be a no-op, got:\n%s", out)
	}
}

// TestImportRespectsExistingCopy: an entry whose copy already exists in the
// repo is adopted without rewriting the copy (reconciling content is update's
// job, not import's).
func TestImportRespectsExistingCopy(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	bin := skillmBinary(t)
	_, url := initSkillRepo(t)
	e := env{home: t.TempDir(), userDir: t.TempDir(), bin: bin}

	project := evalProject(t, t.TempDir())
	writeLockfile(t, project, map[string]*lockfile.Entry{
		"alpha": {Source: url, SourceURL: url, Ref: "main", SourceType: lockfile.SourceGit,
			SkillPath: "alpha/SKILL.md", ComputedHash: "h"},
	})
	// The teammate's committed copy, deliberately different from upstream.
	copyDir := filepath.Join(project, ".agents", "skills", "alpha")
	if err := os.MkdirAll(copyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(copyDir, "SKILL.md"), []byte("TEAMMATE EDIT\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	e.runIn(t, project, "import")

	// Home has upstream content; the repo copy is untouched.
	hb, err := os.ReadFile(filepath.Join(store.SkillDir(e.home, "alpha"), "SKILL.md"))
	if err != nil || !strings.Contains(string(hb), "alpha body") {
		t.Fatalf("Home copy wrong: err=%v content=%s", err, hb)
	}
	cb, err := os.ReadFile(filepath.Join(copyDir, "SKILL.md"))
	if err != nil || !strings.Contains(string(cb), "TEAMMATE EDIT") {
		t.Fatalf("import must not rewrite an existing repo copy: err=%v content=%s", err, cb)
	}
}

// TestUpdateAutoImportsTeammateEntries: an all-skills update sweeps the tracked
// roots' lockfiles and adopts entries skillm does not manage yet — the
// "teammate added a skill with npx skills" flow.
func TestUpdateAutoImportsTeammateEntries(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	bin := skillmBinary(t)
	_, url := initSkillRepo(t)
	e := env{home: t.TempDir(), userDir: t.TempDir(), bin: bin}

	// skillm manages alpha in this project...
	project := evalProject(t, t.TempDir())
	e.run(t, "add", url, "alpha")
	e.runIn(t, project, "install", "alpha", "--local")

	// ...then a teammate adds beta to the same lockfile out-of-band.
	lf, err := lockfile.Load(project)
	if err != nil {
		t.Fatal(err)
	}
	lf.Skills["beta"] = &lockfile.Entry{
		Source: url, SourceURL: url, Ref: "main", SourceType: lockfile.SourceGit,
		SkillPath: "beta/SKILL.md", ComputedHash: "h",
	}
	if err := lockfile.Save(project, lf); err != nil {
		t.Fatal(err)
	}

	out := e.run(t, "update")
	if !strings.Contains(out, "imported beta") {
		t.Fatalf("update should auto-import beta, got:\n%s", out)
	}
	if !store.Exists(e.home, "beta") {
		t.Fatal("beta not fetched into Home by the auto-import")
	}
	entry, ok := loadState(t, e).Get("beta")
	if !ok || len(entry.VendoredAt) != 1 || entry.VendoredAt[0] != project {
		t.Fatalf("beta not recorded at the project: %+v ok=%v", entry, ok)
	}
	assertVendoredCopy(t, filepath.Join(project, ".agents", "skills", "beta"), "beta body")

	// An explicit-id update stays surgical: no adoption sweep.
	lf, _ = lockfile.Load(project)
	lf.Skills["gamma"] = &lockfile.Entry{
		Source: url, SourceURL: url, Ref: "main", SourceType: lockfile.SourceGit,
		SkillPath: "gamma/SKILL.md", ComputedHash: "h",
	}
	if err := lockfile.Save(project, lf); err != nil {
		t.Fatal(err)
	}
	e.run(t, "update", "alpha")
	if store.Exists(e.home, "gamma") {
		t.Fatal("explicit-id update must not auto-import")
	}
}

// TestImportNameCollisionSkipped: a lock entry whose name is already in Home
// from a different source is skipped with a warning, never overwritten.
func TestImportNameCollisionSkipped(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	bin := skillmBinary(t)
	_, url := initSkillRepo(t)
	e := env{home: t.TempDir(), userDir: t.TempDir(), bin: bin}

	// alpha in Home from a LOCAL path source.
	src := t.TempDir()
	writeSkillMD(t, filepath.Join(src, "alpha"), "alpha", "local original")
	e.run(t, "add", filepath.Join(src, "alpha"))

	project := evalProject(t, t.TempDir())
	writeLockfile(t, project, map[string]*lockfile.Entry{
		"alpha": {Source: url, SourceURL: url, Ref: "main", SourceType: lockfile.SourceGit,
			SkillPath: "alpha/SKILL.md", ComputedHash: "h"},
	})

	out := e.runIn(t, project, "import")
	if !strings.Contains(out, "different source") {
		t.Fatalf("import should warn about the collision, got:\n%s", out)
	}
	// Home copy untouched.
	b, err := os.ReadFile(filepath.Join(store.SkillDir(e.home, "alpha"), "SKILL.md"))
	if err != nil || !strings.Contains(string(b), "local original") {
		t.Fatalf("collision must not overwrite the Home copy: err=%v content=%s", err, b)
	}
	entry, _ := loadState(t, e).Get("alpha")
	if len(entry.VendoredAt) != 0 {
		t.Fatalf("collision must not record the root: %v", entry.VendoredAt)
	}
}
