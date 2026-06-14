package ui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The test binary's stdout is not a terminal, so IsTTY() is false here and we
// exercise every auto-degraded (non-interactive) code path.

func TestIsTTYFalseUnderTest(t *testing.T) {
	if IsTTY() {
		t.Fatal("IsTTY() = true under `go test`; expected non-terminal stdout")
	}
}

func TestRenderSkillTableEmpty(t *testing.T) {
	got := RenderSkillTable(nil)
	if !strings.Contains(got, "No skills") {
		t.Fatalf("empty table = %q, want a 'No skills' notice", got)
	}
}

func TestRenderSkillTablePlain(t *testing.T) {
	rows := []Row{
		{ID: "alpha", Source: "https://example.com/repo", Linked: "global: claude", Kind: "git"},
		{ID: "beta", Source: "/tmp/local", Linked: "-", Kind: "local"},
	}
	got := RenderSkillTable(rows)

	// Plain (non-TTY) output: header line + tab-separated cells, no ANSI escapes.
	if strings.Contains(got, "\x1b[") {
		t.Fatalf("non-TTY table contains ANSI escapes: %q", got)
	}
	lines := strings.Split(got, "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3 (header + 2 rows): %q", len(lines), got)
	}
	header := strings.Split(lines[0], "\t")
	want := []string{"ID", "Source", "Linked", "Kind"}
	if len(header) != len(want) {
		t.Fatalf("header columns = %v, want %v", header, want)
	}
	for i := range want {
		if header[i] != want[i] {
			t.Fatalf("header[%d] = %q, want %q", i, header[i], want[i])
		}
	}
	if !strings.Contains(lines[1], "alpha") || !strings.Contains(lines[1], "git") {
		t.Fatalf("row 1 missing expected cells: %q", lines[1])
	}
	if !strings.Contains(lines[2], "beta") || !strings.Contains(lines[2], "local") {
		t.Fatalf("row 2 missing expected cells: %q", lines[2])
	}
}

func TestSelectSkillsNonTTY(t *testing.T) {
	got, err := SelectSkills("Pick skills", []Option{{Label: "a", Value: "a"}})
	if err == nil {
		t.Fatal("SelectSkills on non-TTY: want error, got nil")
	}
	if got != nil {
		t.Fatalf("SelectSkills on non-TTY: want nil selection, got %v", got)
	}
	if !strings.Contains(err.Error(), "--all") {
		t.Fatalf("SelectSkills error %q should name the --all escape hatch", err)
	}
}

func TestSelectAgentsNonTTY(t *testing.T) {
	_, err := SelectAgents([]string{"claude", "codex"}, []string{"claude"})
	if err == nil {
		t.Fatal("SelectAgents on non-TTY: want error, got nil")
	}
	if !strings.Contains(err.Error(), "non-interactive") {
		t.Fatalf("SelectAgents error %q should mention non-interactive", err)
	}
}

func TestSelectScopeNonTTY(t *testing.T) {
	_, err := SelectScope(t.TempDir())
	if err == nil {
		t.Fatal("SelectScope on non-TTY: want error, got nil")
	}
	if !strings.Contains(err.Error(), "--global") || !strings.Contains(err.Error(), "--local") {
		t.Fatalf("SelectScope error %q should name the --global/--local flags", err)
	}
}

