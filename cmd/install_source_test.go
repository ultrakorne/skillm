package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ultrakorne/skillm/internal/state"
	"github.com/ultrakorne/skillm/internal/store"
)

// These tests drive the real binary through install's SOURCE mode (PLAN §3
// install): `install <url|path>` fetches into Home and installs in one step,
// reusing the same fetch pipeline as `add`. They reuse the harness from
// integration_test.go (env, initSkillRepo, skillmBinary, assert*).

// initSkillRepoWith builds a git repo (branch main) holding one skill directory
// per (id → body) entry, and returns the repo path and its file:// URL. It is
// the multi-source companion to initSkillRepo, used to manufacture a second
// Source whose skill ids deliberately collide with the first.
func initSkillRepoWith(t *testing.T, skills map[string]string) (repo, url string) {
	t.Helper()
	repo = t.TempDir()
	for id, body := range skills {
		writeSkillMD(t, filepath.Join(repo, id), id, body)
	}
	runGit(t, repo, "init", "-q", "-b", "main")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "test")
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-q", "-m", "skills")
	return repo, fileURL(repo)
}

// TestInstallSourceGit covers install's git source mode: a single skill_id
// selector, then --all to fetch+install the rest of the catalog (the already-in
// alpha is reused from the same Source, not re-fetched).
func TestInstallSourceGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	bin := skillmBinary(t)
	_, url := initSkillRepo(t)
	e := env{home: t.TempDir(), userDir: t.TempDir(), bin: bin}

	// --- selector: fetch + install only alpha at global -----------------------
	out := e.run(t, "install", url, "alpha", "--global")
	if !strings.Contains(out, "added alpha") {
		t.Fatalf("install alpha from source: expected an 'added' line, got:\n%s", out)
	}
	assertLinkResolvesIntoHome(t, e, claudeGlobalLink(e, "alpha"), "alpha")
	assertLinkResolvesIntoHome(t, e, agentsGlobalLink(e, "alpha"), "alpha")
	a, ok := loadState(t, e).Get("alpha")
	if !ok || a.Kind != state.KindGit {
		t.Fatalf("alpha not registered as a git skill: %+v ok=%v", a, ok)
	}
	// beta/gamma were not selected, so they are neither added nor installed.
	if store.Exists(e.home, "beta") {
		t.Fatal("beta must not be added when only alpha was selected")
	}
	assertNoLink(t, claudeGlobalLink(e, "beta"), "beta not selected")

	// --- --all: fetch + install the rest; alpha is reused, not re-fetched ------
	out = e.run(t, "install", url, "--all", "--global")
	if !strings.Contains(out, "already in Home") {
		t.Fatalf("install --all: expected a reuse notice for alpha, got:\n%s", out)
	}
	for _, id := range []string{"alpha", "beta", "gamma"} {
		assertLinkResolvesIntoHome(t, e, claudeGlobalLink(e, id), id)
		assertLinkResolvesIntoHome(t, e, agentsGlobalLink(e, id), id)
	}
	if n := len(loadState(t, e).Skills); n != 3 {
		t.Fatalf("registry has %d skills, want 3 (alpha, beta, gamma)", n)
	}
}

// TestInstallSourceLocal covers install's local-path source mode: a path-shaped
// argument to a directory holding a SKILL.md is fetched (copied) into Home as a
// local skill and installed.
func TestInstallSourceLocal(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	bin := skillmBinary(t)
	e := env{home: t.TempDir(), userDir: t.TempDir(), bin: bin}

	src := t.TempDir()
	writeSkillMD(t, filepath.Join(src, "mylocal"), "mylocal", "local body")

	out := e.run(t, "install", filepath.Join(src, "mylocal"), "--global")
	if !strings.Contains(out, "added mylocal") {
		t.Fatalf("install local source: expected an 'added' line, got:\n%s", out)
	}
	assertLinkResolvesIntoHome(t, e, claudeGlobalLink(e, "mylocal"), "mylocal")
	entry, ok := loadState(t, e).Get("mylocal")
	if !ok || entry.Kind != state.KindLocal {
		t.Fatalf("mylocal not registered as a local skill: %+v ok=%v", entry, ok)
	}
}

