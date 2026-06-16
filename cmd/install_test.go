package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ultrakorne/skillm/internal/agentdir"
	"github.com/ultrakorne/skillm/internal/config"
	"github.com/ultrakorne/skillm/internal/store"
)

// TestInstalledMark verifies the annotation the interactive install picker shows
// for each skill: a skill installed globally or locally in the current directory
// is marked, while one linked only in some OTHER project directory is treated as
// not installed (so the mark reflects what installing from here would change).
func TestInstalledMark(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome) // sandbox the agents' global skill folders

	home := t.TempDir()
	if err := store.EnsureHome(home); err != nil {
		t.Fatalf("EnsureHome: %v", err)
	}
	const id = "demo"
	skillDir := store.SkillDir(home, id)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill: %v", err)
	}

	agents := config.Default().AllAgents() // claude, codex
	a := agents[0]
	cwd := t.TempDir()

	// Nothing linked yet.
	if got := installedMark(home, id, agents, cwd); got != "" {
		t.Fatalf("unlinked mark = %q, want empty", got)
	}

	// A local link in a DIFFERENT directory must NOT count as installed.
	linkInto(t, a, agentdir.Local, t.TempDir(), skillDir, id)
	if got := installedMark(home, id, agents, cwd); got != "" {
		t.Fatalf("local link elsewhere counted as installed: mark = %q, want empty", got)
	}

	// A local link in cwd counts as installed (local).
	linkInto(t, a, agentdir.Local, cwd, skillDir, id)
	if got := installedMark(home, id, agents, cwd); got != " (installed: local)" {
		t.Fatalf("local-in-cwd mark = %q, want %q", got, " (installed: local)")
	}

	// Adding a global link makes it both, in scope order.
	linkInto(t, a, agentdir.Global, "", skillDir, id)
	if got := installedMark(home, id, agents, cwd); got != " (installed: global, local)" {
		t.Fatalf("both mark = %q, want %q", got, " (installed: global, local)")
	}
}

// TestInstalledMarkHomeAliasesGlobal verifies the home-directory invariant for
// the install picker: when scanned from cwd == HOME, a purely *global* link must
// be marked "installed: global" only. Each agent's local folder there is its
// global folder, so without the alias filter the same link would also be
// reported as local ("installed: global, local").
func TestInstalledMarkHomeAliasesGlobal(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("USERPROFILE", fakeHome) // Windows resolves home via USERPROFILE

	home := t.TempDir()
	if err := store.EnsureHome(home); err != nil {
		t.Fatalf("EnsureHome: %v", err)
	}
	const id = "demo"
	skillDir := store.SkillDir(home, id)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill: %v", err)
	}

	agents := config.Default().AllAgents() // claude, codex
	a := agents[0]

	// A single global link, scanned with cwd == HOME.
	linkInto(t, a, agentdir.Global, "", skillDir, id)
	if got := installedMark(home, id, agents, fakeHome); got != " (installed: global)" {
		t.Fatalf("mark from home = %q, want %q", got, " (installed: global)")
	}
}

// linkInto creates a skillm-style symlink (folder/<id> -> target) for agent a at
// the given scope and base directory, creating the folder as needed.
func linkInto(t *testing.T, a agentdir.Agent, scope agentdir.Scope, base, target, id string) {
	t.Helper()
	folder, ok := agentdir.SkillsFolder(a, scope, base)
	if !ok {
		t.Fatalf("agent %s has no %s folder", a.Name, scope)
	}
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", folder, err)
	}
	if err := os.Symlink(target, filepath.Join(folder, id)); err != nil {
		t.Fatalf("symlink: %v", err)
	}
}
