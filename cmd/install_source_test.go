package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ultrakorne/skillm/internal/state"
)

// These tests drive the real binary through install's SOURCE mode (`install
// <url|path>` fetches and installs in one step) and install-by-id mode (`install
// <id>` adds another scope to an already-installed skill). They reuse the
// harness from integration_test.go (env, initSkillRepo, skillmBinary, assert*).

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
// selector installs just that skill; then --all fetches and installs the rest of
// the catalog (the already-installed alpha is simply re-installed).
func TestInstallSourceGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	bin := skillmBinary(t)
	_, url := initSkillRepo(t)
	e := env{home: t.TempDir(), userDir: t.TempDir(), bin: bin}

	// --- selector: fetch + install only alpha at global -----------------------
	out := e.run(t, "install", url, "alpha", "--global")
	if !strings.Contains(out, "installed alpha") {
		t.Fatalf("install alpha from source: expected an 'installed' line, got:\n%s", out)
	}
	assertGlobalInstalled(t, e, "alpha")
	a, ok := loadState(t, e).Get("alpha")
	if !ok || a.Kind != state.KindGit {
		t.Fatalf("alpha not registered as a git skill: %+v ok=%v", a, ok)
	}
	// beta/gamma were not selected, so they are neither registered nor installed.
	if _, ok := loadState(t, e).Get("beta"); ok {
		t.Fatal("beta must not be registered when only alpha was selected")
	}
	assertNoLink(t, claudeGlobalLink(e, "beta"), "beta not selected")

	// --- --all: fetch + install the rest; alpha is re-installed ---------------
	out = e.run(t, "install", url, "--all", "--global")
	for _, id := range []string{"alpha", "beta", "gamma"} {
		assertGlobalInstalled(t, e, id)
	}
	if n := len(loadState(t, e).Skills); n != 3 {
		t.Fatalf("registry has %d skills, want 3 (alpha, beta, gamma)", n)
	}
}

// TestInstallSourceLocal covers install's local-path source mode: a path-shaped
// argument to a directory holding a SKILL.md is copied straight into the chosen
// scope as a local skill and registered.
func TestInstallSourceLocal(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	bin := skillmBinary(t)
	e := env{home: t.TempDir(), userDir: t.TempDir(), bin: bin}

	src := t.TempDir()
	writeSkillMD(t, filepath.Join(src, "mylocal"), "mylocal", "local body")

	out := e.run(t, "install", filepath.Join(src, "mylocal"), "--global")
	if !strings.Contains(out, "installed mylocal") {
		t.Fatalf("install local source: expected an 'installed' line, got:\n%s", out)
	}
	assertGlobalInstalled(t, e, "mylocal")
	entry, ok := loadState(t, e).Get("mylocal")
	if !ok || entry.Kind != state.KindLocal {
		t.Fatalf("mylocal not registered as a local skill: %+v ok=%v", entry, ok)
	}
}

// TestInstallShapeBareNameIsId proves the shape-based disambiguation: a bare
// name is ALWAYS a registered id, never a Source, even when a same-named
// directory holding a different skill sits in the current directory.
func TestInstallShapeBareNameIsId(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	bin := skillmBinary(t)
	_, url := initSkillRepo(t)
	e := env{home: t.TempDir(), userDir: t.TempDir(), bin: bin}

	// Install the git "alpha" globally so it is registered.
	e.run(t, "install", url, "alpha", "--global")

	// Work in a project that contains a DECOY local skill, also named "alpha".
	project := evalProject(t, t.TempDir())
	writeSkillMD(t, filepath.Join(project, "alpha"), "alpha", "LOCAL DECOY body")

	// A bare "alpha" must resolve to the registered (git) skill, never the
	// ./alpha decoy — so this installs it locally from the global canonical copy,
	// leaving it a git skill.
	e.runIn(t, project, "install", "alpha", "--local")

	a, _ := loadState(t, e).Get("alpha")
	if a.Kind != state.KindGit {
		t.Fatalf("bare name resolved to the local decoy (kind=%q), want the registered git skill", a.Kind)
	}
	b, err := os.ReadFile(filepath.Join(project, ".claude", "skills", "alpha", "SKILL.md"))
	if err != nil {
		t.Fatalf("read installed alpha: %v", err)
	}
	if strings.Contains(string(b), "DECOY") {
		t.Fatalf("installed the ./alpha decoy instead of the registered skill:\n%s", b)
	}
}

