package cmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestCheckListGolden pins the plain (non-TTY) output of `skillm check` and
// `skillm list` byte for byte, stdout and stderr separately, across every row
// kind: up to date, update available, a subdir removed upstream, an unreachable
// source, a local skill, and Global plus Local installs seen from inside and
// outside the project, and an empty Home. Sandbox paths are replaced by
// placeholders so the golden files are stable. Regenerate with
// SKILLM_UPDATE_GOLDEN=1.
func TestCheckListGolden(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	bin := skillmBinary(t)
	repo, url := initSkillRepo(t)
	repo2, url2 := initSkillRepoWith(t, map[string]string{"delta": "delta body"})
	localSrc := t.TempDir()
	writeSkillMD(t, filepath.Join(localSrc, "omega"), "omega", "omega body")
	project := t.TempDir()
	e := env{home: t.TempDir(), userDir: t.TempDir(), bin: bin}

	e.run(t, "install", url, "alpha", "beta", "gamma", "--global")
	e.runIn(t, project, "install", url, "alpha", "gamma", "--local")
	e.run(t, "install", url2, "delta", "--global")
	e.run(t, "install", filepath.Join(localSrc, "omega"), "--global")

	// beta changes upstream, gamma disappears upstream, delta's repo vanishes.
	writeSkillMD(t, filepath.Join(repo, "beta"), "beta", "beta body v2")
	runGit(t, repo, "rm", "-q", "-r", "gamma")
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-q", "-m", "change beta, drop gamma")
	if err := os.RemoveAll(repo2); err != nil {
		t.Fatalf("remove repo2: %v", err)
	}

	placeholders := []struct{ path, name string }{
		{e.home, "$SKILLM_HOME"},
		{e.userDir, "$HOME"},
		{project, "$PROJECT"},
		{repo, "$REPO"},
		{repo2, "$REPO2"},
		{localSrc, "$LOCALSRC"},
	}
	normalize := func(s string) string {
		// Replace the symlink-resolved spelling first: on macOS it is the raw
		// spelling with a /private prefix, so the reverse order would leave
		// "/private" behind.
		for _, p := range placeholders {
			if real, err := filepath.EvalSymlinks(p.path); err == nil && real != p.path {
				s = strings.ReplaceAll(s, real, p.name)
				s = strings.ReplaceAll(s, urlPath(real), p.name)
			}
		}
		for _, p := range placeholders {
			s = strings.ReplaceAll(s, p.path, p.name)
			// A git Source is recorded as the file:// URL git was given
			// (fileURL, forward-slashed with a leading "/" before a Windows
			// drive letter), never the raw native path, so on Windows the
			// plain replace above never matches inside it. Matching the
			// URL's own path spelling catches that case too; on POSIX
			// urlPath(p.path) == p.path, so this is a harmless no-op there.
			s = strings.ReplaceAll(s, urlPath(p.path), p.name)
		}
		// Collapse any native separator left in a path that continued past a
		// placeholder (e.g. a local skill's Source is `$LOCALSRC` plus a
		// subdirectory) so the fixture's forward slashes match on Windows too.
		return filepath.ToSlash(s)
	}

	empty := env{home: t.TempDir(), userDir: t.TempDir(), bin: bin}
	cases := []struct {
		name string
		e    env
		dir  string
		args []string
	}{
		{"check", e, e.userDir, []string{"check"}},
		{"list_outside_project", e, e.userDir, []string{"list"}},
		{"list_in_project", e, project, []string{"list"}},
		{"check_empty_home", empty, empty.userDir, []string{"check"}},
		{"list_empty_home", empty, empty.userDir, []string{"list"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr := tc.e.runSplit(t, tc.dir, tc.args...)
			got := "--- stdout\n" + normalize(stdout) + "--- stderr\n" + normalize(stderr)
			golden := filepath.Join("testdata", "golden", tc.name+".txt")
			if os.Getenv("SKILLM_UPDATE_GOLDEN") == "1" {
				if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
					t.Fatalf("write golden: %v", err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("read golden (regenerate with SKILLM_UPDATE_GOLDEN=1): %v", err)
			}
			if got != string(want) {
				t.Fatalf("%s output differs from %s\n--- got\n%s\n--- want\n%s", tc.name, golden, got, want)
			}
		})
	}
}

// urlPath returns the path portion of fileURL(path): what a git file:// URL
// looks like for path, minus the scheme. On Windows this is the
// forward-slashed, leading-slash spelling (fileURL's own conversion) rather
// than path's native backslashed form, which is what actually appears in a
// git Source recorded from that URL. On POSIX it is path unchanged.
func urlPath(path string) string {
	return strings.TrimPrefix(fileURL(path), "file://")
}

// runSplit runs the binary in dir with the sandbox environment and returns its
// stdout and stderr separately, failing the test on a non-zero exit.
func (e env) runSplit(t *testing.T, dir string, args ...string) (stdout, stderr string) {
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
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		t.Fatalf("skillm %s (in %s) failed: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), dir, err, out.String(), errOut.String())
	}
	return out.String(), errOut.String()
}
