package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ultrakorne/skillm/internal/agentdir"
	"github.com/ultrakorne/skillm/internal/config"
	"github.com/ultrakorne/skillm/internal/linker"
	"github.com/ultrakorne/skillm/internal/state"
)

// agentsHome writes a config.toml defining claude (enabled) and codex
// (disabled) with folders under the sandboxed home, and returns Home and the
// two agents.
func agentsHome(t *testing.T) (home string, claude, codex agentdir.Agent) {
	t.Helper()
	globalRoot := sandboxGlobalRoot(t)
	home = t.TempDir()
	claude, codex = testAgents(globalRoot)
	on, off := true, false
	cfg := &config.Config{Agents: map[string]config.AgentDef{
		"claude": {Enabled: &on, Global: claude.Global, Local: claude.Local},
		"codex":  {Enabled: &off, Global: codex.Global, Local: codex.Local},
	}}
	if err := config.Save(home, cfg); err != nil {
		t.Fatal(err)
	}
	return home, claude, codex
}

// enabledNames reads the enabled agents back from config.toml.
func enabledNames(t *testing.T, home string) string {
	t.Helper()
	cfg, err := config.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(cfg.EnabledNames(), ",")
}

// TestAgentsListsDefinedAgents: Agents returns every defined agent, sorted,
// with its enabled state and locations.
func TestAgentsListsDefinedAgents(t *testing.T) {
	home, claude, _ := agentsHome(t)
	got, err := Agents(Options{Home: home})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "claude" || !got[0].Enabled || got[1].Name != "codex" || got[1].Enabled {
		t.Fatalf("Agents = %+v, want claude (enabled), codex (disabled)", got)
	}
	if got[0].Global != claude.Global || got[0].Local != claude.Local {
		t.Fatalf("claude locations = %q, %q; want %q, %q", got[0].Global, got[0].Local, claude.Global, claude.Local)
	}
}

// TestSetAgentsSwap: one call enabling codex and disabling claude saves the
// new flags, lets codex inherit claude's links, removes claude's, and reports
// each change as an event and in the result.
func TestSetAgentsSwap(t *testing.T) {
	home, claude, codex := agentsHome(t)
	cwd := t.TempDir()
	mustLink(t, home, "alpha", claude, agentdir.Global, cwd)
	st := &state.State{}
	st.Upsert(state.SkillEntry{ID: "alpha", Kind: state.KindLocal, Source: t.TempDir(), Global: true})
	if err := state.Save(home, st); err != nil {
		t.Fatal(err)
	}

	rep := &recorder{}
	res, err := SetAgents(context.Background(), Options{Home: home, Cwd: cwd}, rep, []string{"codex"}, []string{"claude"})
	if err != nil {
		t.Fatalf("SetAgents: %v", err)
	}
	if got := enabledNames(t, home); got != "codex" {
		t.Fatalf("enabled in config = %q, want codex", got)
	}
	if strings.Join(res.Enabled, ",") != "codex" || len(res.Changes) != 2 {
		t.Fatalf("result = %+v, want codex enabled and two changes", res)
	}
	if c := res.Changes[0]; c.Name != "codex" || !c.Enabled || strings.Join(c.Skills, ",") != "alpha" || strings.Join(c.Places, ",") != "global" {
		t.Fatalf("enable change = %+v", c)
	}
	if c := res.Changes[1]; c.Name != "claude" || c.Enabled || strings.Join(c.Skills, ",") != "alpha" {
		t.Fatalf("disable change = %+v", c)
	}
	assertResolvesGlobal(t, linkPath(t, codex, agentdir.Global, cwd, "alpha"), "alpha")
	assertAbsent(t, linkPath(t, claude, agentdir.Global, cwd, "alpha"))

	en := eventsWith(rep, CodeAgentEnabled)
	dis := eventsWith(rep, CodeAgentDisabled)
	if len(en) != 1 || en[0].Text != "enabled codex — installed 1 skill (global)" {
		t.Fatalf("agent_enabled events = %+v", en)
	}
	if len(dis) != 1 || dis[0].Text != "disabled claude — removed 1 skill (global)" {
		t.Fatalf("agent_disabled events = %+v", dis)
	}
}

// TestSetAgentsNothingToLink: enabling an agent while nothing is installed
// reports agent_enabled_empty, whose text names no command (the CLI adds it).
func TestSetAgentsNothingToLink(t *testing.T) {
	home, _, _ := agentsHome(t)
	rep := &recorder{}
	if _, err := SetAgents(context.Background(), Options{Home: home, Cwd: t.TempDir()}, rep, []string{"codex"}, nil); err != nil {
		t.Fatal(err)
	}
	ev := eventsWith(rep, CodeAgentEnabledEmpty)
	if len(ev) != 1 || ev[0].Text != "enabled codex — nothing to install yet" {
		t.Fatalf("agent_enabled_empty events = %+v", ev)
	}
	if got := enabledNames(t, home); got != "claude,codex" {
		t.Fatalf("enabled in config = %q, want claude,codex", got)
	}
}

