package store

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestHome_OverrideWins(t *testing.T) {
	t.Setenv("SKILLM_HOME", filepath.FromSlash("/env/path"))
	override := filepath.FromSlash("/override/path")
	got, err := Home(override)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Clean(override)
	if got != want {
		t.Errorf("Home(override) = %q, want %q (override beats env)", got, want)
	}
}

func TestHome_EnvWhenNoOverride(t *testing.T) {
	env := filepath.FromSlash("/env/skillm")
	t.Setenv("SKILLM_HOME", env)
	got, err := Home("")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Clean(env)
	if got != want {
		t.Errorf("Home(\"\") = %q, want %q", got, want)
	}
}

func TestHome_DefaultUnderUserHome(t *testing.T) {
	t.Setenv("SKILLM_HOME", "")
	hd, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no user home dir: %v", err)
	}
	got, err := Home("")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(hd, ".skillm")
	if got != want {
		t.Errorf("Home(\"\") = %q, want default %q", got, want)
	}
}

func TestEnsureHomeAndLayout(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".skillm")
	if err := EnsureHome(home); err != nil {
		t.Fatalf("EnsureHome: %v", err)
	}
	// Idempotent.
	if err := EnsureHome(home); err != nil {
		t.Fatalf("EnsureHome (second call): %v", err)
	}

	info, err := os.Stat(SkillsDir(home))
	if err != nil || !info.IsDir() {
		t.Fatalf("skills dir not created: %v", err)
	}
	if SkillDir(home, "x") != filepath.Join(home, "skills", "x") {
		t.Errorf("SkillDir wrong: %q", SkillDir(home, "x"))
	}
}

func TestEnsureHome_EmptyError(t *testing.T) {
	if err := EnsureHome(""); err == nil {
		t.Fatal("expected error for empty home")
	}
}

func TestAddSkillDir_RoundTrip(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".skillm")
	if err := EnsureHome(home); err != nil {
		t.Fatal(err)
	}

	// Build a source skill dir with nested files and an executable script.
	src := t.TempDir()
	mustWrite(t, filepath.Join(src, "SKILL.md"), "---\nname: demo\n---\nbody\n", 0o644)
	mustWrite(t, filepath.Join(src, "ref", "guide.md"), "guide\n", 0o644)
	mustWrite(t, filepath.Join(src, "scripts", "run.sh"), "#!/bin/sh\necho hi\n", 0o755)

	if err := AddSkillDir(home, "demo", src); err != nil {
		t.Fatalf("AddSkillDir: %v", err)
	}
	if !Exists(home, "demo") {
		t.Fatal("Exists should report the added skill")
	}

	dst := SkillDir(home, "demo")
	assertFile(t, filepath.Join(dst, "SKILL.md"), "---\nname: demo\n---\nbody\n")
	assertFile(t, filepath.Join(dst, "ref", "guide.md"), "guide\n")
	assertFile(t, filepath.Join(dst, "scripts", "run.sh"), "#!/bin/sh\necho hi\n")

	// Executable bit preserved (POSIX only).
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(dst, "scripts", "run.sh"))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o111 == 0 {
			t.Errorf("executable bit not preserved: mode %v", info.Mode())
		}
	}
}

func TestAddSkillDir_Collision(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".skillm")
	if err := EnsureHome(home); err != nil {
		t.Fatal(err)
	}
	src := t.TempDir()
	mustWrite(t, filepath.Join(src, "SKILL.md"), "x\n", 0o644)

	if err := AddSkillDir(home, "dup", src); err != nil {
		t.Fatalf("first AddSkillDir: %v", err)
	}
	if err := AddSkillDir(home, "dup", src); err == nil {
		t.Fatal("expected collision error on duplicate id")
	}
}

func TestAddSkillDir_SourceNotDir(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".skillm")
	if err := EnsureHome(home); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(t.TempDir(), "file.txt")
	mustWrite(t, f, "x", 0o644)
	if err := AddSkillDir(home, "id", f); err == nil {
		t.Fatal("expected error: source is not a directory")
	}
}

func TestRemoveSkillDir(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".skillm")
	if err := EnsureHome(home); err != nil {
		t.Fatal(err)
	}
	src := t.TempDir()
	mustWrite(t, filepath.Join(src, "SKILL.md"), "x\n", 0o644)
	if err := AddSkillDir(home, "gone", src); err != nil {
		t.Fatal(err)
	}

	if err := RemoveSkillDir(home, "gone"); err != nil {
		t.Fatalf("RemoveSkillDir: %v", err)
	}
	if Exists(home, "gone") {
		t.Fatal("skill should be gone after RemoveSkillDir")
	}
	// Idempotent: removing again is not an error.
	if err := RemoveSkillDir(home, "gone"); err != nil {
		t.Errorf("RemoveSkillDir on absent skill should be nil, got %v", err)
	}
}

func mustWrite(t *testing.T, path, content string, perm os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), perm); err != nil {
		t.Fatal(err)
	}
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(got) != want {
		t.Errorf("%s = %q, want %q", path, string(got), want)
	}
}
