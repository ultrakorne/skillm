package agentdir

import (
	"path/filepath"
	"runtime"
	"testing"
)

// setTestHome pins the user home directory for the duration of the test. On
// Windows, os.UserHomeDir reads USERPROFILE rather than HOME.
func setTestHome(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", dir)
	}
}

// claudeAgent and codexAgent are the conventional definitions, constructed
// directly: the catalog now lives in config, not in this package.
func claudeAgent() Agent {
	return Agent{Name: "claude", Global: "~/.claude/skills", Local: ".claude/skills"}
}

func codexAgent() Agent {
	return Agent{Name: "codex", Global: "~/.codex/skills", Local: ".codex/skills"}
}

func TestSupports(t *testing.T) {
	cases := []struct {
		a             Agent
		global, local bool
	}{
		{Agent{Name: "both", Global: "~/.x/skills", Local: ".x/skills"}, true, true},
		{Agent{Name: "globalonly", Global: "~/.x/skills"}, true, false},
		{Agent{Name: "localonly", Local: ".x/skills"}, false, true},
		{Agent{Name: "none"}, false, false},
	}
	for _, c := range cases {
		if got := c.a.Supports(Global); got != c.global {
			t.Errorf("%s.Supports(Global) = %v, want %v", c.a.Name, got, c.global)
		}
		if got := c.a.Supports(Local); got != c.local {
			t.Errorf("%s.Supports(Local) = %v, want %v", c.a.Name, got, c.local)
		}
	}
}

func TestSkillsFolderGlobal(t *testing.T) {
	// Pin a known home so the expected paths are deterministic.
	home := t.TempDir()
	setTestHome(t, home)

	cases := []struct {
		agent Agent
		want  string
	}{
		{claudeAgent(), filepath.Join(home, ".claude", "skills")},
		{codexAgent(), filepath.Join(home, ".codex", "skills")},
	}
	for _, c := range cases {
		// base is irrelevant for Global scope.
		got, ok := SkillsFolder(c.agent, Global, "/some/project")
		if !ok {
			t.Errorf("SkillsFolder(%s, Global) ok = false, want true", c.agent.Name)
			continue
		}
		if got != c.want {
			t.Errorf("SkillsFolder(%s, Global) = %q, want %q", c.agent.Name, got, c.want)
		}
	}
}

func TestSkillsFolderLocal(t *testing.T) {
	base := "/home/ultra/dev/myproject"
	cases := []struct {
		agent Agent
		want  string
	}{
		{claudeAgent(), filepath.Join(base, ".claude", "skills")},
		{codexAgent(), filepath.Join(base, ".codex", "skills")},
	}
	for _, c := range cases {
		got, ok := SkillsFolder(c.agent, Local, base)
		if !ok {
			t.Errorf("SkillsFolder(%s, Local) ok = false, want true", c.agent.Name)
			continue
		}
		if got != c.want {
			t.Errorf("SkillsFolder(%s, Local) = %q, want %q", c.agent.Name, got, c.want)
		}
	}
}

// TestSkillsFolderMissingScope verifies that an agent which defines only one
// scope reports (_, false) for the scope it does not define, so callers skip it.
func TestSkillsFolderMissingScope(t *testing.T) {
	globalOnly := Agent{Name: "g", Global: "~/.g/skills"}
	if _, ok := SkillsFolder(globalOnly, Local, "/proj"); ok {
		t.Error("SkillsFolder(global-only, Local) ok = true, want false")
	}
	if _, ok := SkillsFolder(globalOnly, Global, "/proj"); !ok {
		t.Error("SkillsFolder(global-only, Global) ok = false, want true")
	}

	localOnly := Agent{Name: "l", Local: ".l/skills"}
	if _, ok := SkillsFolder(localOnly, Global, "/proj"); ok {
		t.Error("SkillsFolder(local-only, Global) ok = true, want false")
	}
	if _, ok := SkillsFolder(localOnly, Local, "/proj"); !ok {
		t.Error("SkillsFolder(local-only, Local) ok = false, want true")
	}
}