// TestSetAgentsRefusals: an unknown name, a name in both lists, and a change
// that leaves no agent enabled are each refused before anything is written;
// a change that changes nothing writes nothing either.
func TestSetAgentsRefusals(t *testing.T) {
	home, _, _ := agentsHome(t)
	cfgPath := config.Path(home)
	before, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	opts := Options{Home: home, Cwd: t.TempDir()}
	ctx := context.Background()

	var unknown *UnknownAgentError
	if _, err := SetAgents(ctx, opts, nil, []string{"nope"}, nil); !errors.As(err, &unknown) || unknown.Names[0] != "nope" {
		t.Fatalf("unknown agent: err = %v, want *UnknownAgentError naming nope", err)
	}
	if _, err := SetAgents(ctx, opts, nil, []string{"codex"}, []string{"codex"}); err == nil {
		t.Fatal("enabling and disabling the same agent should fail")
	}
	if _, err := SetAgents(ctx, opts, nil, nil, []string{"claude"}); !errors.Is(err, ErrNoAgentEnabled) {
		t.Fatalf("disabling the last agent: err = %v, want ErrNoAgentEnabled", err)
	}
	res, err := SetAgents(ctx, opts, nil, []string{"claude"}, []string{"codex"})
	if err != nil || len(res.Changes) != 0 || strings.Join(res.Enabled, ",") != "claude" {
		t.Fatalf("no-op change: res = %+v, err = %v; want no changes, claude enabled", res, err)
	}

	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("config.toml changed:\n%s\nwant:\n%s", after, before)
	}
	if _, err := os.Stat(state.Path(home)); !os.IsNotExist(err) {
		t.Fatalf("state.toml was written (stat err = %v)", err)
	}
}

// uninstallFixture registers skill alpha with a Global copy linked for
// claude, and a tracked project whose claude folder holds a real directory
// named alpha that skillm did not create. It returns Home, cwd and that
// directory.
func uninstallFixture(t *testing.T) (home, cwd, foreign string) {
	t.Helper()
	home, claude, _ := agentsHome(t)
	cwd = t.TempDir()
	proj := t.TempDir()
	mustLink(t, home, "alpha", claude, agentdir.Global, cwd)
	foreign = linkPath(t, claude, agentdir.Local, proj, "alpha")
	if err := os.MkdirAll(foreign, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(foreign, "mine.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := &state.State{LocalRoots: []string{proj}}
	st.Upsert(state.SkillEntry{ID: "alpha", Kind: state.KindLocal, Source: t.TempDir(), Global: true})
	if err := state.Save(home, st); err != nil {
		t.Fatal(err)
	}
	return home, cwd, foreign
}

// TestUninstallBlockedNeedsForce: a foreign entry at a link path stops the
// uninstall with an error that matches ErrNeedsConfirm (and the refusal),
// keeping the Registry entry; with Force it is a warning, the entry stays on
// disk, and the skill is uninstalled.
func TestUninstallBlockedNeedsForce(t *testing.T) {
	home, cwd, foreign := uninstallFixture(t)
	ctx := context.Background()

	_, err := Uninstall(ctx, Options{Home: home, Cwd: cwd}, nil, []string{"alpha"})
	var blocked *UninstallBlockedError
	if !errors.As(err, &blocked) || blocked.ID != "alpha" || !errors.Is(err, ErrNeedsConfirm) || !errors.Is(err, linker.ErrNotManaged) {
		t.Fatalf("err = %v, want an *UninstallBlockedError for alpha matching ErrNeedsConfirm and ErrNotManaged", err)
	}
	if !strings.HasPrefix(err.Error(), "refusing to remove "+foreign) {
		t.Fatalf("err text = %q, want the linker's refusal", err.Error())
	}
	st, _ := state.Load(home)
	if _, ok := st.Get("alpha"); !ok {
		t.Fatal("a blocked uninstall must keep the Registry entry")
	}

	rep := &recorder{}
	res, err := Uninstall(ctx, Options{Home: home, Cwd: cwd, Force: true}, rep, []string{"alpha"})
	if err != nil {
		t.Fatalf("forced Uninstall: %v", err)
	}
	if len(res.Skills) != 1 || len(res.Skills[0].Warnings) != 1 {
		t.Fatalf("result = %+v, want alpha with one warning", res)
	}
	if ev := eventsWith(rep, CodeUnlinkRefused); len(ev) != 1 {
		t.Fatalf("unlink_refused events = %+v, want one", ev)
	}
	if ev := eventsWith(rep, CodeUninstalled); len(ev) != 1 || ev[0].Text != "uninstalled alpha" {
		t.Fatalf("uninstalled events = %+v", ev)
	}
	if _, err := os.Stat(filepath.Join(foreign, "mine.txt")); err != nil {
		t.Fatalf("the foreign directory was touched: %v", err)
	}
	if CopyExists(home, "alpha", agentdir.Global, "") {
		t.Fatal("the Global copy was not removed")
	}
	st, _ = state.Load(home)
	if _, ok := st.Get("alpha"); ok {
		t.Fatal("the Registry entry was not dropped")
	}
}

// TestUninstallNotInstalled: an id not in the Registry fails the whole call
// before anything is removed.
func TestUninstallNotInstalled(t *testing.T) {
	home, cwd, _ := uninstallFixture(t)
	_, err := Uninstall(context.Background(), Options{Home: home, Cwd: cwd, Force: true}, nil, []string{"alpha", "nope"})
	var missing *NotInstalledError
	if !errors.As(err, &missing) || strings.Join(missing.IDs, ",") != "nope" {
		t.Fatalf("err = %v, want *NotInstalledError naming nope", err)
	}
	if err.Error() != "not installed: nope; nothing to uninstall" {
		t.Fatalf("err text = %q", err.Error())
	}
	if !CopyExists(home, "alpha", agentdir.Global, "") {
		t.Fatal("alpha's copy was removed although the call failed")
	}
}