// TestInstallByIdCopiesFromGlobalCopy proves install-by-id sources from the
// existing global canonical copy, without a re-fetch: with the skill installed
// globally, changing the upstream is ignored when a bare id adds a local scope.
func TestInstallByIdCopiesFromGlobalCopy(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	bin := skillmBinary(t)
	repo, url := initSkillRepo(t)
	e := env{home: t.TempDir(), userDir: t.TempDir(), bin: bin}

	e.run(t, "install", url, "alpha", "--global")
	before, _ := loadState(t, e).Get("alpha")

	// Change alpha upstream so a re-fetch WOULD alter the content.
	writeSkillMD(t, filepath.Join(repo, "alpha"), "alpha", "alpha body CHANGED")
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-q", "-m", "edit alpha")

	// Adding a local install by bare id copies from the global canonical copy —
	// no network, so the revision is unchanged and the content is the original.
	project := evalProject(t, t.TempDir())
	e.runIn(t, project, "install", "alpha", "--local")

	after, _ := loadState(t, e).Get("alpha")
	if after.Revision != before.Revision {
		t.Fatalf("revision changed (%q -> %q): install-by-id with a global copy must not re-fetch",
			before.Revision, after.Revision)
	}
	body, err := os.ReadFile(filepath.Join(project, ".agents", "skills", "alpha", "SKILL.md"))
	if err != nil {
		t.Fatalf("read local alpha: %v", err)
	}
	if strings.Contains(string(body), "CHANGED") {
		t.Fatalf("local copy took the changed upstream; must copy from the global copy:\n%s", body)
	}
}

// TestInstallByIdRefetchesWhenOnlyLocal proves install-by-id re-fetches from the
// recorded source when the skill has no global copy to reuse: with only a local
// install, adding another scope by id fetches fresh content and may advance the
// recorded revision.
func TestInstallByIdRefetchesWhenOnlyLocal(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	bin := skillmBinary(t)
	repo, url := initSkillRepo(t)
	e := env{home: t.TempDir(), userDir: t.TempDir(), bin: bin}

	// Install alpha ONLY locally (no global copy to reuse).
	projectA := evalProject(t, t.TempDir())
	e.runIn(t, projectA, "install", url, "alpha", "--local")
	before, _ := loadState(t, e).Get("alpha")

	// Advance alpha upstream.
	writeSkillMD(t, filepath.Join(repo, "alpha"), "alpha", "alpha body CHANGED")
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-q", "-m", "edit alpha")

	// A bare-id install with no global copy re-fetches: revision advances and the
	// new install carries the changed content.
	e.run(t, "install", "alpha", "--global")

	after, _ := loadState(t, e).Get("alpha")
	if after.Revision == before.Revision {
		t.Fatalf("revision unchanged (%q): install-by-id with only-local installs must re-fetch", after.Revision)
	}
	body, err := os.ReadFile(filepath.Join(agentsGlobalCopy(e, "alpha"), "SKILL.md"))
	if err != nil {
		t.Fatalf("read global alpha: %v", err)
	}
	if !strings.Contains(string(body), "CHANGED") {
		t.Fatalf("re-fetch did not pick up the changed upstream:\n%s", body)
	}
}

