package cmd

import (
	"path/filepath"
	"testing"

	"github.com/ultrakorne/skillm/internal/agentdir"
	"github.com/ultrakorne/skillm/internal/config"
	"github.com/ultrakorne/skillm/internal/state"
)

// TestRefreshVendoredCopiesDropsEntryWhenLastInstallPruned verifies the model
// invariant "an entry exists only while installed somewhere": when update finds
// a recorded install whose canonical copy has vanished, it prunes the install —
// and if that was the skill's last one, the registry entry is dropped too.
func TestRefreshVendoredCopiesDropsEntryWhenLastInstallPruned(t *testing.T) {
	home := t.TempDir()
	// A git skill recorded as installed at a project root whose copy does NOT
	// exist (the project was moved or the files were deleted).
	gone := filepath.Join(t.TempDir(), "gone")
	st := &state.State{Skills: []state.SkillEntry{{
		ID: "alpha", Kind: state.KindGit, Source: "u", Path: "alpha", Ref: "main", Revision: "r",
		VendoredAt: []string{gone},
	}}}
	agents := config.Default().AllAgents()

	changed := refreshVendoredCopies(home, agents, st, []string{"alpha"}, map[string]bool{}, map[string]string{})
	if !changed {
		t.Fatal("expected a change (vanished install pruned, entry dropped)")
	}
	if _, ok := st.Get("alpha"); ok {
		t.Fatal("entry must be dropped once its last install is pruned")
	}
}

// TestRefreshVendoredCopiesKeepsEntryWithRemainingInstall verifies the converse:
// a skill with a still-present global install is NOT dropped when one of its
// project installs is pruned.
func TestRefreshVendoredCopiesKeepsEntryWithRemainingInstall(t *testing.T) {
	home := t.TempDir()
	sandboxGlobalRoot(t) // so the global canonical copy lands in the sandbox
	gone := filepath.Join(t.TempDir(), "gone")
	st := &state.State{Skills: []state.SkillEntry{{
		ID: "alpha", Kind: state.KindGit, Source: "u", Path: "alpha", Ref: "main", Revision: "r",
		Global: true, VendoredAt: []string{gone},
	}}}
	agents := config.Default().AllAgents()

	// Materialize the global canonical copy so the global install survives while
	// the vanished project install is pruned.
	makeCanonicalCopy(t, agentdir.Global, "", "alpha")

	refreshVendoredCopies(home, agents, st, []string{"alpha"}, map[string]bool{}, map[string]string{})
	e, ok := st.Get("alpha")
	if !ok {
		t.Fatal("entry must survive while the global install remains")
	}
	if len(e.VendoredAt) != 0 {
		t.Fatalf("the vanished project install should be pruned; VendoredAt = %v", e.VendoredAt)
	}
	if !e.Global {
		t.Fatal("the intact global install must stay recorded")
	}
}
