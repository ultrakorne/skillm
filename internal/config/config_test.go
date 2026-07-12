package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDefault(t *testing.T) {
	c := Default()
	if len(c.Agents) != 2 {
		t.Fatalf("Default() has %d agents, want 2", len(c.Agents))
	}
	claude, ok := c.Agents["claude"]
	if !ok {
		t.Fatal("Default() missing claude")
	}
	if !claude.IsEnabled() {
		t.Error("claude should be enabled by default")
	}
	if claude.Global != "~/.claude/skills" || claude.Local != ".claude/skills" {
		t.Errorf("claude locations = %q, %q", claude.Global, claude.Local)
	}
	agents, ok := c.Agents["agents"]
	if !ok {
		t.Fatal("Default() missing agents")
	}
	// The "agents" entry uses the cross-agent .agents/skills convention (read
	// by Codex, Cursor, Amp, Gemini CLI, …) and is named for the folder, not
	// any single tool.
	if agents.Global != "~/.agents/skills" || agents.Local != ".agents/skills" {
		t.Errorf("agents locations = %q, %q", agents.Global, agents.Local)
	}
}

func TestIsEnabledDefaultsTrue(t *testing.T) {
	if !(AgentDef{}).IsEnabled() {
		t.Error("AgentDef with nil Enabled should default to enabled")
	}
	f := false
	if (AgentDef{Enabled: &f}).IsEnabled() {
		t.Error("AgentDef with Enabled=false should be disabled")
	}
}

func TestLoadAbsentReturnsDefault(t *testing.T) {
	home := t.TempDir()

	c, err := Load(home)
	if err != nil {
		t.Fatalf("Load on empty home: %v", err)
	}
	if !reflect.DeepEqual(c, Default()) {
		t.Errorf("Load absent = %+v, want Default() %+v", c, Default())
	}

	// Load must not write the file (config is user-owned).
	if _, err := os.Stat(Path(home)); !os.IsNotExist(err) {
		t.Errorf("Load created %s; it must not write when absent", Path(home))
	}
}

// TestLoadIsAuthoritative verifies that a present config file is parsed on its
// own and not merged onto the built-in defaults: an agent omitted from the file
// is genuinely absent, so a user can drop claude entirely.
func TestLoadIsAuthoritative(t *testing.T) {
	home := t.TempDir()
	body := "[agents.opencode]\nenabled = true\nglobal = \"~/.config/opencode/skill\"\nlocal = \".opencode/skill\"\n"
	if err := os.WriteFile(Path(home), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	c, err := Load(home)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(c.Agents) != 1 {
		t.Fatalf("Load merged defaults: %d agents, want 1 (opencode only)", len(c.Agents))
	}
	if _, ok := c.Agents["claude"]; ok {
		t.Error("claude leaked in from defaults; a present file must be authoritative")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	home := t.TempDir()

	f := false
	want := &Config{Agents: map[string]AgentDef{
		"claude":   {Enabled: boolPtr(true), Global: "~/.claude/skills", Local: ".claude/skills"},
		"opencode": {Enabled: &f, Global: "~/.config/opencode/skill"}, // global-only, disabled
	}}
	if err := Save(home, want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load(home)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round-trip mismatch:\n got = %+v\nwant = %+v", got, want)
	}
}

func TestEnsureExists(t *testing.T) {
	home := t.TempDir()

	// Absent -> writes the default config.
	if err := EnsureExists(home); err != nil {
		t.Fatalf("EnsureExists (absent): %v", err)
	}
	if _, err := os.Stat(Path(home)); err != nil {
		t.Fatalf("EnsureExists did not write config: %v", err)
	}

	// Present -> never clobbers; a sentinel file must survive untouched.
	body := "[agents.only]\nglobal = \"~/.only/skills\"\n"
	if err := os.WriteFile(Path(home), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureExists(home); err != nil {
		t.Fatalf("EnsureExists (present): %v", err)
	}
	got, err := os.ReadFile(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Errorf("EnsureExists clobbered an existing file:\n got = %q\nwant = %q", string(got), body)
	}
}

func TestSaveCreatesHome(t *testing.T) {
	home := filepath.Join(t.TempDir(), "nested", "skillm")

	if err := Save(home, Default()); err != nil {
		t.Fatalf("Save into nonexistent home: %v", err)
	}
	if _, err := os.Stat(Path(home)); err != nil {
		t.Errorf("config file not written: %v", err)
	}
}

func TestSaveNilErrors(t *testing.T) {
	if err := Save(t.TempDir(), nil); err == nil {
		t.Error("Save(nil) = nil error, want error")
	}
}

func TestEnabledAgentsFiltersAndSorts(t *testing.T) {
	f := false
	c := &Config{Agents: map[string]AgentDef{
		"zeta":  {Global: "~/.z/skills"},               // enabled (nil flag)
		"alpha": {Enabled: boolPtr(true), Local: ".a"}, // enabled
		"beta":  {Enabled: &f, Global: "~/.b/skills"},  // disabled
	}}

	enabled := c.EnabledAgents()
	if len(enabled) != 2 {
		t.Fatalf("EnabledAgents len = %d, want 2", len(enabled))
	}
	if enabled[0].Name != "alpha" || enabled[1].Name != "zeta" {
		t.Errorf("EnabledAgents = %q, %q; want alpha, zeta (sorted, beta excluded)", enabled[0].Name, enabled[1].Name)
	}
	if got := c.EnabledNames(); !reflect.DeepEqual(got, []string{"alpha", "zeta"}) {
		t.Errorf("EnabledNames = %v, want [alpha zeta]", got)
	}
	if all := c.AllAgents(); len(all) != 3 {
		t.Fatalf("AllAgents len = %d, want 3", len(all))
	}
}

func TestSetEnabled(t *testing.T) {
	c := Default()
	c.SetEnabled([]string{"agents"})

	if c.Agents["claude"].IsEnabled() {
		t.Error("claude should be disabled after SetEnabled([agents])")
	}
	if !c.Agents["agents"].IsEnabled() {
		t.Error("agents should be enabled after SetEnabled([agents])")
	}
	// Toggling must preserve each agent's locations.
	if c.Agents["claude"].Global != "~/.claude/skills" {
		t.Errorf("claude location lost on toggle: %q", c.Agents["claude"].Global)
	}
}
