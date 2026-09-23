package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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

	changed, _ := refreshVendoredCopies(home, agents, st, []string{"alpha"}, map[string]bool{}, map[string]string{}, false)
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

	refreshVendoredCopies(home, agents, st, []string{"alpha"}, map[string]bool{}, map[string]string{}, false)
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

// TestClassifyStagingErr pins the rule that keeps an up-to-date skill from
// failing the run: materializing the upstream tree is now unconditional (it is
// what installs are compared against), so its failure must stay fatal only when
// the revision actually advanced and the content is genuinely needed.
func TestClassifyStagingErr(t *testing.T) {
	boom := errors.New("no space left on device")

	advanced := classifyStagingErr(boom, true)
	if errors.Is(advanced, errDriftCheckSkipped) {
		t.Fatal("a staging failure on an advanced revision must stay fatal")
	}
	if !errors.Is(advanced, boom) {
		t.Fatalf("the underlying cause must survive; got %v", advanced)
	}

	unchanged := classifyStagingErr(boom, false)
	if !errors.Is(unchanged, errDriftCheckSkipped) {
		t.Fatalf("a staging failure on an unchanged revision must be non-fatal; got %v", unchanged)
	}
	if !strings.Contains(unchanged.Error(), boom.Error()) {
		t.Fatalf("the reported message must name the cause; got %v", unchanged)
	}
}

// TestRefreshVendoredCopiesForceTakesOverAgentDir: a skill copied by hand
// into an agent folder blocks skillm's link on a plain update, and --force
// replaces it with the link even though the canonical copy is already in sync.
func TestRefreshVendoredCopiesForceTakesOverAgentDir(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	claude, _ := testAgents(sandboxGlobalRoot(t))
	agents := []agentdir.Agent{claude}
	st := &state.State{Skills: []state.SkillEntry{{
		ID: "alpha", Kind: state.KindGit, Source: "u", Path: "alpha", Ref: "main", Revision: "r",
		VendoredAt: []string{root},
	}}}
	makeCanonicalCopy(t, agentdir.Local, root, "alpha")
	lp := linkPath(t, claude, agentdir.Local, root, "alpha")
	if err := os.MkdirAll(lp, 0o755); err != nil {
		t.Fatal(err)
	}

	// Copy in sync, no --force: the foreign dir stays.
	staged := map[string]string{"alpha": agentdir.CanonicalSkillDir(root, "alpha")}
	refreshVendoredCopies(home, agents, st, []string{"alpha"}, map[string]bool{}, staged, false)
	if fi, err := os.Lstat(lp); err != nil || fi.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("plain update must leave the foreign dir alone (err=%v)", err)
	}

	if _, synced := refreshVendoredCopies(home, agents, st, []string{"alpha"}, map[string]bool{}, staged, true); !synced {
		t.Fatal("a takeover is work done: synced must be true so update does not claim everything was up to date")
	}
	fi, err := os.Lstat(lp)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("--force must replace the foreign dir with a link (err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(lp, "SKILL.md")); err != nil {
		t.Fatalf("link does not resolve to the canonical copy: %v", err)
	}
}
