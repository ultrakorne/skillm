package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/ultrakorne/skillm/internal/state"
	"github.com/ultrakorne/skillm/internal/store"
)

// This file holds the end-to-end integration test required by PLAN §8: it spins
// up a temp Home (via SKILLM_HOME) and a real local git repo holding several
// skill directories, then drives the *built* skillm binary through the full
// add → link → check → update → remove lifecycle. Driving the real binary with
// SKILLM_HOME set (rather than calling cobra commands in-process) exercises the
// genuine treeless-clone / ls-tree / sparse-checkout code paths against real git
// — the SubtreeSHA revision tracking runs for real, not mocked.
//
// The repo is served over a file:// URL so git uses its network transport and
// honours --filter=tree:0 (a plain local-path clone would silently
// hardlink-optimise and skip the treeless filter). HOME is redirected to a temp
// directory so the agents' *global* skill folders (~/.claude/skills,
// ~/.codex/skills) land inside the sandbox and never touch the developer's real
// dotfiles.

// builtBinary builds ./skillm once per test binary run and returns its path.
var (
	binOnce sync.Once
	binPath string
	binErr  error
)

func skillmBinary(t *testing.T) string {
	t.Helper()
	binOnce.Do(func() {
		if _, err := exec.LookPath("go"); err != nil {
			binErr = err
			return
		}
		dir, err := os.MkdirTemp("", "skillm-bin-")
		if err != nil {
			binErr = err
			return
		}
		out := filepath.Join(dir, "skillm")
		if runtime.GOOS == "windows" {
			out += ".exe"
		}
		// The package under test is github.com/ultrakorne/skillm/cmd; the main
		// package (the binary) lives one directory up from this test file.
		build := exec.Command("go", "build", "-o", out, ".")
		build.Dir = ".."
		if b, err := build.CombinedOutput(); err != nil {
			binErr = &buildError{out: string(b), err: err}
			return
		}
		binPath = out
	})
	if binErr != nil {
		t.Fatalf("build skillm binary: %v", binErr)
	}
	return binPath
}

type buildError struct {
	out string
	err error
}

func (e *buildError) Error() string { return e.err.Error() + "\n" + e.out }

// env carries the isolated HOME / SKILLM_HOME for every binary invocation.
type env struct {
	home    string // SKILLM_HOME (the skillm store)
	userDir string // HOME (so global agent folders are sandboxed)
	bin     string
}

