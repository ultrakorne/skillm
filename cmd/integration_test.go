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

// fileURL converts a local directory path to a file:// URL that git accepts on
// all platforms. On Windows the path needs a leading slash before the drive
// letter (file:///C:/...), which is already present on Unix paths.
func fileURL(path string) string {
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return "file://" + p
}

// This file holds the end-to-end integration test required by PLAN §8: it spins
// up a temp Home (via SKILLM_HOME) and a real local git repo holding several
// skill directories, then drives the *built* skillm binary through the full
// add → install → check → update → uninstall lifecycle. Driving the real binary with
// SKILLM_HOME set (rather than calling cobra commands in-process) exercises the
// genuine treeless-clone / ls-tree / sparse-checkout code paths against real git
// — the SubtreeSHA revision tracking runs for real, not mocked.
//
// The repo is served over a file:// URL so git uses its network transport and
// honours --filter=tree:0 (a plain local-path clone would silently
// hardlink-optimise and skip the treeless filter). HOME is redirected to a temp
// directory so the agents' *global* skill folders (~/.claude/skills,
// ~/.agents/skills) land inside the sandbox and never touch the developer's
// real dotfiles.

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
	extraEnv := []string{
		"HOME=" + e.userDir,
		// Windows: os.UserHomeDir reads USERPROFILE, not HOME.
		"USERPROFILE=" + e.userDir,
		"SKILLM_HOME=" + e.home,
		// Make the sandbox deterministic regardless of the developer's git identity.
		"GIT_CONFIG_GLOBAL=" + filepath.Join(e.userDir, ".gitconfig"),
		"GIT_CONFIG_SYSTEM=" + os.DevNull,
		"GIT_TERMINAL_PROMPT=0",
	}
	cmd.Env = append(os.Environ(), extraEnv...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// runGit runs git in dir, failing the test on error.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_SYSTEM="+os.DevNull,
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

	url = fileURL(repo)
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

// claudeGlobalLink and agentsGlobalLink compute the expected global link paths
// under the sandbox HOME.
func claudeGlobalLink(e env, id string) string {
	return filepath.Join(e.userDir, ".claude", "skills", id)
}
func agentsGlobalLink(e env, id string) string {
	return filepath.Join(e.userDir, ".agents", "skills", id)
}

// TestLifecycleEndToEnd is the full PLAN §8 integration test: a temp Home + a
// real local git repo, driven through add → install → check → update → uninstall
// via the built binary, asserting symlink targets and registry contents at each
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

	// --- install from a source (git, single id, --global) -------------------
	// install's source mode fetches alpha into Home AND links it in one step,
	// the way `add --global` used to before add became fetch-only.
	out := e.run(t, "install", url, "alpha", "--global")
	if !strings.Contains(out, "added alpha") {
		t.Fatalf("install alpha from source: unexpected output:\n%s", out)
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
	assertLinkResolvesIntoHome(t, e, agentsGlobalLink(e, "alpha"), "alpha")

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

	// --- install (default scope is global per config default) ---------------
	e.run(t, "install", "beta", "--global")
	assertLinkResolvesIntoHome(t, e, claudeGlobalLink(e, "beta"), "beta")
	assertLinkResolvesIntoHome(t, e, agentsGlobalLink(e, "beta"), "beta")

	// --- install --local creates project-scoped links under cwd -------------
	// Run with the working directory set to a temp project so .claude/.agents
	// land there and not in the developer's tree. beta stays installed globally
	// too — uninstall must later tear down every scope.
	project := t.TempDir()
	localCmd := exec.Command(bin, "install", "beta", "--local")
	localCmd.Dir = project
	localCmd.Env = append(os.Environ(),
		"HOME="+e.userDir,
		"USERPROFILE="+e.userDir,
		"SKILLM_HOME="+e.home,
		"GIT_CONFIG_SYSTEM="+os.DevNull,
	)
	if b, err := localCmd.CombinedOutput(); err != nil {
		t.Fatalf("install beta --local: %v\n%s", err, b)
	}
	assertLinkResolvesIntoHome(t, e, filepath.Join(project, ".claude", "skills", "beta"), "beta")
	assertLinkResolvesIntoHome(t, e, filepath.Join(project, ".agents", "skills", "beta"), "beta")

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

	// --- uninstall (auto-unlink everywhere + delete + drop registry entry) --
	// Local-scope links are resolved relative to the process cwd, so run
	// uninstall from the project directory where beta was linked locally — that
	// is how a user would invoke it, and it lets uninstall tear down the local
	// link too.
	uninstallCmd := exec.Command(bin, "uninstall", "beta", "--yes")
	uninstallCmd.Dir = project
	uninstallCmd.Env = append(os.Environ(),
		"HOME="+e.userDir,
		"USERPROFILE="+e.userDir,
		"SKILLM_HOME="+e.home,
		"GIT_CONFIG_SYSTEM="+os.DevNull,
	)
	uninstallOut, uninstallErr := uninstallCmd.CombinedOutput()
	if uninstallErr != nil {
		t.Fatalf("uninstall beta: %v\n%s", uninstallErr, uninstallOut)
	}
	out = string(uninstallOut)
	if !strings.Contains(out, "uninstalled beta") {
		t.Fatalf("uninstall beta: unexpected output:\n%s", out)
	}
	if store.Exists(e.home, "beta") {
		t.Fatal("beta still present in Home after uninstall")
	}
	if _, ok := loadState(t, e).Get("beta"); ok {
		t.Fatal("beta still in registry after uninstall")
	}
	// The local link and BOTH global links must be gone (no dangling symlinks
	// left behind), since beta was installed at every scope.
	if _, err := os.Lstat(filepath.Join(project, ".claude", "skills", "beta")); !os.IsNotExist(err) {
		t.Fatalf("beta local link not removed by uninstall, err = %v", err)
	}
	if _, err := os.Lstat(claudeGlobalLink(e, "beta")); !os.IsNotExist(err) {
		t.Fatalf("beta claude global link not removed by uninstall, err = %v", err)
	}
	if _, err := os.Lstat(agentsGlobalLink(e, "beta")); !os.IsNotExist(err) {
		t.Fatalf("beta agents global link not removed by uninstall, err = %v", err)
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

// TestInstallLocalFromHomeRefuses drives the real binary to prove the
// home-directory invariant end-to-end: `install --local` run from HOME refuses
// (local there resolves to the global skill folder), creates no link, and
// records no local root. This is the only way to exercise the os.Getwd()-based
// scope resolution the in-process helper tests cannot reach.
func TestInstallLocalFromHomeRefuses(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	bin := skillmBinary(t)
	_, url := initSkillRepo(t)

	// Resolve symlinks in the sandbox home so the path the child reports via
	// os.Getwd() (which resolves them — e.g. /var -> /private/var on macOS)
	// matches HOME exactly. A real home (/Users/foo, C:\Users\foo, /home/foo) is
	// not behind such a symlink, so this only matters for the tmpdir sandbox.
	userDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve sandbox home: %v", err)
	}
	e := env{home: t.TempDir(), userDir: userDir, bin: bin}

	// Fetch a skill into Home without linking it.
	e.run(t, "add", url, "alpha")

	// Run `install --local` with the working directory set to HOME.
	cmd := exec.Command(bin, "install", "alpha", "--local")
	cmd.Dir = e.userDir
	cmd.Env = append(os.Environ(),
		"HOME="+e.userDir,
		"USERPROFILE="+e.userDir,
		"SKILLM_HOME="+e.home,
		"GIT_CONFIG_SYSTEM="+os.DevNull,
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("install --local from home should fail; got success:\n%s", out)
	}
	if !strings.Contains(string(out), "resolves to the global skill folder") {
		t.Fatalf("expected the alias error, got:\n%s", out)
	}

	// No local root recorded, and no link created under HOME.
	if st := loadState(t, e); len(st.LocalRoots) != 0 {
		t.Fatalf("home must not be recorded as a local root: %v", st.LocalRoots)
	}
	if _, err := os.Lstat(filepath.Join(e.userDir, ".claude", "skills", "alpha")); !os.IsNotExist(err) {
		t.Fatalf("install --local from home must not create a link; lstat err = %v", err)
	}
}

// assertNoLink fails if linkPath exists at all (we expect no entry there).
func assertNoLink(t *testing.T, linkPath, msg string) {
	t.Helper()
	if _, err := os.Lstat(linkPath); !os.IsNotExist(err) {
		t.Fatalf("%s: %s still exists (err = %v)", msg, linkPath, err)
	}
}

// runIn executes the skillm binary with the working directory set to dir (so
// local-scope resolution sees it as cwd), failing the test on a non-zero exit.
func (e env) runIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := e.tryRunIn(t, dir, args...)
	if err != nil {
		t.Fatalf("skillm %s (in %s) failed: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return out
}

// tryRunIn is runIn without failing on error, for cases expecting a non-zero exit.
func (e env) tryRunIn(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(e.bin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"HOME="+e.userDir,
		"USERPROFILE="+e.userDir,
		"SKILLM_HOME="+e.home,
		"GIT_CONFIG_GLOBAL="+filepath.Join(e.userDir, ".gitconfig"),
		"GIT_CONFIG_SYSTEM="+os.DevNull,
		"GIT_TERMINAL_PROMPT=0",
	)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// assertVendoredCopy verifies path is a REAL directory (not a symlink) holding a
// SKILL.md whose content contains wantSubstr — i.e. a self-contained Vendored
// copy, not a link back into Home.
func assertVendoredCopy(t *testing.T, path, wantSubstr string) {
	t.Helper()
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat vendored copy %s: %v", path, err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("%s is a symlink, want a real vendored copy", path)
	}
	if !fi.IsDir() {
		t.Fatalf("%s is not a directory", path)
	}
	b, err := os.ReadFile(filepath.Join(path, "SKILL.md"))
	if err != nil {
		t.Fatalf("read SKILL.md in vendored copy %s: %v", path, err)
	}
	if !strings.Contains(string(b), wantSubstr) {
		t.Fatalf("vendored %s/SKILL.md = %q, want it to contain %q", path, b, wantSubstr)
	}
}

// evalProject resolves symlinks in a project dir so it matches the absolute path
// a child process records via os.Getwd() (which resolves them, e.g. /var ->
// /private/var on macOS). On Linux tmpdirs this is usually a no-op.
func evalProject(t *testing.T, dir string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return dir
	}
	return resolved
}

// TestVendoredCopyLifecycle drives the real binary through the full vendoring
// lifecycle: install --local --copy writes self-contained copies (per agent,
// not symlinks) and records the root; a Global symlink coexists; update refreshes
// the committed copies in place AND the global symlink; uninstall deletes the
// copies, the Home copy, and the registry entry. It also covers the guard rails:
// --copy --global is rejected, and a foreign directory is not clobbered without
// --force.
func TestVendoredCopyLifecycle(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	bin := skillmBinary(t)
	repo, url := initSkillRepo(t)
	e := env{home: t.TempDir(), userDir: t.TempDir(), bin: bin}

	// Fetch alpha into Home, no install yet.
	e.run(t, "add", url, "alpha")

	// --- install --local --copy: real copies per agent, root recorded ---------
	project := evalProject(t, t.TempDir())
	out := e.runIn(t, project, "install", "alpha", "--local", "--copy")
	if !strings.Contains(out, "copied alpha") {
		t.Fatalf("install --copy: expected a 'copied' line, got:\n%s", out)
	}
	claudeCopy := filepath.Join(project, ".claude", "skills", "alpha")
	agentsCopy := filepath.Join(project, ".agents", "skills", "alpha")
	assertVendoredCopy(t, claudeCopy, "alpha body")
	assertVendoredCopy(t, agentsCopy, "alpha body")

	// The registry records the project as a vendored root (and only that).
	alpha, _ := loadState(t, e).Get("alpha")
	if got := alpha.VendoredAt; len(got) != 1 || got[0] != project {
		t.Fatalf("vendored_at = %v, want [%s]", got, project)
	}

	// --- a Global symlink coexists with the Local copy ------------------------
	e.run(t, "install", "alpha", "--global")
	assertLinkResolvesIntoHome(t, e, claudeGlobalLink(e, "alpha"), "alpha")
	// The local copies are still real directories, untouched by the global link.
	assertVendoredCopy(t, claudeCopy, "alpha body")

	// --- update refreshes the committed copies in place -----------------------
	if err := os.WriteFile(filepath.Join(repo, "alpha", "SKILL.md"),
		[]byte("---\nname: alpha\ndescription: alpha skill\n---\nalpha body CHANGED\n"), 0o644); err != nil {
		t.Fatalf("rewrite alpha upstream: %v", err)
	}
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-q", "-m", "edit alpha")

	out = e.run(t, "update")
	if !strings.Contains(out, "Updated alpha") {
		t.Fatalf("update: expected alpha updated, got:\n%s", out)
	}
	// Both committed copies now hold the new content...
	assertVendoredCopy(t, claudeCopy, "CHANGED")
	assertVendoredCopy(t, agentsCopy, "CHANGED")
	// ...and the global symlink transparently exposes it via Home.
	gb, err := os.ReadFile(filepath.Join(claudeGlobalLink(e, "alpha"), "SKILL.md"))
	if err != nil || !strings.Contains(string(gb), "CHANGED") {
		t.Fatalf("global symlink did not see the update: err=%v content=%s", err, gb)
	}

	// --- uninstall deletes copies + Home + registry entry ---------------------
	e.runIn(t, project, "uninstall", "alpha", "--yes")
	assertNoLink(t, claudeCopy, "uninstall must delete the vendored copy")
	assertNoLink(t, agentsCopy, "uninstall must delete the vendored copy")
	assertNoLink(t, claudeGlobalLink(e, "alpha"), "uninstall must remove the global symlink")
	if store.Exists(e.home, "alpha") {
		t.Fatal("alpha still in Home after uninstall")
	}
	if _, ok := loadState(t, e).Get("alpha"); ok {
		t.Fatal("alpha still in registry after uninstall")
	}

	// --- guard: --copy --global is rejected -----------------------------------
	e.run(t, "add", url, "beta")
	if out, err := e.tryRun(t, "install", "beta", "--global", "--copy"); err == nil ||
		!strings.Contains(out, "only valid for a local install") {
		t.Fatalf("--copy --global should be rejected; err=%v out=%s", err, out)
	}

	// --- guard: a foreign directory is not clobbered without --force ----------
	proj2 := evalProject(t, t.TempDir())
	foreign := filepath.Join(proj2, ".claude", "skills", "beta")
	if err := os.MkdirAll(foreign, 0o755); err != nil {
		t.Fatalf("create foreign dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(foreign, "MINE.txt"), []byte("hand-written\n"), 0o644); err != nil {
		t.Fatalf("write foreign file: %v", err)
	}
	if out, err := e.tryRunIn(t, proj2, "install", "beta", "--local", "--copy"); err == nil ||
		!strings.Contains(out, "--force") {
		t.Fatalf("vendoring over a foreign dir should refuse on a non-TTY naming --force; err=%v out=%s", err, out)
	}
	// The foreign file survives the refusal.
	if _, err := os.Stat(filepath.Join(foreign, "MINE.txt")); err != nil {
		t.Fatalf("refusal must not touch the foreign files: %v", err)
	}
	// With --force it overwrites and adopts the directory as a vendored copy.
	e.runIn(t, proj2, "install", "beta", "--local", "--copy", "--force")
	assertVendoredCopy(t, foreign, "beta body")
	if _, err := os.Stat(filepath.Join(foreign, "MINE.txt")); !os.IsNotExist(err) {
		t.Fatalf("--force should have replaced the foreign dir; MINE.txt err = %v", err)
	}
}

// TestVendorSymlinkConversion proves install converts between skillm's own
// symlink and copy forms in place: a symlinked local install becomes a copy
// under --copy (recording the root), and a plain re-install turns it back into a
// symlink (dropping the vendored root).
func TestVendorSymlinkConversion(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	bin := skillmBinary(t)
	_, url := initSkillRepo(t)
	e := env{home: t.TempDir(), userDir: t.TempDir(), bin: bin}
	e.run(t, "add", url, "alpha")

	project := evalProject(t, t.TempDir())
	claudePath := filepath.Join(project, ".claude", "skills", "alpha")

	// Symlink install first.
	e.runIn(t, project, "install", "alpha", "--local")
	assertLinkResolvesIntoHome(t, e, claudePath, "alpha")
	if got, _ := loadState(t, e).Get("alpha"); len(got.VendoredAt) != 0 {
		t.Fatalf("symlink install must not record a vendored root: %v", got.VendoredAt)
	}

	// Convert to a copy.
	e.runIn(t, project, "install", "alpha", "--local", "--copy")
	assertVendoredCopy(t, claudePath, "alpha body")
	if got, _ := loadState(t, e).Get("alpha"); len(got.VendoredAt) != 1 || got.VendoredAt[0] != project {
		t.Fatalf("convert to copy must record the vendored root; got %v", got.VendoredAt)
	}

	// Convert back to a symlink with a plain local install.
	e.runIn(t, project, "install", "alpha", "--local")
	assertLinkResolvesIntoHome(t, e, claudePath, "alpha")
	if got, _ := loadState(t, e).Get("alpha"); len(got.VendoredAt) != 0 {
		t.Fatalf("convert back to symlink must drop the vendored root; got %v", got.VendoredAt)
	}
}

// TestInstallUninstallMulti exercises the multi-skill behaviour added with the
// install/uninstall rename: variadic ids, --all, atomic failure on an unknown
// id, and the non-interactive guards (no picker / no scope on a non-TTY). The
// binary's stdin is never a TTY here, so the interactive pickers refuse — which
// is exactly the non-TTY contract we assert.
func TestInstallUninstallMulti(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	bin := skillmBinary(t)
	_, url := initSkillRepo(t)

	e := env{home: t.TempDir(), userDir: t.TempDir(), bin: bin}

	// Fetch three skills into Home without linking any of them.
	for _, id := range []string{"alpha", "beta", "gamma"} {
		e.run(t, "add", url, id)
	}

	// --- atomic install: one bad id installs nothing ------------------------
	if out, err := e.tryRun(t, "install", "alpha", "nope", "--global"); err == nil {
		t.Fatalf("install with an unknown id should fail; got success:\n%s", out)
	}
	assertNoLink(t, claudeGlobalLink(e, "alpha"), "atomic install must not link the valid id")

	// --- non-TTY guards -----------------------------------------------------
	// No ids and no --all: the picker refuses and names the escape hatch.
	if out, err := e.tryRun(t, "install"); err == nil || !strings.Contains(out, "--all") {
		t.Fatalf("bare install on a non-TTY should error naming --all; err=%v out=%s", err, out)
	}
	// Ids but no scope flag: the scope picker refuses and names --global/--local.
	if out, err := e.tryRun(t, "install", "alpha"); err == nil || !strings.Contains(out, "--global") {
		t.Fatalf("install without a scope on a non-TTY should error naming --global; err=%v out=%s", err, out)
	}

	// --- variadic install ---------------------------------------------------
	e.run(t, "install", "alpha", "beta", "--global")
	for _, id := range []string{"alpha", "beta"} {
		assertLinkResolvesIntoHome(t, e, claudeGlobalLink(e, id), id)
		assertLinkResolvesIntoHome(t, e, agentsGlobalLink(e, id), id)
	}
	// gamma was not named, so it stays uninstalled.
	assertNoLink(t, claudeGlobalLink(e, "gamma"), "gamma must not be installed yet")

	// --- install --all picks up the rest (already-linked ones are no-ops) ---
	e.run(t, "install", "--all", "--global")
	assertLinkResolvesIntoHome(t, e, claudeGlobalLink(e, "gamma"), "gamma")

	// --- atomic uninstall: one bad id removes nothing -----------------------
	if out, err := e.tryRun(t, "uninstall", "beta", "nope", "--yes"); err == nil {
		t.Fatalf("uninstall with an unknown id should fail; got success:\n%s", out)
	}
	if !store.Exists(e.home, "beta") {
		t.Fatal("atomic uninstall must not remove the valid id (beta)")
	}

	// --- variadic uninstall removes exactly the named skills ----------------
	out := e.run(t, "uninstall", "alpha", "gamma", "--yes")
	if !strings.Contains(out, "uninstalled alpha") || !strings.Contains(out, "uninstalled gamma") {
		t.Fatalf("uninstall alpha gamma: unexpected output:\n%s", out)
	}
	for _, id := range []string{"alpha", "gamma"} {
		if store.Exists(e.home, id) {
			t.Fatalf("%s still in Home after uninstall", id)
		}
		if _, ok := loadState(t, e).Get(id); ok {
			t.Fatalf("%s still in registry after uninstall", id)
		}
		assertNoLink(t, claudeGlobalLink(e, id), "uninstall must drop the link")
	}
	// beta is untouched.
	if !store.Exists(e.home, "beta") {
		t.Fatal("beta removed as a side effect of uninstalling alpha/gamma")
	}
	assertLinkResolvesIntoHome(t, e, claudeGlobalLink(e, "beta"), "beta")

	// --- uninstall --all clears whatever remains ----------------------------
	e.run(t, "uninstall", "--all", "--yes")
	if n := len(loadState(t, e).Skills); n != 0 {
		t.Fatalf("uninstall --all left %d skills in the registry, want 0", n)
	}
	assertNoLink(t, claudeGlobalLink(e, "beta"), "uninstall --all must drop beta's link")
}