// TestDirSuggestions checks the path-completion candidate list: subdirectories
// whose full path extends what the user typed, files excluded, and a missing
// directory yielding nothing.
func TestDirSuggestions(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"alpha", "alabaster", "beta"} {
		if err := os.Mkdir(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "alfile"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	// Partial leaf "al": only the two "al*" directories match; "beta" does not,
	// and the "alfile" regular file is excluded even though its name matches.
	got := dirSuggestions(filepath.Join(root, "al"))
	want := []string{filepath.Join(root, "alabaster"), filepath.Join(root, "alpha")}
	if !equalUnordered(got, want) {
		t.Fatalf("dirSuggestions(.../al) = %v, want %v", got, want)
	}

	// A trailing separator lists every subdirectory of the directory itself.
	gotAll := dirSuggestions(root + string(os.PathSeparator))
	wantAll := []string{
		filepath.Join(root, "alabaster"),
		filepath.Join(root, "alpha"),
		filepath.Join(root, "beta"),
	}
	if !equalUnordered(gotAll, wantAll) {
		t.Fatalf("dirSuggestions(root/) = %v, want %v", gotAll, wantAll)
	}

	// A nonexistent directory is best-effort: no suggestions, no panic.
	if got := dirSuggestions(filepath.Join(root, "nope", "deeper")); got != nil {
		t.Fatalf("dirSuggestions(missing) = %v, want nil", got)
	}
}

func TestValidateDir(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := validateDir(dir); err != nil {
		t.Errorf("validateDir(existing dir) = %v, want nil", err)
	}
	if err := validateDir(""); err == nil {
		t.Error("validateDir(\"\") = nil, want error")
	}
	if err := validateDir(file); err == nil {
		t.Error("validateDir(file) = nil, want error")
	}
	if err := validateDir(filepath.Join(dir, "missing")); err == nil {
		t.Error("validateDir(missing) = nil, want error")
	}
}

func TestExpandTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}
	sep := string(os.PathSeparator)
	cases := map[string]string{
		"~":                        home,
		"~" + sep + "proj":         filepath.Join(home, "proj"),
		sep + "abs" + sep + "path": sep + "abs" + sep + "path",
		"relative":                 "relative",
		"~notme":                   "~notme", // "~" only expands alone or before a separator
	}
	for in, want := range cases {
		if got := expandTilde(in); got != want {
			t.Errorf("expandTilde(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWithTrailingSep(t *testing.T) {
	sep := string(os.PathSeparator)
	if got := withTrailingSep(""); got != "" {
		t.Errorf("withTrailingSep(\"\") = %q, want \"\"", got)
	}
	if got := withTrailingSep("/a/b"); got != "/a/b"+sep {
		t.Errorf("withTrailingSep(/a/b) = %q, want %q", got, "/a/b"+sep)
	}
	if got := withTrailingSep("/a/b" + sep); got != "/a/b"+sep {
		t.Errorf("withTrailingSep(idempotent) = %q, want %q", got, "/a/b"+sep)
	}
}

// equalUnordered reports whether a and b contain the same elements regardless
// of order (dirSuggestions order follows the OS directory listing).
func equalUnordered(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]int, len(a))
	for _, s := range a {
		seen[s]++
	}
	for _, s := range b {
		seen[s]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}

func TestConfirmNonTTY(t *testing.T) {
	ok, err := Confirm("Proceed?")
	if err == nil {
		t.Fatal("Confirm on non-TTY: want error, got nil")
	}
	if ok {
		t.Fatal("Confirm on non-TTY: want false, got true")
	}
	if !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("Confirm error %q should name the --yes flag", err)
	}
}

func TestRunProgressPlainExecutesWork(t *testing.T) {
	var reported []int
	called := false
	err := RunProgress(5, func(report func(done int)) error {
		called = true
		for i := 1; i <= 5; i++ {
			report(i)
		}
		reported = append(reported, 5)
		return nil
	})
	if err != nil {
		t.Fatalf("RunProgress returned error: %v", err)
	}
	if !called {
		t.Fatal("RunProgress did not invoke work")
	}
	if len(reported) != 1 {
		t.Fatalf("work side effects not observed: %v", reported)
	}
}

func TestRunProgressPropagatesError(t *testing.T) {
	sentinel := errors.New("boom")
	err := RunProgress(10, func(report func(done int)) error {
		report(1)
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("RunProgress error = %v, want %v", err, sentinel)
	}
}

func TestRunProgressNilWork(t *testing.T) {
	if err := RunProgress(100, nil); err != nil {
		t.Fatalf("RunProgress(nil work) = %v, want nil", err)
	}
}

func TestRunProgressTinyTotalRunsPlainly(t *testing.T) {
	// total < threshold must still execute work (just without a bar).
	called := false
	err := RunProgress(1, func(report func(done int)) error {
		called = true
		report(1)
		return nil
	})
	if err != nil {
		t.Fatalf("RunProgress(tiny) error: %v", err)
	}
	if !called {
		t.Fatal("RunProgress(tiny) did not run work")
	}
}

func TestPrintHelpersNoPanic(t *testing.T) {
	// Smoke test: helpers must not panic on a non-TTY and format args.
	Successf("ok %s", "added")
	Warnf("careful %d", 3)
	Errorf("failed: %v", errors.New("x"))
}
