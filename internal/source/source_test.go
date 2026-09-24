package source

import (
	"os"
	"path/filepath"
	"reflect"
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
	// A shorthand-shaped relative path that really exists, to prove an existing
	// directory still beats the GitHub shorthand reading. Run from a temp cwd so
	// the relative form resolves to it.
	const shorthandDir = "acme/skills"
	if err := os.MkdirAll(filepath.Join(dir, shorthandDir), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", shorthandDir, err)
	}
	t.Chdir(dir)

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

		// GitHub owner/repo shorthand: a remote, but only when no directory of
		// that name exists (see the shorthand cases in TestGitHubShorthand).
		{name: "shorthand", arg: "railwayapp/railway-skills", want: Git},
		{name: "shorthand dotted repo", arg: "owner/.github", want: Git},
		{name: "shorthand loses to existing dir", arg: shorthandDir, want: Local},

		{name: "empty", arg: "", wantErr: true},
		{name: "blank", arg: "   ", wantErr: true},
		{name: "missing path", arg: filepath.Join(dir, "does-not-exist"), wantErr: true},
		{name: "regular file", arg: file, wantErr: true},
		{name: "plain word no host", arg: "name:something", wantErr: true}, // ambiguous bare host w/o dot, not a path
		{name: "deeper path is not shorthand", arg: "owner/repo/sub", wantErr: true},
		{name: "missing relative path is not shorthand", arg: "./missing/dir", wantErr: true},
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

func TestLooksLikeSource(t *testing.T) {
	tests := []struct {
		name string
		arg  string
		want bool
	}{
		// Git remotes are Sources (shape only, no filesystem access).
		{"https url", "https://github.com/ultrakorne/skill-collection", true},
		{"https .git", "https://github.com/x/y.git", true},
		{"scp git@host", "git@github.com:ultrakorne/repo.git", true},
		{"scp bare host with dot", "github.com:ultrakorne/repo", true},
		{"trailing .git path", "some/thing.git", true},

		// Explicitly path-shaped local paths are Sources.
		{"dot-slash", "./my-skill", true},
		{"dot-dot-slash", "../my-skill", true},
		{"absolute", "/abs/path/skill", true},
		{"nested relative", "a/b", true},
		{"backslash (windows)", `dir\skill`, true},
		{"tilde", "~", true},
		{"tilde slash", "~/skills/foo", true},
		{"tilde user", "~bob/skill", true},

		// Bare single-segment names are in-Home ids, never Sources — even when a
		// same-named directory exists in cwd (this is shape-only, so we can't tell
		// and deliberately don't look).
		{"bare name", "grill-with-docs", false},
		{"bare name with dot", "my.skill", false},
		{"empty", "", false},
		{"blank", "   ", false},
		// Padding is trimmed before shape detection.
		{"padded url", "  https://github.com/x/y  ", true},
		{"padded bare name", "  grill  ", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LooksLikeSource(tt.arg); got != tt.want {
				t.Errorf("LooksLikeSource(%q) = %v, want %v", tt.arg, got, tt.want)
			}
		})
	}
}

func TestGitHubShorthand(t *testing.T) {
	tests := []struct {
		name string
		arg  string
		want string // "" means: not a shorthand
	}{
		{"owner repo", "railwayapp/railway-skills", "https://github.com/railwayapp/railway-skills.git"},
		{"underscores and digits", "acme_2/skills_v2", "https://github.com/acme_2/skills_v2.git"},
		{"dotted repo", "owner/.github", "https://github.com/owner/.github.git"},
		{"mixed case preserved", "Owner/Repo", "https://github.com/Owner/Repo.git"},
		{"trailing .git stripped once", "owner/repo.git", "https://github.com/owner/repo.git"},
		{"padded", "  owner/repo  ", "https://github.com/owner/repo.git"},

		// Everything that must NOT be read as a shorthand.
		{"bare name", "grill-with-docs", ""},
		{"deeper path", "owner/repo/sub", ""},
		{"leading slash", "/owner/repo", ""},
		{"dot slash", "./repo", ""},
		{"dot dot slash", "../repo", ""},
		{"tilde", "~/repo", ""},
		{"https url", "https://github.com/owner/repo", ""},
		{"scp remote", "git@github.com:owner/repo.git", ""},
		{"host path", "github.com/owner/repo", ""},
		{"backslash", `dir\repo`, ""},
		{"inner space", "owner/re po", ""},
		{"empty owner", "/repo", ""},
		{"empty repo", "owner/", ""},
		{"repo is just .git", "owner/.git", ""},
		{"dot dot segment", "owner/..", ""},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := GitHubShorthand(tt.arg)
			if tt.want == "" {
				if ok {
					t.Fatalf("GitHubShorthand(%q) = %q, true; want not a shorthand", tt.arg, got)
				}
				return
			}
			if !ok {
				t.Fatalf("GitHubShorthand(%q) = not a shorthand; want %q", tt.arg, tt.want)
			}
			if got != tt.want {
				t.Fatalf("GitHubShorthand(%q) = %q, want %q", tt.arg, got, tt.want)
			}
		})
	}
}

