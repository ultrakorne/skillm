package source

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestClassify(t *testing.T) {
	// A real local directory to exercise the Local branch.
	dir := t.TempDir()
	// A real file to exercise the "file, not a directory" error.
	file := filepath.Join(dir, "afile.txt")
	if err := os.WriteFile(file, []byte("hi"), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	tests := []struct {
		name    string
		arg     string
		want    Kind
		wantErr bool
	}{
		{name: "https", arg: "https://github.com/ultrakorne/skill-collection", want: Git},
		{name: "https with .git", arg: "https://github.com/ultrakorne/skill-collection.git", want: Git},
		{name: "http", arg: "http://example.com/repo", want: Git},
		{name: "ssh scheme", arg: "ssh://git@github.com/ultrakorne/repo.git", want: Git},
		{name: "git scheme", arg: "git://github.com/ultrakorne/repo", want: Git},
		{name: "file scheme", arg: "file:///tmp/repo", want: Git},
		{name: "scp git@host", arg: "git@github.com:ultrakorne/skill-collection.git", want: Git},
		{name: "scp git@host no .git", arg: "git@github.com:ultrakorne/skill-collection", want: Git},
		{name: "scp bare host with dot", arg: "github.com:ultrakorne/repo", want: Git},
		{name: "trailing .git path", arg: "some/relative/thing.git", want: Git},
		{name: "uppercase scheme", arg: "HTTPS://github.com/x/y", want: Git},
		{name: "padded url", arg: "  https://github.com/x/y  ", want: Git},

		{name: "existing dir", arg: dir, want: Local},
		{name: "dot relative is not scp", arg: ".", want: Local}, // cwd exists as a dir

		{name: "empty", arg: "", wantErr: true},
		{name: "blank", arg: "   ", wantErr: true},
		{name: "missing path", arg: filepath.Join(dir, "does-not-exist"), wantErr: true},
		{name: "regular file", arg: file, wantErr: true},
		{name: "plain word no host", arg: "name:something", wantErr: true}, // ambiguous bare host w/o dot, not a path
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Classify(tt.arg)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Classify(%q) = %v, want error", tt.arg, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Classify(%q) unexpected error: %v", tt.arg, err)
			}
			if got != tt.want {
				t.Fatalf("Classify(%q) = %v, want %v", tt.arg, got, tt.want)
			}
		})
	}
}

func TestKindString(t *testing.T) {
	if Git.String() != "git" {
		t.Errorf("Git.String() = %q, want %q", Git.String(), "git")
	}
	if Local.String() != "local" {
		t.Errorf("Local.String() = %q, want %q", Local.String(), "local")
	}
}

// writeSkill creates dir/SKILL.md with a minimal frontmatter naming the skill.
func writeSkill(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	content := "---\nname: " + name + "\ndescription: test skill\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write SKILL.md in %s: %v", dir, err)
	}
}

func ids(found []Found) []string {
	out := make([]string, len(found))
	for i, f := range found {
		out[i] = f.Id
	}
	sort.Strings(out)
	return out
}

func TestDiscoverSkills_Zero(t *testing.T) {
	root := t.TempDir()
	// A couple of directories and files, but no SKILL.md anywhere.
	if err := os.MkdirAll(filepath.Join(root, "docs", "nested"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	found, err := DiscoverSkills(root)
	if err != nil {
		t.Fatalf("DiscoverSkills: %v", err)
	}
	if len(found) != 0 {
		t.Fatalf("expected 0 skills, got %d: %v", len(found), ids(found))
	}
}

func TestDiscoverSkills_One(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, filepath.Join(root, "grill-with-docs"), "grill-with-docs")

	found, err := DiscoverSkills(root)
	if err != nil {
		t.Fatalf("DiscoverSkills: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("expected 1 skill, got %d: %v", len(found), ids(found))
	}
	f := found[0]
	if f.Id != "grill-with-docs" {
		t.Errorf("Id = %q, want %q", f.Id, "grill-with-docs")
	}
	if f.Skill == nil {
		t.Fatal("Skill is nil")
	}
	if f.Skill.Name != "grill-with-docs" {
		t.Errorf("Skill.Name = %q, want %q", f.Skill.Name, "grill-with-docs")
	}
	if f.Dir != filepath.Join(root, "grill-with-docs") {
		t.Errorf("Dir = %q, want %q", f.Dir, filepath.Join(root, "grill-with-docs"))
	}
}

func TestDiscoverSkills_Many(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, filepath.Join(root, "alpha"), "alpha")
	writeSkill(t, filepath.Join(root, "beta"), "beta")
	writeSkill(t, filepath.Join(root, "nested", "gamma"), "gamma")

	// A nested SKILL.md *inside* an already-found skill must NOT be reported
	// as a separate skill (one directory == one skill, no recursion).
	writeSkill(t, filepath.Join(root, "alpha", "examples", "inner"), "inner")

	// A .git directory should be skipped entirely.
	writeSkill(t, filepath.Join(root, ".git", "evil"), "evil")

	found, err := DiscoverSkills(root)
	if err != nil {
		t.Fatalf("DiscoverSkills: %v", err)
	}

	got := ids(found)
	want := []string{"alpha", "beta", "gamma"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestDiscoverSkills_RootIsSkill(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "root-skill")

	found, err := DiscoverSkills(root)
	if err != nil {
		t.Fatalf("DiscoverSkills: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("expected 1 skill (root), got %d: %v", len(found), ids(found))
	}
	if found[0].Dir != root {
		t.Errorf("Dir = %q, want root %q", found[0].Dir, root)
	}
	// ID is the base name of the temp dir.
	if found[0].Id != filepath.Base(root) {
		t.Errorf("Id = %q, want %q", found[0].Id, filepath.Base(root))
	}
}

func TestDiscoverSkills_NotADir(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "afile")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := DiscoverSkills(file); err == nil {
		t.Fatal("expected error for non-directory rootDir")
	}
	if _, err := DiscoverSkills(filepath.Join(root, "missing")); err == nil {
		t.Fatal("expected error for missing rootDir")
	}
}