// TestInstallSourceSameSourceRefetches proves that installing from the SAME
// source re-fetches (there is no library to reuse): an upstream change is picked
// up and the recorded revision advances.
func TestInstallSourceSameSourceRefetches(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	bin := skillmBinary(t)
	repo, url := initSkillRepo(t)
	e := env{home: t.TempDir(), userDir: t.TempDir(), bin: bin}

	e.run(t, "install", url, "alpha", "--global")
	before, _ := loadState(t, e).Get("alpha")

	// Change alpha upstream.
	writeSkillMD(t, filepath.Join(repo, "alpha"), "alpha", "alpha body CHANGED")
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-q", "-m", "edit alpha")

	// install from the SAME source re-fetches and re-installs the fresh content.
	e.run(t, "install", url, "alpha", "--global")
	after, _ := loadState(t, e).Get("alpha")
	if after.Revision == before.Revision {
		t.Fatalf("revision unchanged (%q): a same-source install must re-fetch the current upstream", after.Revision)
	}
	body, err := os.ReadFile(filepath.Join(agentsGlobalCopy(e, "alpha"), "SKILL.md"))
	if err != nil {
		t.Fatalf("read global alpha: %v", err)
	}
	if !strings.Contains(string(body), "CHANGED") {
		t.Fatalf("same-source install did not refresh the content:\n%s", body)
	}
}

// TestInstallSourceDifferentSourceCollision proves a same-id-different-Source
// clash is a collision error, and that the check is atomic across the whole
// selection: one clash installs NOTHING.
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

	// alpha is registered from url1.
	e.run(t, "install", url1, "alpha", "--global")

	// Installing alpha from url2 (a DIFFERENT source) is a collision error.
	out, err := e.tryRun(t, "install", url2, "alpha", "--global")
	if err == nil {
		t.Fatalf("different-source install should fail; got success:\n%s", out)
	}
	if !strings.Contains(out, "different source") || !strings.Contains(out, "--as") {
		t.Fatalf("expected a different-source collision error naming --as, got:\n%s", out)
	}

	// Atomic across the selection: --all over url2 (= {alpha, delta}) clashes on
	// alpha, so delta must be neither registered nor installed.
	out, err = e.tryRun(t, "install", url2, "--all", "--global")
	if err == nil {
		t.Fatalf("atomic different-source install should fail; got success:\n%s", out)
	}
	if _, ok := loadState(t, e).Get("delta"); ok {
		t.Fatal("atomic: delta must not be registered when the selection has a different-source clash")
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

// TestInstallSourceLocalScope covers local installs via source mode: fetch +
// write the committable project install (canonical copy, relative claude link,
// lockfile) in one step, recording the install root.
func TestInstallSourceLocalScope(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	bin := skillmBinary(t)
	_, url := initSkillRepo(t)
	e := env{home: t.TempDir(), userDir: t.TempDir(), bin: bin}

	project := evalProject(t, t.TempDir())
	out := e.runIn(t, project, "install", url, "alpha", "--local")
	if !strings.Contains(out, "installed alpha") {
		t.Fatalf("install --local from source: expected an 'installed' line, got:\n%s", out)
	}
	assertVendoredCopy(t, filepath.Join(project, ".agents", "skills", "alpha"), "alpha body")
	assertLinkResolvesToCanonical(t, project, filepath.Join(project, ".claude", "skills", "alpha"), "alpha")
	if _, err := os.Stat(filepath.Join(project, "skills-lock.json")); err != nil {
		t.Fatalf("skills-lock.json missing after source install: %v", err)
	}

	a, _ := loadState(t, e).Get("alpha")
	if got := a.VendoredAt; len(got) != 1 || got[0] != project {
		t.Fatalf("vendored_at = %v, want [%s]", got, project)
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
	e.run(t, "install", url, "alpha", "--global")

	if out, err := e.tryRun(t, "install", "alpha", "--global", "--as", "x"); err == nil || !strings.Contains(out, "--as") {
		t.Fatalf("--as in id mode should be rejected; err=%v out=%s", err, out)
	}
	if out, err := e.tryRun(t, "install", "alpha", "--global", "--ref", "main"); err == nil || !strings.Contains(out, "--ref") {
		t.Fatalf("--ref in id mode should be rejected; err=%v out=%s", err, out)
	}
}
