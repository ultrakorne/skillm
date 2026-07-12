package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ultrakorne/skillm/internal/agentdir"
	"github.com/ultrakorne/skillm/internal/config"
	"github.com/ultrakorne/skillm/internal/state"
	"github.com/ultrakorne/skillm/internal/store"
)

// These tests exercise migrateDeadAgentDirs in-process. The user home is
// pinned to a temp dir (so ~ expansion lands in the sandbox) and the
// package-level flags are set directly, restored afterwards because they are
// globals shared by every command in the package.

// migrateFixture pins an isolated user home plus skillm Home for one test and
// points the persistent flags at them. yes controls --yes, which the
// migration needs off a TTY to actually apply.
func migrateFixture(t *testing.T, yes bool) (home, userDir string) {
	t.Helper()
	home = t.TempDir()
	userDir = t.TempDir()
	t.Setenv("HOME", userDir)
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", userDir)
	}
	prevHome, prevYes, prevForce := flagHome, flagYes, flagForce
	flagHome, flagYes, flagForce = home, yes, false
	t.Cleanup(func() { flagHome, flagYes, flagForce = prevHome, prevYes, prevForce })
	return home, userDir
}

// writeOldCodexConfig seeds config.toml with the dead pre-migration codex
// paths, optionally carrying an explicit enabled flag.
func writeOldCodexConfig(t *testing.T, home string, enabled *bool) {
	t.Helper()
	cfg := &config.Config{Agents: map[string]config.AgentDef{
		"codex": {Enabled: enabled, Global: "~/.codex/skills", Local: ".codex/skills"},
	}}
	if err := config.Save(home, cfg); err != nil {
		t.Fatalf("seed old config: %v", err)
	}
}

// oldCodexAgent is the pre-migration definition, used to plant links the way
// an old skillm would have.
func oldCodexAgent() agentdir.Agent {
	return agentdir.Agent{Name: "codex", Global: "~/.codex/skills", Local: ".codex/skills"}
}

