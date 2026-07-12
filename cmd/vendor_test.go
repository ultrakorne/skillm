package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ultrakorne/skillm/internal/agentdir"
	"github.com/ultrakorne/skillm/internal/config"
	"github.com/ultrakorne/skillm/internal/store"
)

// vendorTestSetup builds a temp Home holding skill "demo" and returns the home,
// the enabled agents (claude+agents), and a fresh project base distinct from HOME
// (so each agent's local folder is real, not aliased to its global one).
func vendorTestSetup(t *testing.T) (home, base string, agents []agentdir.Agent) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())

	home = t.TempDir()
	if err := store.EnsureHome(home); err != nil {
		t.Fatalf("EnsureHome: %v", err)
	}
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("demo body\n"), 0o644); err != nil {
		t.Fatalf("write src SKILL.md: %v", err)
	}
	if err := store.AddSkillDir(home, "demo", src); err != nil {
		t.Fatalf("AddSkillDir: %v", err)
	}
	return home, t.TempDir(), config.Default().AllAgents()
}

func claudeSlot(base string) string { return filepath.Join(base, ".claude", "skills", "demo") }
func agentsSlot(base string) string { return filepath.Join(base, ".agents", "skills", "demo") }

// TestVendorApplyWritesAbsent: an absent target gets a fresh copy for each agent.
func TestVendorApplyWritesAbsent(t *testing.T) {
	home, base, agents := vendorTestSetup(t)

	outcomes, err := vendorApply(home, "demo", agents, base, false, false)
	if err != nil {
		t.Fatalf("vendorApply: %v", err)
	}
	if len(outcomes) != 2 {
		t.Fatalf("got %d outcomes, want 2 (claude, agents)", len(outcomes))
	}
	for _, o := range outcomes {
		if o.Action != vendorWrote {
			t.Errorf("%s action = %v, want vendorWrote", o.Agent.Name, o.Action)
		}
	}
	b, err := os.ReadFile(filepath.Join(claudeSlot(base), "SKILL.md"))
	if err != nil || string(b) != "demo body\n" {
		t.Fatalf("vendored copy content = %q err=%v, want %q", b, err, "demo body\n")
	}
	// And vendorScan now sees both agents' copies.
	if have := vendorScan(home, "demo", agents, base); len(have) != 2 {
		t.Fatalf("vendorScan after write = %d agents, want 2", len(have))
	}
}

// TestVendorApplyConvertsOwnSymlink: skillm's own symlink at the target is
// replaced by a copy without needing --force.
func TestVendorApplyConvertsOwnSymlink(t *testing.T) {
	home, base, agents := vendorTestSetup(t)

	// Hand-build skillm's own symlink (into Home) at claude's local target.
	folder := filepath.Dir(claudeSlot(base))
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink(store.SkillDir(home, "demo"), claudeSlot(base)); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	outcomes, err := vendorApply(home, "demo", agents, base, false, false)
	if err != nil {
		t.Fatalf("vendorApply: %v", err)
	}
	var claudeAction vendorAction = -1
	for _, o := range outcomes {
		if o.Agent.Name == "claude" {
			claudeAction = o.Action
		}
	}
	if claudeAction != vendorConverted {
		t.Fatalf("claude action = %v, want vendorConverted", claudeAction)
	}
	// The target is now a real directory, not a symlink.
	fi, err := os.Lstat(claudeSlot(base))
	if err != nil || fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
		t.Fatalf("converted target should be a real dir; mode=%v err=%v", fi.Mode(), err)
	}
}

// TestVendorForeignDirRefusedThenForced: an unrecorded foreign directory is a
// conflict that is left untouched without force and adopted with it.
func TestVendorForeignDirRefusedThenForced(t *testing.T) {
	home, base, agents := vendorTestSetup(t)

	foreign := claudeSlot(base)
	if err := os.MkdirAll(foreign, 0o755); err != nil {
		t.Fatalf("mkdir foreign: %v", err)
	}
	if err := os.WriteFile(filepath.Join(foreign, "MINE.txt"), []byte("hand\n"), 0o644); err != nil {
		t.Fatalf("write foreign: %v", err)
	}

	// Not recorded, not forced → reported as a conflict.
	conflicts := vendorConflicts(home, "demo", agents, base, false)
	found := false
	for _, c := range conflicts {
		if c == foreign {
			found = true
		}
	}
	if !found {
		t.Fatalf("vendorConflicts should include the foreign dir %s; got %v", foreign, conflicts)
	}

	// Apply without force → ATOMIC: the foreign claude slot blocks the whole base,
	// so EVERY agent (incl. agents, whose slot is absent) is reported blocked and
	// NOTHING is written. This is what stops the caller recording a half-foreign
	// root (the P1 bug: a recorded root with a skipped foreign dir gets clobbered
	// by a later update/uninstall).
	outcomes, err := vendorApply(home, "demo", agents, base, false, false)
	if err != nil {
		t.Fatalf("vendorApply (no force): %v", err)
	}
	for _, o := range outcomes {
		if o.Action != vendorBlocked {
			t.Fatalf("%s action = %v, want vendorBlocked (atomic skip)", o.Agent.Name, o.Action)
		}
	}
	if _, err := os.Stat(filepath.Join(foreign, "MINE.txt")); err != nil {
		t.Fatalf("blocked foreign dir must survive: %v", err)
	}
	// The clean agents slot must NOT have been written — no partial vendoring.
	if _, err := os.Lstat(agentsSlot(base)); !os.IsNotExist(err) {
		t.Fatalf("agents slot must stay empty on an atomic skip; lstat err = %v", err)
	}

	// Recorded == true means the dir is one of ours → refreshed (overwritten).
	if outs, err := vendorApply(home, "demo", agents, base, true, false); err != nil {
		t.Fatalf("vendorApply (recorded): %v", err)
	} else {
		for _, o := range outs {
			if o.Agent.Name == "claude" && o.Action != vendorRefreshed {
				t.Fatalf("recorded claude action = %v, want vendorRefreshed", o.Action)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(foreign, "MINE.txt")); !os.IsNotExist(err) {
		t.Fatalf("refresh should overwrite the dir; MINE.txt err = %v", err)
	}
}

// TestVendorRemove deletes only the real-directory copies, returning the agents
// whose copy was removed.
func TestVendorRemove(t *testing.T) {
	home, base, agents := vendorTestSetup(t)
	if _, err := vendorApply(home, "demo", agents, base, false, false); err != nil {
		t.Fatalf("seed copies: %v", err)
	}

	removed, err := vendorRemove(home, "demo", agents, base)
	if err != nil {
		t.Fatalf("vendorRemove: %v", err)
	}
	if len(removed) != 2 {
		t.Fatalf("removed %d agents, want 2", len(removed))
	}
	if _, err := os.Lstat(claudeSlot(base)); !os.IsNotExist(err) {
		t.Fatalf("copy should be gone after vendorRemove; err = %v", err)
	}
	// Idempotent: removing again removes nothing.
	if again, _ := vendorRemove(home, "demo", agents, base); len(again) != 0 {
		t.Fatalf("second vendorRemove removed %d, want 0", len(again))
	}
}