// run executes the skillm binary with the sandbox environment and returns the
// combined output. It fails the test on a non-zero exit.
func (e env) run(t *testing.T, args ...string) string {
	t.Helper()
	out, err := e.tryRun(t, args...)
	if err != nil {
		t.Fatalf("skillm %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

// tryRun executes the binary and returns its combined output and error without
// failing the test, for cases that expect a non-zero exit.
func (e env) tryRun(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(e.bin, args...)
	cmd.Env = append(os.Environ(),
		"HOME="+e.userDir,
		"SKILLM_HOME="+e.home,
		// Make the sandbox deterministic regardless of the developer's git
		// identity / config.
		"GIT_CONFIG_GLOBAL="+filepath.Join(e.userDir, ".gitconfig"),
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_TERMINAL_PROMPT=0",
	)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// runGit runs git in dir, failing the test on error.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// writeSkillMD writes a SKILL.md with minimal frontmatter under dir.
func writeSkillMD(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	content := "---\nname: " + name + "\ndescription: " + name + " skill\n---\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
}

// initSkillRepo creates a git repo at a temp dir holding alpha/, beta/ and
// gamma/ skill directories on branch main, and returns the repo path and a
// file:// URL pointing at it. beta carries a nested supporting file to prove
// supporting files travel with the skill.
func initSkillRepo(t *testing.T) (repo, url string) {
	t.Helper()
	repo = t.TempDir()
	writeSkillMD(t, filepath.Join(repo, "alpha"), "alpha", "alpha body")
	writeSkillMD(t, filepath.Join(repo, "beta"), "beta", "beta body")
	writeSkillMD(t, filepath.Join(repo, "gamma"), "gamma", "gamma body")
	if err := os.WriteFile(filepath.Join(repo, "beta", "helper.txt"), []byte("helper\n"), 0o644); err != nil {
		t.Fatalf("write helper: %v", err)
	}

	runGit(t, repo, "init", "-q", "-b", "main")
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "test")
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-q", "-m", "initial skills")

	url = "file://" + repo
	return repo, url
}

// loadState reads and parses the registry from the sandbox Home.
func loadState(t *testing.T, e env) *state.State {
	t.Helper()
	st, err := state.Load(e.home)
	if err != nil {
		t.Fatalf("load state.toml: %v", err)
	}
	return st
}

// assertLinkResolvesIntoHome verifies that linkPath is a symlink whose resolved
// target is store.SkillDir(home, id) and that the linked SKILL.md is readable
// through the link (i.e. the link is live, not dangling).
func assertLinkResolvesIntoHome(t *testing.T, e env, linkPath, id string) {
	t.Helper()
	fi, err := os.Lstat(linkPath)
	if err != nil {
		t.Fatalf("lstat %s: %v", linkPath, err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%s is not a symlink (mode %v)", linkPath, fi.Mode())
	}
	target, err := os.Readlink(linkPath)
	if err != nil {
		t.Fatalf("readlink %s: %v", linkPath, err)
	}
	want := store.SkillDir(e.home, id)
	if filepath.Clean(target) != filepath.Clean(want) {
		t.Fatalf("link %s -> %q, want %q", linkPath, target, want)
	}
	// The link must resolve to a real directory inside Home, with a SKILL.md.
	if _, err := os.Stat(filepath.Join(linkPath, "SKILL.md")); err != nil {
		t.Fatalf("SKILL.md not reachable through link %s: %v", linkPath, err)
	}
}

// claudeGlobalLink and codexGlobalLink compute the expected global link paths
// under the sandbox HOME.
func claudeGlobalLink(e env, id string) string {
	return filepath.Join(e.userDir, ".claude", "skills", id)
}
func codexGlobalLink(e env, id string) string {
	return filepath.Join(e.userDir, ".codex", "skills", id)
}

// TestLifecycleEndToEnd is the full PLAN §8 integration test: a temp Home + a
// real local git repo, driven through add → link → check → update → remove via
// the built binary, asserting symlink targets and registry contents at each
// step.
func TestLifecycleEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	bin := skillmBinary(t)
	repo, url := initSkillRepo(t)

	e := env{
		home:    t.TempDir(),
		userDir: t.TempDir(),
		bin:     bin,
	}

	// --- add (git, single id, with --global so it also links) ---------------
	out := e.run(t, "add", url, "alpha", "--global")
	if !strings.Contains(out, "added alpha") {
		t.Fatalf("add alpha: unexpected output:\n%s", out)
	}

	// Registry: one git entry for alpha with the right source/path/ref and a
	// non-empty revision (the subdir tree SHA, read for real via ls-tree).
	st := loadState(t, e)
	if len(st.Skills) != 1 {
		t.Fatalf("after add alpha: %d entries, want 1: %+v", len(st.Skills), st.Skills)
	}
	alpha, ok := st.Get("alpha")
	if !ok {
		t.Fatal("alpha missing from registry")
	}
	if alpha.Kind != state.KindGit {
		t.Errorf("alpha kind = %q, want git", alpha.Kind)
	}
	if alpha.Source != url {
		t.Errorf("alpha source = %q, want %q", alpha.Source, url)
	}
	if alpha.Path != "alpha" {
		t.Errorf("alpha path = %q, want %q", alpha.Path, "alpha")
	}
	if alpha.Ref != "main" {
		t.Errorf("alpha ref = %q, want %q", alpha.Ref, "main")
	}
	if len(alpha.Revision) < 7 {
		t.Errorf("alpha revision = %q, want a real tree SHA", alpha.Revision)
	}
	if alpha.InstalledAt.IsZero() {
		t.Error("alpha installed_at is zero")
	}

	// The Home copy exists and global links were created for both agents,
	// resolving back into Home.
	if !store.Exists(e.home, "alpha") {
		t.Fatalf("alpha not present in Home %s", store.SkillsDir(e.home))
	}
	assertLinkResolvesIntoHome(t, e, claudeGlobalLink(e, "alpha"), "alpha")
	assertLinkResolvesIntoHome(t, e, codexGlobalLink(e, "alpha"), "alpha")

	// --- add (fetch-only, no link) ------------------------------------------
	e.run(t, "add", url, "beta")
	if _, err := os.Lstat(claudeGlobalLink(e, "beta")); !os.IsNotExist(err) {
		t.Fatalf("bare add must not link beta; lstat err = %v", err)
	}
	// beta's supporting file must have travelled into Home with it.
	if _, err := os.Stat(filepath.Join(store.SkillDir(e.home, "beta"), "helper.txt")); err != nil {
		t.Fatalf("beta helper.txt not copied into Home: %v", err)
	}

	// --- add with --as (collision-free rename) ------------------------------
	e.run(t, "add", url, "gamma", "--as", "renamed-gamma")
	if _, ok := loadState(t, e).Get("renamed-gamma"); !ok {
		t.Fatal("renamed-gamma missing from registry after --as")
	}
	if !store.Exists(e.home, "renamed-gamma") {
		t.Fatal("renamed-gamma not present in Home")
	}

	// --- collision: re-adding alpha must error and not duplicate ------------
	if out, err := e.tryRun(t, "add", url, "alpha"); err == nil {
		t.Fatalf("re-adding alpha should fail (collision), got success:\n%s", out)
	}
	if n := len(loadState(t, e).Skills); n != 3 {
		t.Fatalf("after collision attempt: %d entries, want 3 (alpha, beta, renamed-gamma)", n)
	}

	// --- link (default scope is global per config default) ------------------
	e.run(t, "link", "beta", "--global")
	assertLinkResolvesIntoHome(t, e, claudeGlobalLink(e, "beta"), "beta")
	assertLinkResolvesIntoHome(t, e, codexGlobalLink(e, "beta"), "beta")

	// --- link --local creates project-scoped links under cwd ----------------
	// Run with the working directory set to a temp project so .claude/.codex
	// land there and not in the developer's tree.
	project := t.TempDir()
	localCmd := exec.Command(bin, "link", "beta", "--local")
	localCmd.Dir = project
	localCmd.Env = append(os.Environ(),
		"HOME="+e.userDir,
		"SKILLM_HOME="+e.home,
		"GIT_CONFIG_SYSTEM=/dev/null",
	)
	if b, err := localCmd.CombinedOutput(); err != nil {
		t.Fatalf("link beta --local: %v\n%s", err, b)
	}
	assertLinkResolvesIntoHome(t, e, filepath.Join(project, ".claude", "skills", "beta"), "beta")
	assertLinkResolvesIntoHome(t, e, filepath.Join(project, ".codex", "skills", "beta"), "beta")

	// --- unlink one scope leaves the other intact ---------------------------
	e.run(t, "unlink", "beta", "--global")
	if _, err := os.Lstat(claudeGlobalLink(e, "beta")); !os.IsNotExist(err) {
		t.Fatalf("beta global link should be gone after unlink, err = %v", err)
	}
	// The local link must survive the global unlink.
	assertLinkResolvesIntoHome(t, e, filepath.Join(project, ".claude", "skills", "beta"), "beta")

	// --- check (read-only) before any upstream change: all up-to-date -------
	out = e.run(t, "check")
	if !strings.Contains(out, "up-to-date") {
		t.Fatalf("check before change: expected up-to-date, got:\n%s", out)
	}
	if strings.Contains(out, "update available") {
		t.Fatalf("check before change: unexpected update available:\n%s", out)
	}
	// check must not have mutated the registry revisions.
	if got, _ := loadState(t, e).Get("alpha"); got.Revision != alpha.Revision {
		t.Fatalf("check mutated alpha revision: %q -> %q", alpha.Revision, got.Revision)
	}

	// --- mutate ONLY beta upstream; check must flag beta but not alpha ------
	if err := os.WriteFile(filepath.Join(repo, "beta", "SKILL.md"),
		[]byte("---\nname: beta\ndescription: beta skill\n---\nbeta body CHANGED\n"), 0o644); err != nil {
		t.Fatalf("rewrite beta upstream: %v", err)
	}
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-q", "-m", "edit beta only")

	out = e.run(t, "check")
	if !strings.Contains(out, "beta") || !strings.Contains(out, "update available") {
		t.Fatalf("check after beta edit: expected beta update available, got:\n%s", out)
	}
	// Per-skill isolation: alpha's line must say up-to-date, not update available.
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "alpha") && strings.Contains(line, "update available") {
			t.Fatalf("alpha incorrectly flagged after a beta-only commit:\n%s", out)
		}
	}

	// --- update (all outdated) ---------------------------------------------
	revAlphaBefore, _ := loadState(t, e).Get("alpha")
	revBetaBefore, _ := loadState(t, e).Get("beta")

	out = e.run(t, "update")
	if !strings.Contains(out, "Updated beta") {
		t.Fatalf("update: expected beta to be updated, got:\n%s", out)
	}

	st = loadState(t, e)
	gotAlpha, _ := st.Get("alpha")
	gotBeta, _ := st.Get("beta")
	if gotAlpha.Revision != revAlphaBefore.Revision {
		t.Errorf("alpha revision changed by update (should be untouched): %q -> %q",
			revAlphaBefore.Revision, gotAlpha.Revision)
	}
	if gotBeta.Revision == revBetaBefore.Revision {
		t.Errorf("beta revision not advanced by update: still %q", gotBeta.Revision)
	}

	// The Home copy now holds the changed content, and the surviving local link
	// transparently exposes the update (agents see Home through the symlink).
	homeBeta, err := os.ReadFile(filepath.Join(store.SkillDir(e.home, "beta"), "SKILL.md"))
	if err != nil {
		t.Fatalf("read updated beta in Home: %v", err)
	}
	if !strings.Contains(string(homeBeta), "CHANGED") {
		t.Fatalf("Home beta not updated; content:\n%s", homeBeta)
	}
	linkedBeta, err := os.ReadFile(filepath.Join(project, ".claude", "skills", "beta", "SKILL.md"))
	if err != nil {
		t.Fatalf("read beta through local link: %v", err)
	}
	if !strings.Contains(string(linkedBeta), "CHANGED") {
		t.Fatalf("update did not propagate through the symlink; linked content:\n%s", linkedBeta)
	}

	// A second check is now clean again.
	out = e.run(t, "check")
	if strings.Contains(out, "update available") {
		t.Fatalf("check after update: still reports update available:\n%s", out)
	}

	// --- update of a local skill is a no-op note ----------------------------
	// Add a local skill from a directory, then confirm update skips it.
	localSrc := t.TempDir()
	writeSkillMD(t, filepath.Join(localSrc, "mylocal"), "mylocal", "local body")
	e.run(t, "add", filepath.Join(localSrc, "mylocal"))
	localEntry, ok := loadState(t, e).Get("mylocal")
	if !ok || localEntry.Kind != state.KindLocal {
		t.Fatalf("mylocal not added as a local skill: %+v ok=%v", localEntry, ok)
	}
	if localEntry.Ref != "" || localEntry.Revision != "" {
		t.Errorf("local skill must carry no ref/revision: %+v", localEntry)
	}
	out = e.run(t, "update", "mylocal")
	if !strings.Contains(strings.ToLower(out), "local skill") {
		t.Fatalf("update mylocal: expected a local-skill note, got:\n%s", out)
	}

	// --- remove (auto-unlink everywhere + delete + drop registry entry) -----
	// Local-scope links are resolved relative to the process cwd, so run remove
	// from the project directory where beta was linked locally — that is how a
	// user would invoke it, and it lets remove tear down the local link too.
	removeCmd := exec.Command(bin, "remove", "beta", "--yes")
	removeCmd.Dir = project
	removeCmd.Env = append(os.Environ(),
		"HOME="+e.userDir,
		"SKILLM_HOME="+e.home,
		"GIT_CONFIG_SYSTEM=/dev/null",
	)
	removeOut, removeErr := removeCmd.CombinedOutput()
	if removeErr != nil {
		t.Fatalf("remove beta: %v\n%s", removeErr, removeOut)
	}
	out = string(removeOut)
	if !strings.Contains(out, "removed beta") {
		t.Fatalf("remove beta: unexpected output:\n%s", out)
	}
	if store.Exists(e.home, "beta") {
		t.Fatal("beta still present in Home after remove")
	}
	if _, ok := loadState(t, e).Get("beta"); ok {
		t.Fatal("beta still in registry after remove")
	}
	// Both the surviving local link and any global link must be gone (no
	// dangling symlinks left behind).
	if _, err := os.Lstat(filepath.Join(project, ".claude", "skills", "beta")); !os.IsNotExist(err) {
		t.Fatalf("beta local link not removed by remove, err = %v", err)
	}
	if _, err := os.Lstat(codexGlobalLink(e, "beta")); !os.IsNotExist(err) {
		t.Fatalf("beta codex global link not removed by remove, err = %v", err)
	}

	// alpha must be untouched by beta's removal.
	if !store.Exists(e.home, "alpha") {
		t.Fatal("alpha removed as a side effect of removing beta")
	}
	assertLinkResolvesIntoHome(t, e, claudeGlobalLink(e, "alpha"), "alpha")

	// --- final registry shape ----------------------------------------------
	st = loadState(t, e)
	have := map[string]bool{}
	for _, s := range st.Skills {
		have[s.ID] = true
	}
	for _, want := range []string{"alpha", "renamed-gamma", "mylocal"} {
		if !have[want] {
			t.Errorf("expected %q to remain in registry; have %v", want, have)
		}
	}
	if have["beta"] {
		t.Errorf("beta should be gone from registry; have %v", have)
	}
}