func TestMigrateMovesGlobalLinkAndRewritesConfig(t *testing.T) {
	home, userDir := migrateFixture(t, true)
	f := false
	writeOldCodexConfig(t, home, &f) // disabled: the flag must survive the rewrite
	makeSkill(t, home, "alpha")
	mustLink(t, home, "alpha", oldCodexAgent(), agentdir.Global, "")

	if err := migrateDeadAgentDirs(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	cfg, err := config.Load(home)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	// The seeded entry is renamed along with the path fix: the folder serves
	// every .agents-native agent, so the "codex" name is retired.
	if _, ok := cfg.Agents["codex"]; ok {
		t.Error("seeded \"codex\" entry should have been renamed to \"agents\"")
	}
	agents, ok := cfg.Agents["agents"]
	if !ok {
		t.Fatal("renamed \"agents\" entry missing after migration")
	}
	if agents.Global != "~/.agents/skills" || agents.Local != ".agents/skills" {
		t.Errorf("agents locations = %q, %q; want the .agents/skills pair", agents.Global, agents.Local)
	}
	if agents.IsEnabled() {
		t.Error("migration must preserve the enabled flag (was disabled)")
	}

	// The link moved: present under ~/.agents/skills, gone from ~/.codex/skills.
	assertResolves(t, home, filepath.Join(userDir, ".agents", "skills", "alpha"), "alpha")
	assertAbsent(t, filepath.Join(userDir, ".codex", "skills", "alpha"))

	// The emptied old folder was cleaned up.
	if _, err := os.Lstat(filepath.Join(userDir, ".codex", "skills")); !os.IsNotExist(err) {
		t.Errorf("old global folder should be removed once empty; lstat err = %v", err)
	}
}

func TestMigrateLeavesLocalRootLinksWithNotice(t *testing.T) {
	// Local installs are committed copies now, so the migration cannot relocate
	// an old Home-pointing local link — it leaves it untouched and points the
	// user at `skillm install --local` instead.
	home, _ := migrateFixture(t, true)
	writeOldCodexConfig(t, home, nil)
	proj := t.TempDir()
	makeSkill(t, home, "alpha")
	// Hand-build the legacy shape: an absolute symlink into Home under the dead
	// .codex/skills folder.
	oldLink := filepath.Join(proj, ".codex", "skills", "alpha")
	if err := os.MkdirAll(filepath.Dir(oldLink), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(store.SkillDir(home, "alpha"), oldLink); err != nil {
		t.Fatal(err)
	}
	if err := state.Save(home, &state.State{LocalRoots: []string{proj}}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	if err := migrateDeadAgentDirs(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// The legacy link stays where it was (no half-migrated state, nothing
	// deleted), and no dangling link was created in .agents/skills.
	assertResolves(t, home, oldLink, "alpha")
	assertAbsent(t, filepath.Join(proj, ".agents", "skills", "alpha"))
}

func TestMigrateMovesVendoredCopy(t *testing.T) {
	home, _ := migrateFixture(t, true)
	writeOldCodexConfig(t, home, nil)
	root := t.TempDir()
	makeSkill(t, home, "alpha")
	st := &state.State{Skills: []state.SkillEntry{
		{ID: "alpha", Kind: state.KindLocal, Source: "/src/alpha", VendoredAt: []string{root}},
	}}
	if err := state.Save(home, st); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	oldCopy := filepath.Join(root, ".codex", "skills", "alpha")
	if err := os.MkdirAll(oldCopy, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldCopy, "SKILL.md"), []byte("vendored body"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := migrateDeadAgentDirs(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	newCopy := filepath.Join(root, ".agents", "skills", "alpha")
	fi, err := os.Lstat(newCopy)
	if err != nil {
		t.Fatalf("vendored copy not moved: %v", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
		t.Fatalf("moved copy must stay a real directory; mode = %v", fi.Mode())
	}
	if _, err := os.Stat(filepath.Join(newCopy, "SKILL.md")); err != nil {
		t.Fatalf("copy content lost in move: %v", err)
	}
	assertAbsent(t, oldCopy)
}

func TestMigrateLeavesCustomizedPathsAlone(t *testing.T) {
	// flagYes stays false: a hand-customized agent must not even reach the
	// consent step — no match means an immediate, silent no-op.
	home, _ := migrateFixture(t, false)
	cfg := &config.Config{Agents: map[string]config.AgentDef{
		"codex": {Global: "~/.mycodex/skills", Local: ".codex/skills"},
	}}
	if err := config.Save(home, cfg); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(config.Path(home))
	if err != nil {
		t.Fatal(err)
	}

	if err := migrateDeadAgentDirs(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	after, err := os.ReadFile(config.Path(home))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("customized config was rewritten:\nbefore: %s\nafter: %s", before, after)
	}
}

func TestMigrateAbsentConfigIsNoop(t *testing.T) {
	home, _ := migrateFixture(t, true)

	if err := migrateDeadAgentDirs(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// A fresh install must not gain a config file from the migration pass.
	if _, err := os.Stat(config.Path(home)); !os.IsNotExist(err) {
		t.Errorf("migration created config.toml on a fresh install; stat err = %v", err)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	home, userDir := migrateFixture(t, true)
	writeOldCodexConfig(t, home, nil)
	makeSkill(t, home, "alpha")
	mustLink(t, home, "alpha", oldCodexAgent(), agentdir.Global, "")

	if err := migrateDeadAgentDirs(); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	afterFirst, err := os.ReadFile(config.Path(home))
	if err != nil {
		t.Fatal(err)
	}

	if err := migrateDeadAgentDirs(); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	afterSecond, err := os.ReadFile(config.Path(home))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(afterFirst, afterSecond) {
		t.Errorf("second run rewrote the config:\nfirst: %s\nsecond: %s", afterFirst, afterSecond)
	}
	// The relocated link is still in place and was not disturbed.
	assertResolves(t, home, filepath.Join(userDir, ".agents", "skills", "alpha"), "alpha")
}

func TestMigrateNonTTYWithoutYesWarnsAndProceeds(t *testing.T) {
	// Off a TTY and without --yes/--force the migration must not touch
	// anything (and must not block on a prompt) — it only hints once.
	home, userDir := migrateFixture(t, false)
	writeOldCodexConfig(t, home, nil)
	makeSkill(t, home, "alpha")
	mustLink(t, home, "alpha", oldCodexAgent(), agentdir.Global, "")
	before, err := os.ReadFile(config.Path(home))
	if err != nil {
		t.Fatal(err)
	}

	if err := migrateDeadAgentDirs(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	after, err := os.ReadFile(config.Path(home))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("non-TTY migration without --yes must not rewrite config")
	}
	assertResolves(t, home, filepath.Join(userDir, ".codex", "skills", "alpha"), "alpha")
	assertAbsent(t, filepath.Join(userDir, ".agents", "skills", "alpha"))
}

func TestMigrateRenamesCodexWithNewPair(t *testing.T) {
	// A config seeded in the window where the entry already carried the
	// .agents/skills pair but was still named "codex": the rename is config
	// only — the locations are identical, so links must not be touched.
	home, userDir := migrateFixture(t, true)
	f := false
	cfg := &config.Config{Agents: map[string]config.AgentDef{
		"codex": {Enabled: &f, Global: "~/.agents/skills", Local: ".agents/skills"},
	}}
	if err := config.Save(home, cfg); err != nil {
		t.Fatal(err)
	}
	makeSkill(t, home, "alpha")
	newPair := agentdir.Agent{Name: "codex", Global: "~/.agents/skills", Local: ".agents/skills"}
	mustLink(t, home, "alpha", newPair, agentdir.Global, "")

	if err := migrateDeadAgentDirs(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	got, err := config.Load(home)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	if _, ok := got.Agents["codex"]; ok {
		t.Error("\"codex\" entry with the .agents pair should have been renamed")
	}
	agents, ok := got.Agents["agents"]
	if !ok {
		t.Fatal("renamed \"agents\" entry missing")
	}
	if agents.Global != "~/.agents/skills" || agents.Local != ".agents/skills" {
		t.Errorf("rename must not touch the locations; got %q, %q", agents.Global, agents.Local)
	}
	if agents.IsEnabled() {
		t.Error("rename must preserve the enabled flag (was disabled)")
	}
	// The link already lives in the right folder and stays exactly there.
	assertResolves(t, home, filepath.Join(userDir, ".agents", "skills", "alpha"), "alpha")
}

func TestMigrateRenameBlockedByExistingAgentsEntry(t *testing.T) {
	// A user-defined "agents" entry blocks the rename: the "codex" entry keeps
	// its name (paths still fixed) and neither definition is lost.
	home, userDir := migrateFixture(t, true)
	cfg := &config.Config{Agents: map[string]config.AgentDef{
		"codex":  {Global: "~/.codex/skills", Local: ".codex/skills"},
		"agents": {Global: "~/.custom/skills", Local: ".custom/skills"},
	}}
	if err := config.Save(home, cfg); err != nil {
		t.Fatal(err)
	}
	makeSkill(t, home, "alpha")
	mustLink(t, home, "alpha", oldCodexAgent(), agentdir.Global, "")

	if err := migrateDeadAgentDirs(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	got, err := config.Load(home)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	codex, ok := got.Agents["codex"]
	if !ok {
		t.Fatal("\"codex\" entry must survive a blocked rename")
	}
	if codex.Global != "~/.agents/skills" || codex.Local != ".agents/skills" {
		t.Errorf("blocked rename must still fix the dead paths; got %q, %q", codex.Global, codex.Local)
	}
	agents, ok := got.Agents["agents"]
	if !ok {
		t.Fatal("user-defined \"agents\" entry was lost")
	}
	if agents.Global != "~/.custom/skills" || agents.Local != ".custom/skills" {
		t.Errorf("user-defined \"agents\" entry was rewritten; got %q, %q", agents.Global, agents.Local)
	}
	// The link relocation is unaffected by the blocked rename.
	assertResolves(t, home, filepath.Join(userDir, ".agents", "skills", "alpha"), "alpha")
	assertAbsent(t, filepath.Join(userDir, ".codex", "skills", "alpha"))
}

func TestMigrateKeepsNameOfOtherDeadPairAgent(t *testing.T) {
	// Only the exact seeded key "codex" is renamed; a dead-pair agent under
	// any other name gets its paths fixed but keeps its identity.
	home, _ := migrateFixture(t, true)
	cfg := &config.Config{Agents: map[string]config.AgentDef{
		"mycodex": {Global: "~/.codex/skills", Local: ".codex/skills"},
	}}
	if err := config.Save(home, cfg); err != nil {
		t.Fatal(err)
	}

	if err := migrateDeadAgentDirs(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	got, err := config.Load(home)
	if err != nil {
		t.Fatalf("reload config: %v", err)
	}
	my, ok := got.Agents["mycodex"]
	if !ok {
		t.Fatal("\"mycodex\" entry lost its name")
	}
	if my.Global != "~/.agents/skills" || my.Local != ".agents/skills" {
		t.Errorf("mycodex paths not fixed; got %q, %q", my.Global, my.Local)
	}
	if _, ok := got.Agents["agents"]; ok {
		t.Error("no \"agents\" entry should appear when the seeded key is absent")
	}
}