// TestGlobalTemplateForms covers ~ expansion, the bare "~", relative-rooted-at-home,
// and absolute-taken-as-is for Global locations.
func TestGlobalTemplateForms(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	// Use a real temp dir as the "absolute path taken as-is" case so the path
	// is genuinely absolute on every platform (Windows paths need a drive letter).
	absDir := t.TempDir()

	cases := []struct {
		in   string
		want string
	}{
		{"~/.config/opencode/skill", filepath.Join(home, ".config", "opencode", "skill")},
		{"~", home},
		{".claude/skills", filepath.Join(home, ".claude", "skills")}, // relative rooted at home
		{absDir, absDir},                                              // absolute as-is
	}
	for _, c := range cases {
		got, ok := SkillsFolder(Agent{Name: "x", Global: c.in}, Global, "")
		if !ok || got != c.want {
			t.Errorf("Global %q -> (%q, %v), want (%q, true)", c.in, got, ok, c.want)
		}
	}
}

func TestLinkPath(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	base := "/home/ultra/dev/myproject"
	const id = "grill-with-docs"

	cases := []struct {
		agent Agent
		scope Scope
		want  string
	}{
		{claudeAgent(), Global, filepath.Join(home, ".claude", "skills", id)},
		{codexAgent(), Global, filepath.Join(home, ".codex", "skills", id)},
		{claudeAgent(), Local, filepath.Join(base, ".claude", "skills", id)},
		{codexAgent(), Local, filepath.Join(base, ".codex", "skills", id)},
	}
	for _, c := range cases {
		got, ok := LinkPath(c.agent, c.scope, base, id)
		if !ok {
			t.Errorf("LinkPath(%s, %s) ok = false, want true", c.agent.Name, c.scope)
			continue
		}
		if got != c.want {
			t.Errorf("LinkPath(%s, %s, %q) = %q, want %q", c.agent.Name, c.scope, id, got, c.want)
		}
	}
}

func TestLinkPathMissingScope(t *testing.T) {
	globalOnly := Agent{Name: "g", Global: "~/.g/skills"}
	if _, ok := LinkPath(globalOnly, Local, "/proj", "id"); ok {
		t.Error("LinkPath(global-only, Local) ok = true, want false")
	}
}

// TestLocalAliasesGlobal verifies the per-agent alias rule: an agent's local
// folder aliases its global folder exactly when base resolves to the agent's
// global parent (the canonical case is base == home), and only when the agent
// defines both folders.
func TestLocalAliasesGlobal(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)

	project := filepath.Join(home, "dev", "myproject")

	cases := []struct {
		name string
		a    Agent
		base string
		want bool
	}{
		// claude global is ~/.claude/skills; at base == home its local
		// .claude/skills resolves to the very same folder.
		{"claude at home aliases", claudeAgent(), home, true},
		{"codex at home aliases", codexAgent(), home, true},
		// In a real project the two diverge — a genuine local scope.
		{"claude in project is real", claudeAgent(), project, false},
		// Global-only and local-only agents cannot alias (need both folders).
		{"global only", Agent{Name: "g", Global: "~/.g/skills"}, home, false},
		{"local only", Agent{Name: "l", Local: ".l/skills"}, home, false},
		// Divergent templates: even at home, global ~/.foo never equals local
		// ~/.bar — the rule is per-agent path equality, not "base == home".
		{"divergent templates at home", Agent{Name: "d", Global: "~/.foo/skills", Local: ".bar/skills"}, home, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := LocalAliasesGlobal(c.a, c.base); got != c.want {
				t.Errorf("LocalAliasesGlobal(%s, %q) = %v, want %v", c.a.Name, c.base, got, c.want)
			}
		})
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
	// When the home lookup fails, a Global SkillsFolder must still produce a
	// usable path rooted at "~" rather than panicking or returning empty.
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	got, ok := SkillsFolder(claudeAgent(), Global, "")
	if !ok || got == "" {
		t.Fatalf("SkillsFolder returned (%q, %v); want a non-empty path", got, ok)
	}
}