// TestInstallShapeBareNameIsId proves the shape-based disambiguation: a bare
// name is ALWAYS an in-Home id, never a Source, even when a same-named directory
// holding a different skill sits in the current directory.
func TestInstallShapeBareNameIsId(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	bin := skillmBinary(t)
	_, url := initSkillRepo(t)
	e := env{home: t.TempDir(), userDir: t.TempDir(), bin: bin}

	// Put the git "alpha" into Home (fetch-only).
	e.run(t, "add", url, "alpha")

	// Work in a project that contains a DECOY local skill, also named "alpha".
	project := evalProject(t, t.TempDir())
	writeSkillMD(t, filepath.Join(project, "alpha"), "alpha", "LOCAL DECOY body")

	// A bare "alpha" must resolve to the in-Home (git) skill, never the ./alpha
	// decoy — so this installs the existing Home copy, leaving it a git skill.
	e.runIn(t, project, "install", "alpha", "--global")
	assertLinkResolvesIntoHome(t, e, claudeGlobalLink(e, "alpha"), "alpha")

	a, _ := loadState(t, e).Get("alpha")
	if a.Kind != state.KindGit {
		t.Fatalf("bare name resolved to the local decoy (kind=%q), want the in-Home git skill", a.Kind)
	}
	b, err := os.ReadFile(filepath.Join(claudeGlobalLink(e, "alpha"), "SKILL.md"))
	if err != nil {
		t.Fatalf("read installed alpha: %v", err)
	}
	if strings.Contains(string(b), "DECOY") {
		t.Fatalf("installed the ./alpha decoy instead of the in-Home skill:\n%s", b)
	}
}

// TestInstallSourceSameSourceReuse proves same-Source reuse does NOT re-fetch:
// after the skill is in Home, an upstream change is ignored by install source
// mode (the existing Home copy is installed; `update` is the way to refresh).
func TestInstallSourceSameSourceReuse(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	bin := skillmBinary(t)
	repo, url := initSkillRepo(t)
	e := env{home: t.TempDir(), userDir: t.TempDir(), bin: bin}

	// Fetch alpha into Home (no install).
	e.run(t, "add", url, "alpha")
	before, _ := loadState(t, e).Get("alpha")

	// Change alpha upstream so a re-fetch WOULD alter the Home copy.
	if err := os.WriteFile(filepath.Join(repo, "alpha", "SKILL.md"),
		[]byte("---\nname: alpha\ndescription: alpha skill\n---\nalpha body CHANGED\n"), 0o644); err != nil {
		t.Fatalf("rewrite alpha upstream: %v", err)
	}
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-q", "-m", "edit alpha")

	// install from the SAME source must reuse the existing Home copy, not re-fetch.
	out := e.run(t, "install", url, "alpha", "--global")
	if !strings.Contains(out, "already in Home") {
		t.Fatalf("expected a reuse notice, got:\n%s", out)
	}
	// Revision unchanged (no re-fetch) and Home content still the original.
	after, _ := loadState(t, e).Get("alpha")
	if after.Revision != before.Revision {
		t.Fatalf("revision changed (%q -> %q): install must not re-fetch a same-source skill",
			before.Revision, after.Revision)
	}
	body, err := os.ReadFile(filepath.Join(store.SkillDir(e.home, "alpha"), "SKILL.md"))
	if err != nil {
		t.Fatalf("read Home alpha: %v", err)
	}
	if strings.Contains(string(body), "CHANGED") {
		t.Fatalf("Home copy was overwritten; install must not re-fetch:\n%s", body)
	}
	// But it IS installed now.
	assertLinkResolvesIntoHome(t, e, claudeGlobalLink(e, "alpha"), "alpha")
}

// TestInstallSourceDifferentSourceCollision proves a same-id-different-Source
// clash is a collision error, and that the check is atomic across the whole
// selection: one clash adds and installs NOTHING.
func TestInstallSourceDifferentSourceCollision(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	bin := skillmBinary(t)
	_, url1 := initSkillRepo(t) // alpha, beta, gamma
	_, url2 := initSkillRepoWith(t, map[string]string{
		"alpha": "a DIFFERENT alpha", // collides with url1's alpha
		"delta": "delta body",        // unique to url2
	})
	e := env{home: t.TempDir(), userDir: t.TempDir(), bin: bin}

	// alpha is in Home from url1.
	e.run(t, "add", url1, "alpha")

	// Installing alpha from url2 (a DIFFERENT source) is a collision error.
	out, err := e.tryRun(t, "install", url2, "alpha", "--global")
	if err == nil {
		t.Fatalf("different-source install should fail; got success:\n%s", out)
	}
	if !strings.Contains(out, "different source") || !strings.Contains(out, "--as") {
		t.Fatalf("expected a different-source collision error naming --as, got:\n%s", out)
	}
	assertNoLink(t, claudeGlobalLink(e, "alpha"), "collision must install nothing")

	// Atomic across the selection: --all over url2 (= {alpha, delta}) clashes on
	// alpha, so delta must be neither added to Home nor installed.
	out, err = e.tryRun(t, "install", url2, "--all", "--global")
	if err == nil {
		t.Fatalf("atomic different-source install should fail; got success:\n%s", out)
	}
	if store.Exists(e.home, "delta") {
		t.Fatal("atomic: delta must not be added when the selection has a different-source clash")
	}
	assertNoLink(t, claudeGlobalLink(e, "delta"), "atomic: delta must not be installed")
}

