package core

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ultrakorne/skillm/internal/agentdir"
	"github.com/ultrakorne/skillm/internal/config"
	"github.com/ultrakorne/skillm/internal/state"
	"github.com/ultrakorne/skillm/internal/store"
)

// listFixture sandboxes HOME (so Global agent folders land in a temp dir and
// never touch the real ~/.claude or ~/.agents), creates a Home holding the
// skill directory that hand-made skillm links point into, and returns the
// Home, that directory and the default agents (agents, claude — sorted).
func listFixture(t *testing.T, id string) (fakeHome, home, skillDir string, agents []agentdir.Agent) {
	t.Helper()
	fakeHome = t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("USERPROFILE", fakeHome)

	home = t.TempDir()
	if err := store.EnsureHome(home); err != nil {
		t.Fatalf("EnsureHome: %v", err)
	}
	skillDir = filepath.Join(home, "skills", id)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill: %v", err)
	}
	return fakeHome, home, skillDir, config.Default().AllAgents()
}

// linkAt hand-builds agent a's skillm link to skillDir at (scope, base).
func linkAt(t *testing.T, a agentdir.Agent, scope agentdir.Scope, base, id, skillDir string) {
	t.Helper()
	folder, _ := agentdir.SkillsFolder(a, scope, base)
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Skipf("cannot create folder %s: %v", folder, err)
	}
	if err := os.Symlink(skillDir, filepath.Join(folder, id)); err != nil {
		t.Fatalf("symlink: %v", err)
	}
}

// TestInstallsOfGlobalLink verifies the live link scan: a skill linked at
// global scope for one agent is listed there for that agent, and a skill
// linked nowhere has no installs.
func TestInstallsOfGlobalLink(t *testing.T) {
	const id = "demo"
	_, home, skillDir, agents := listFixture(t, id)

	if got := installsOf(home, state.SkillEntry{ID: id}, agents, nil, t.TempDir()); len(got) != 0 {
		t.Fatalf("unlinked skill: installs = %+v, want none", got)
	}

	a := agents[0]
	linkAt(t, a, agentdir.Global, "", id, skillDir)

	got := installsOf(home, state.SkillEntry{ID: id}, agents, nil, t.TempDir())
	want := []Install{{
		Scope:  ScopeGlobal,
		Path:   agentdir.CanonicalSkillDirAt(agentdir.Global, "", id),
		Agents: []string{a.Name},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("installs = %+v, want %+v", got, want)
	}
}

// TestInstallsOfLocalRoots verifies a local link in a tracked root that is
// NOT the current directory is found and listed with its root.
func TestInstallsOfLocalRoots(t *testing.T) {
	const id = "demo"
	_, home, skillDir, agents := listFixture(t, id)
	a := agents[0]

	proj := t.TempDir()
	linkAt(t, a, agentdir.Local, proj, id, skillDir)

	cwd := t.TempDir() // a different directory with no links of its own
	got := installsOf(home, state.SkillEntry{ID: id}, agents, []string{proj}, cwd)
	want := []Install{{
		Scope:  ScopeLocal,
		Root:   proj,
		Path:   agentdir.CanonicalSkillDirAt(agentdir.Local, proj, id),
		Agents: []string{a.Name},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("installs = %+v, want %+v", got, want)
	}
}

// TestInstallsOfHomeAliasesGlobal verifies the home-directory invariant: seen
// from cwd == HOME, a global link is listed as Global only — never also as
// Local, since each agent's local folder there is its global folder.
func TestInstallsOfHomeAliasesGlobal(t *testing.T) {
	const id = "demo"
	fakeHome, home, skillDir, agents := listFixture(t, id)
	linkAt(t, agents[0], agentdir.Global, "", id, skillDir)

	got := installsOf(home, state.SkillEntry{ID: id}, agents, nil, fakeHome)
	if len(got) != 1 || got[0].Scope != ScopeGlobal {
		t.Fatalf("installs from home = %+v, want only the Global one", got)
	}
}

// TestInstallsOfRecordedCopies verifies recorded installs: an existing Global
// copy serves the canonical agent and is marked Exists, and a recorded Local
// copy that vanished is still listed, serving nothing.
func TestInstallsOfRecordedCopies(t *testing.T) {
	const id = "demo"
	_, home, _, agents := listFixture(t, id)

	gcopy := agentdir.CanonicalSkillDirAt(agentdir.Global, "", id)
	if err := os.MkdirAll(gcopy, 0o755); err != nil {
		t.Fatalf("mkdir global copy: %v", err)
	}
	proj := t.TempDir()
	e := state.SkillEntry{ID: id, Global: true, VendoredAt: []string{proj}}

	got := installsOf(home, e, agents, nil, "")
	want := []Install{
		{Scope: ScopeGlobal, Path: gcopy, Agents: []string{"agents"}, Recorded: true, Exists: true},
		{Scope: ScopeLocal, Root: proj, Path: agentdir.CanonicalSkillDirAt(agentdir.Local, proj, id), Agents: []string{}, Recorded: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("installs = %+v, want %+v", got, want)
	}
}
