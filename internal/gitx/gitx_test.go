package gitx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestLooksLikeSHA(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"d97bdddcddc6818bc7ae1a0ff501912739da6cf4", true}, // full 40-hex
		{"d97bddd", true}, // abbreviated 7-hex
		{"D97BDDD", true}, // upper-case hex
		{"main", false},   // branch name
		{"master", false}, // branch name
		{"v0.1.0", false}, // tag with dots
		{"d97bdd", false}, // too short (<7)
		{"feature/x", false},
		{"", false},
		{"123456789012345678901234567890123456789012", false}, // too long (>40)
		{"ghijklm", false}, // non-hex letters
	}
	for _, c := range cases {
		if got := looksLikeSHA(c.in); got != c.want {
			t.Errorf("looksLikeSHA(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestCleanSubpath(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{".", ""},
		{"./", ""},
		{"grill-with-docs", "grill-with-docs"},
		{"/grill-with-docs/", "grill-with-docs"},
		{"./a/b", "a/b"},
		{"  spaced  ", "spaced"},
		{"a/b/c", "a/b/c"},
	}
	for _, c := range cases {
		if got := cleanSubpath(c.in); got != c.want {
			t.Errorf("cleanSubpath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// gitAvailable reports whether the git binary is on PATH; tests that need a
// real repo skip when it is not (it always is in CI).
func gitAvailable(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
}

// initRepo creates a small git repo with two skill subdirectories and returns
// its path. Each skill has its own SKILL.md so their subtree SHAs differ.
func initRepo(t *testing.T) string {
	t.Helper()
	gitAvailable(t)

	dir := t.TempDir()
	ctx := context.Background()

	mustGit := func(args ...string) {
		t.Helper()
		if _, err := runGit(ctx, dir, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}

	mustGit("init", "-q", "-b", "main")
	mustGit("config", "user.email", "test@example.com")
	mustGit("config", "user.name", "Test")

	writeFile(t, filepath.Join(dir, "skill-a", "SKILL.md"), "# Skill A\n")
	writeFile(t, filepath.Join(dir, "skill-a", "ref.md"), "a reference\n")
	writeFile(t, filepath.Join(dir, "skill-b", "SKILL.md"), "# Skill B\n")
	writeFile(t, filepath.Join(dir, "README.md"), "root\n")

	mustGit("add", "-A")
	mustGit("commit", "-q", "-m", "initial")

	return dir
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSubtreeSHA(t *testing.T) {
	dir := initRepo(t)
	ctx := context.Background()

	shaA, err := SubtreeSHA(ctx, dir, "main", "skill-a")
	if err != nil {
		t.Fatalf("SubtreeSHA skill-a: %v", err)
	}
	shaB, err := SubtreeSHA(ctx, dir, "main", "skill-b")
	if err != nil {
		t.Fatalf("SubtreeSHA skill-b: %v", err)
	}
	if shaA == "" || shaB == "" {
		t.Fatalf("empty SHA: a=%q b=%q", shaA, shaB)
	}
	if shaA == shaB {
		t.Errorf("expected distinct subtree SHAs, both = %q", shaA)
	}
	if len(shaA) != 40 {
		t.Errorf("expected 40-char SHA, got %q (len %d)", shaA, len(shaA))
	}

	// Default ref ("") resolves to HEAD and should equal the explicit "main".
	shaDefault, err := SubtreeSHA(ctx, dir, "", "skill-a")
	if err != nil {
		t.Fatalf("SubtreeSHA default ref: %v", err)
	}
	if shaDefault != shaA {
		t.Errorf("default-ref SHA %q != main SHA %q", shaDefault, shaA)
	}

	// Stability: changing skill-b must not change skill-a's revision.
	writeFile(t, filepath.Join(dir, "skill-b", "extra.md"), "more\n")
	if _, err := runGit(ctx, dir, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(ctx, dir, "commit", "-q", "-m", "touch b"); err != nil {
		t.Fatal(err)
	}
	shaA2, err := SubtreeSHA(ctx, dir, "main", "skill-a")
	if err != nil {
		t.Fatalf("SubtreeSHA skill-a after b change: %v", err)
	}
	if shaA2 != shaA {
		t.Errorf("skill-a revision changed (%q -> %q) after editing skill-b", shaA, shaA2)
	}
	shaB2, err := SubtreeSHA(ctx, dir, "main", "skill-b")
	if err != nil {
		t.Fatalf("SubtreeSHA skill-b after change: %v", err)
	}
	if shaB2 == shaB {
		t.Errorf("skill-b revision did not change after editing it")
	}
}

func TestSubtreeSHAMissing(t *testing.T) {
	dir := initRepo(t)
	if _, err := SubtreeSHA(context.Background(), dir, "main", "does-not-exist"); err == nil {
		t.Error("expected error for missing subpath, got nil")
	}
}

func TestDefaultRef(t *testing.T) {
	dir := initRepo(t)
	// No origin remote configured here, so DefaultRef falls back to the
	// checked-out branch, which initRepo set to "main".
	ref, err := DefaultRef(context.Background(), dir)
	if err != nil {
		t.Fatalf("DefaultRef: %v", err)
	}
	if ref != "main" {
		t.Errorf("DefaultRef = %q, want %q", ref, "main")
	}
}

func TestMaterializeSubdir(t *testing.T) {
	dir := initRepo(t)
	dest := filepath.Join(t.TempDir(), "out")

	if err := MaterializeSubdir(context.Background(), dir, "skill-a", dest); err != nil {
		t.Fatalf("MaterializeSubdir: %v", err)
	}

	// Contents of skill-a should be at the root of dest, not under skill-a/.
	if _, err := os.Stat(filepath.Join(dest, "SKILL.md")); err != nil {
		t.Errorf("expected SKILL.md at dest root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "ref.md")); err != nil {
		t.Errorf("expected ref.md at dest root: %v", err)
	}
	// Files from other skills / repo root must not leak in.
	if _, err := os.Stat(filepath.Join(dest, "README.md")); !os.IsNotExist(err) {
		t.Errorf("README.md should not be materialized: err=%v", err)
	}
	// No .git directory should be copied.
	if _, err := os.Stat(filepath.Join(dest, ".git")); !os.IsNotExist(err) {
		t.Errorf(".git should not be materialized: err=%v", err)
	}
}

func TestMaterializeSubdirNotDir(t *testing.T) {
	dir := initRepo(t)
	dest := filepath.Join(t.TempDir(), "out")
	// README.md is a file, not a directory.
	if err := MaterializeSubdir(context.Background(), dir, "README.md", dest); err == nil {
		t.Error("expected error materializing a non-directory, got nil")
	}
}

func TestTreelessCloneEmptyArgs(t *testing.T) {
	if err := TreelessClone(context.Background(), "", "main", "/tmp/x"); err == nil {
		t.Error("expected error for empty url")
	}
	if err := TreelessClone(context.Background(), "https://example.com/r.git", "main", ""); err == nil {
		t.Error("expected error for empty destDir")
	}
}

// TestTreelessCloneLocal exercises the real clone path against a local repo
// (file:// URL), which needs no network. Branch-ref and SHA-ref paths are both
// covered.
func TestTreelessCloneLocal(t *testing.T) {
	src := initRepo(t)
	ctx := context.Background()

	// Branch ref path.
	destBranch := filepath.Join(t.TempDir(), "clone-branch")
	if err := TreelessClone(ctx, src, "main", destBranch); err != nil {
		t.Fatalf("TreelessClone branch: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destBranch, "skill-a", "SKILL.md")); err != nil {
		t.Errorf("cloned branch missing skill-a/SKILL.md: %v", err)
	}

	// No-ref path (default branch).
	destDefault := filepath.Join(t.TempDir(), "clone-default")
	if err := TreelessClone(ctx, src, "", destDefault); err != nil {
		t.Fatalf("TreelessClone default: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destDefault, "README.md")); err != nil {
		t.Errorf("cloned default missing README.md: %v", err)
	}

	// SHA ref path: resolve HEAD's commit and clone it by SHA.
	sha, err := runGit(ctx, src, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	destSHA := filepath.Join(t.TempDir(), "clone-sha")
	if err := TreelessClone(ctx, src, sha, destSHA); err != nil {
		t.Fatalf("TreelessClone sha: %v", err)
	}
	got, err := runGit(ctx, destSHA, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if got != sha {
		t.Errorf("cloned SHA HEAD = %q, want %q", got, sha)
	}
}