// TestInstallSourceNonTTYRefusals covers the non-TTY guards for source mode: a
// multi-skill catalog with no selector refuses (naming --all), and a selector
// with no scope flag refuses (naming --global). The binary's stdin is never a
// TTY in tests, so the pickers refuse — exactly the non-TTY contract.
func TestInstallSourceNonTTYRefusals(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	bin := skillmBinary(t)
	_, url := initSkillRepo(t)
	e := env{home: t.TempDir(), userDir: t.TempDir(), bin: bin}

	// No selector on a multi-skill catalog → the skill picker refuses.
	if out, err := e.tryRun(t, "install", url, "--global"); err == nil || !strings.Contains(out, "--all") {
		t.Fatalf("source mode without a selector should refuse naming --all; err=%v out=%s", err, out)
	}

	// Selector given but no scope flag → the scope picker refuses.
	if out, err := e.tryRun(t, "install", url, "alpha"); err == nil || !strings.Contains(out, "--global") {
		t.Fatalf("source mode without a scope flag should refuse naming --global; err=%v out=%s", err, out)
	}
	// Nothing was linked in either case.
	assertNoLink(t, claudeGlobalLink(e, "alpha"), "refused source install must not link")
}

// TestInstallSourceCopy covers --copy via source mode: fetch + vendor a real,
// committable copy into the project in one step, recording the vendored root
// (which requires the registry to be reloaded after the fetch).
func TestInstallSourceCopy(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	bin := skillmBinary(t)
	_, url := initSkillRepo(t)
	e := env{home: t.TempDir(), userDir: t.TempDir(), bin: bin}

	project := evalProject(t, t.TempDir())
	out := e.runIn(t, project, "install", url, "alpha", "--local", "--copy")
	if !strings.Contains(out, "copied alpha") {
		t.Fatalf("install --copy from source: expected a 'copied' line, got:\n%s", out)
	}
	assertVendoredCopy(t, filepath.Join(project, ".claude", "skills", "alpha"), "alpha body")
	assertVendoredCopy(t, filepath.Join(project, ".agents", "skills", "alpha"), "alpha body")

	a, _ := loadState(t, e).Get("alpha")
	if got := a.VendoredAt; len(got) != 1 || got[0] != project {
		t.Fatalf("vendored_at = %v, want [%s]", got, project)
	}
}

// TestInstallSourceCopyGlobalRejectedBeforeFetch proves the pure-flag guard
// fails fast: --copy with --global is rejected BEFORE any network fetch or Home
// mutation, so a failed source install is a true no-op (nothing added to Home).
func TestInstallSourceCopyGlobalRejectedBeforeFetch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	bin := skillmBinary(t)
	_, url := initSkillRepo(t)
	e := env{home: t.TempDir(), userDir: t.TempDir(), bin: bin}

	out, err := e.tryRun(t, "install", url, "alpha", "--global", "--copy")
	if err == nil || !strings.Contains(out, "only valid for a local install") {
		t.Fatalf("--copy --global from a source should be rejected; err=%v out=%s", err, out)
	}
	// The rejection must precede the fetch: alpha must not have been added to
	// Home, and the registry must stay empty.
	if store.Exists(e.home, "alpha") {
		t.Fatal("--copy --global must fail before fetching: alpha must not be in Home")
	}
	if n := len(loadState(t, e).Skills); n != 0 {
		t.Fatalf("registry has %d entries, want 0 (no fetch should have happened)", n)
	}
}

// TestInstallAsRefRejectedInIDMode verifies --as / --ref are rejected when not
// installing from a source (they only make sense for a fetch).
func TestInstallAsRefRejectedInIDMode(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	bin := skillmBinary(t)
	_, url := initSkillRepo(t)
	e := env{home: t.TempDir(), userDir: t.TempDir(), bin: bin}
	e.run(t, "add", url, "alpha")

	if out, err := e.tryRun(t, "install", "alpha", "--global", "--as", "x"); err == nil || !strings.Contains(out, "--as") {
		t.Fatalf("--as in id mode should be rejected; err=%v out=%s", err, out)
	}
	if out, err := e.tryRun(t, "install", "alpha", "--global", "--ref", "main"); err == nil || !strings.Contains(out, "--ref") {
		t.Fatalf("--ref in id mode should be rejected; err=%v out=%s", err, out)
	}
}