func TestGitRemote(t *testing.T) {
	tests := []struct {
		name, arg, want string
	}{
		// A shorthand becomes the same Source as its full HTTPS URL would be.
		{"shorthand expanded", "owner/repo", "https://github.com/owner/repo.git"},
		{"shorthand with .git", "owner/repo.git", "https://github.com/owner/repo.git"},
		// Every other remote is passed through untouched, padding aside.
		{"https url", "https://github.com/owner/repo", "https://github.com/owner/repo"},
		{"https url .git", "https://github.com/owner/repo.git", "https://github.com/owner/repo.git"},
		{"ssh scp", "git@github.com:owner/repo.git", "git@github.com:owner/repo.git"},
		{"gitlab url", "https://gitlab.com/group/sub/repo.git", "https://gitlab.com/group/sub/repo.git"},
		{"padded url is trimmed", "  https://github.com/x/y  ", "https://github.com/x/y"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GitRemote(tt.arg); got != tt.want {
				t.Errorf("GitRemote(%q) = %q, want %q", tt.arg, got, tt.want)
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

// A repo that ships one skill per agent (as pbakaus/impeccable does) commits a
// copy of the same skill under every agent folder; discovery must report it
// once, from the conventional location, not once per copy.
func TestDiscoverSkills_DedupesPerAgentCopies(t *testing.T) {
	cases := []struct {
		name    string
		dirs    []string
		wantDir string
	}{
		{
			name: "agents folder wins over agent-specific copies",
			dirs: []string{
				".agents/skills/impeccable",
				".claude/skills/impeccable",
				".cursor/skills/impeccable",
				"cursor-plugin/skills/impeccable",
				"plugin/skills/impeccable",
				"tests/ws/.claude/skills/impeccable",
			},
			wantDir: ".agents/skills/impeccable",
		},
		{
			name: "top-level skills folder wins over agents folder",
			dirs: []string{
				".agents/skills/impeccable",
				".claude/skills/impeccable",
				"skills/impeccable",
			},
			wantDir: "skills/impeccable",
		},
		{
			name: "shallowest wins without a conventional folder",
			dirs: []string{
				"a/b/impeccable",
				"z/impeccable",
			},
			wantDir: "z/impeccable",
		},
		{
			name: "nested skills folder wins over a shallower fixture",
			dirs: []string{
				"skills/writing/impeccable",
				"test/impeccable",
			},
			wantDir: "skills/writing/impeccable",
		},
		{
			name: "nested skills folder wins over an agent copy",
			dirs: []string{
				".claude/skills/impeccable",
				"skills/writing/impeccable",
			},
			wantDir: "skills/writing/impeccable",
		},
		{
			name: "curated skills folder wins over an agent copy",
			dirs: []string{
				".codex/skills/impeccable",
				"skills/.curated/impeccable",
			},
			wantDir: "skills/.curated/impeccable",
		},
		{
			name: "shallowest wins within the skills folder",
			dirs: []string{
				"skills/cat/impeccable",
				"skills/impeccable",
			},
			wantDir: "skills/impeccable",
		},
		{
			name: "walk order breaks a tie",
			dirs: []string{
				"plugin/skills/impeccable",
				"cursor-plugin/skills/impeccable",
			},
			wantDir: "cursor-plugin/skills/impeccable",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for _, d := range tc.dirs {
				writeSkill(t, filepath.Join(root, filepath.FromSlash(d)), "impeccable")
			}
			writeSkill(t, filepath.Join(root, "other"), "other")

			found, err := DiscoverSkills(root)
			if err != nil {
				t.Fatalf("DiscoverSkills: %v", err)
			}
			if got, want := ids(found), []string{"impeccable", "other"}; !reflect.DeepEqual(got, want) {
				t.Fatalf("ids = %v, want %v", got, want)
			}
			for _, f := range found {
				if f.Id != "impeccable" {
					continue
				}
				if f.Dir != filepath.Join(root, filepath.FromSlash(tc.wantDir)) {
					t.Errorf("Dir = %q, want %q", f.Dir, tc.wantDir)
				}
				if len(f.Duplicates) != len(tc.dirs)-1 {
					t.Errorf("Duplicates = %v, want the %d other copies", f.Duplicates, len(tc.dirs)-1)
				}
				for _, d := range f.Duplicates {
					if d == f.Dir {
						t.Errorf("Duplicates contains the kept Dir %q", d)
					}
				}
			}
		})
	}
}

// The kept copy of a skill stays where the skill's first copy was in walk
// order, even when the copy that wins comes after another skill.
func TestDiscoverSkills_DedupeKeepsWalkOrder(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, filepath.Join(root, ".claude", "skills", "foo"), "foo")
	writeSkill(t, filepath.Join(root, "aaa"), "aaa")
	writeSkill(t, filepath.Join(root, "skills", "foo"), "foo")

	found, err := DiscoverSkills(root)
	if err != nil {
		t.Fatalf("DiscoverSkills: %v", err)
	}
	var got []string
	for _, f := range found {
		rel, _ := filepath.Rel(root, f.Dir)
		got = append(got, f.Id+"@"+filepath.ToSlash(rel))
	}
	if want := []string{"foo@skills/foo", "aaa@aaa"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("found = %v, want %v", got, want)
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

// TestDiscoverSkills_SymlinkedRoot: a root that is a symlink to a directory is
// walked whether or not it carries a trailing separator (the form shell
// tab-completion gives), and the recorded Dir is clean either way.
func TestDiscoverSkills_SymlinkedRoot(t *testing.T) {
	real := t.TempDir()
	writeSkill(t, filepath.Join(real, "alpha"), "alpha")
	writeSkill(t, filepath.Join(real, "beta"), "beta")
	link := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	for _, root := range []string{link, link + string(filepath.Separator)} {
		found, err := DiscoverSkills(root)
		if err != nil {
			t.Fatalf("DiscoverSkills(%q): %v", root, err)
		}
		if got := ids(found); len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
			t.Fatalf("DiscoverSkills(%q) = %v, want [alpha beta]", root, got)
		}
		if want := filepath.Join(link, "alpha"); found[0].Dir != want {
			t.Errorf("DiscoverSkills(%q): Dir = %q, want %q", root, found[0].Dir, want)
		}
	}

	// A symlinked root that is itself the skill.
	skillLink := filepath.Join(t.TempDir(), "alpha")
	if err := os.Symlink(filepath.Join(real, "alpha"), skillLink); err != nil {
		t.Fatal(err)
	}
	found, err := DiscoverSkills(skillLink + string(filepath.Separator))
	if err != nil || len(found) != 1 || found[0].Dir != skillLink || found[0].Id != "alpha" {
		t.Fatalf("root skill through a symlink: found=%+v err=%v", found, err)
	}
}

func TestIsPathRemote(t *testing.T) {
	for arg, want := range map[string]bool{
		"./catalog.git":                      true,
		"../x/catalog.git":                   true,
		"catalog.git":                        true,
		"/abs/catalog.git":                   true,
		"https://github.com/acme/skills.git": false,
		"file:///srv/catalog.git":            false,
		"git@github.com:acme/skills.git":     false,
		"github.com:acme/skills.git":         false,
		"ssh://git@example.com/acme/x.git":   false,
	} {
		if got := IsPathRemote(arg); got != want {
			t.Errorf("IsPathRemote(%q) = %v, want %v", arg, got, want)
		}
	}
}
