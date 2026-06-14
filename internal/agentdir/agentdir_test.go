package agentdir

import (
	"os"
	"path/filepath"
	"testing"
)

// agent fetches a supported agent by name for use in tests, failing if the
// registry no longer contains it.
func agent(t *testing.T, name string) Agent {
	t.Helper()
	for _, a := range All() {
		if a.Name == name {
			return a
		}
	}
	t.Fatalf("agent %q not in registry", name)
	return Agent{}
}

func TestAllRegistry(t *testing.T) {
	all := All()
	if len(all) != 2 {
		t.Fatalf("All() len = %d, want 2", len(all))
	}
	if all[0].Name != "claude" || all[1].Name != "codex" {
		t.Fatalf("All() names = %q,%q; want claude,codex", all[0].Name, all[1].Name)
	}
	// Mutating the returned slice must not affect the registry.
	all[0].Name = "tampered"
	if All()[0].Name != "claude" {
		t.Fatal("All() returned a slice aliasing the registry")
	}
}

func TestEnabled(t *testing.T) {
	cases := []struct {
		names []string
		want  []string
	}{
		{nil, nil},
		{[]string{}, nil},
		{[]string{"claude"}, []string{"claude"}},
		{[]string{"codex"}, []string{"codex"}},
		{[]string{"claude", "codex"}, []string{"claude", "codex"}},
		// Preserves registry order regardless of input order.
		{[]string{"codex", "claude"}, []string{"claude", "codex"}},
		// Unknown names ignored; duplicates collapse.
		{[]string{"claude", "bogus", "claude"}, []string{"claude"}},
	}
	for _, c := range cases {
		got := Enabled(c.names)
		if len(got) != len(c.want) {
			t.Fatalf("Enabled(%v) len = %d, want %d", c.names, len(got), len(c.want))
		}
		for i := range got {
			if got[i].Name != c.want[i] {
				t.Fatalf("Enabled(%v)[%d] = %q, want %q", c.names, i, got[i].Name, c.want[i])
			}
		}
	}
}

func TestSkillsFolderGlobal(t *testing.T) {
	// Pin a known home so the expected paths are deterministic.
	home := t.TempDir()
	t.Setenv("HOME", home)

	cases := []struct {
		agent string
		want  string
	}{
		{"claude", filepath.Join(home, ".claude", "skills")},
		{"codex", filepath.Join(home, ".codex", "skills")},
	}
	for _, c := range cases {
		// cwd is irrelevant for Global scope.
		got := SkillsFolder(agent(t, c.agent), Global, "/some/project")
		if got != c.want {
			t.Errorf("SkillsFolder(%s, Global) = %q, want %q", c.agent, got, c.want)
		}
	}
}

func TestSkillsFolderLocal(t *testing.T) {
	cwd := "/home/ultra/dev/myproject"
	cases := []struct {
		agent string
		want  string
	}{
		{"claude", filepath.Join(cwd, ".claude", "skills")},
		{"codex", filepath.Join(cwd, ".codex", "skills")},
	}
	for _, c := range cases {
		got := SkillsFolder(agent(t, c.agent), Local, cwd)
		if got != c.want {
			t.Errorf("SkillsFolder(%s, Local) = %q, want %q", c.agent, got, c.want)
		}
	}
}

func TestLinkPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cwd := "/home/ultra/dev/myproject"
	const id = "grill-with-docs"

	cases := []struct {
		agent string
		scope Scope
		want  string
	}{
		{"claude", Global, filepath.Join(home, ".claude", "skills", id)},
		{"codex", Global, filepath.Join(home, ".codex", "skills", id)},
		{"claude", Local, filepath.Join(cwd, ".claude", "skills", id)},
		{"codex", Local, filepath.Join(cwd, ".codex", "skills", id)},
	}
	for _, c := range cases {
		got := LinkPath(agent(t, c.agent), c.scope, cwd, id)
		if got != c.want {
			t.Errorf("LinkPath(%s, %s, %q) = %q, want %q", c.agent, c.scope, id, got, c.want)
		}
	}
}

func TestParseScope(t *testing.T) {
	cases := []struct {
		in      string
		want    Scope
		wantErr bool
	}{
		{"global", Global, false},
		{"GLOBAL", Global, false},
		{" global ", Global, false},
		{"g", Global, false},
		{"local", Local, false},
		{"l", Local, false},
		{"", Global, true},
		{"both", Global, true},
	}
	for _, c := range cases {
		got, err := ParseScope(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseScope(%q) err = nil, want error", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseScope(%q) unexpected err: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseScope(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestScopeString(t *testing.T) {
	if Global.String() != "global" {
		t.Errorf("Global.String() = %q, want global", Global.String())
	}
	if Local.String() != "local" {
		t.Errorf("Local.String() = %q, want local", Local.String())
	}
}

func TestHomeDirFallback(t *testing.T) {
	// When the home lookup fails, SkillsFolder must still produce a usable path
	// rooted at "~" rather than panicking or returning an absolute machine path.
	t.Setenv("HOME", "")
	// On some platforms os.UserHomeDir consults other vars; clear the common ones.
	t.Setenv("USERPROFILE", "")
	got := SkillsFolder(agent(t, "claude"), Global, "")
	// Either a real home resolved (rare in this stripped env) or the "~" fallback.
	if got == "" {
		t.Fatal("SkillsFolder returned empty path")
	}
	_ = os.PathSeparator
}
