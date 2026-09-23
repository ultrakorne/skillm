package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ultrakorne/skillm/internal/agentdir"
	"github.com/ultrakorne/skillm/internal/config"
	"github.com/ultrakorne/skillm/internal/state"
	"github.com/ultrakorne/skillm/internal/store"
)

// TestReconcileLocalRootsPrunesHomeAlias verifies a legacy tracked root that is
// the home directory — whose "local" links are really the global ones — is
// pruned, while a genuine project root is kept.
func TestReconcileLocalRootsPrunesHomeAlias(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("USERPROFILE", fakeHome)

	home := t.TempDir()
	if err := store.EnsureHome(home); err != nil {
		t.Fatalf("EnsureHome: %v", err)
	}
	const id = "demo"
	skillDir := filepath.Join(home, "skills", id)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill: %v", err)
	}

	agents := config.Default().AllAgents()
	a := agents[0]

	// A global link under HOME's global skill folder (what a bogus HOME root
	// would otherwise "find" as local).
	gfolder, _ := agentdir.SkillsFolder(a, agentdir.Global, "")
	if err := os.MkdirAll(gfolder, 0o755); err != nil {
		t.Skipf("cannot create global folder %s: %v", gfolder, err)
	}
	if err := os.Symlink(skillDir, filepath.Join(gfolder, id)); err != nil {
		t.Fatalf("global symlink: %v", err)
	}

	// A genuine local link in a real project that must survive reconciliation.
	proj := t.TempDir()
	lfolder, _ := agentdir.SkillsFolder(a, agentdir.Local, proj)
	if err := os.MkdirAll(lfolder, 0o755); err != nil {
		t.Fatalf("mkdir local folder: %v", err)
	}
	if err := os.Symlink(skillDir, filepath.Join(lfolder, id)); err != nil {
		t.Fatalf("local symlink: %v", err)
	}

	st := &state.State{LocalRoots: []string{fakeHome, proj}}
	if !reconcileLocalRoots(home, agents, st) {
		t.Fatal("reconcileLocalRoots reported no change; expected HOME to be pruned")
	}
	if len(st.LocalRoots) != 1 || st.LocalRoots[0] != proj {
		t.Fatalf("LocalRoots = %v, want only the real project %s", st.LocalRoots, proj)
	}
}
