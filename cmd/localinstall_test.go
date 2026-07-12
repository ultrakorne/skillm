package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ultrakorne/skillm/internal/agentdir"
	"github.com/ultrakorne/skillm/internal/config"
	"github.com/ultrakorne/skillm/internal/lockfile"
	"github.com/ultrakorne/skillm/internal/state"
	"github.com/ultrakorne/skillm/internal/store"
)

// localTestSetup builds a temp Home plus a source directory holding skill
// "demo"'s content, and returns the home, a fresh project base distinct from
// HOME (so each agent's local folder is real, not aliased to its global one),
// the source dir vendorOne copies from, and the default agents (claude+agents).
func localTestSetup(t *testing.T) (home, base, src string, agents []agentdir.Agent) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())

	home = t.TempDir()
	if err := store.EnsureHome(home); err != nil {
		t.Fatalf("EnsureHome: %v", err)
	}
	src = t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("demo body\n"), 0o644); err != nil {
		t.Fatalf("write src SKILL.md: %v", err)
	}
	return home, t.TempDir(), src, config.Default().AllAgents()
}

func demoSlot(base string) string   { return agentdir.CanonicalSkillDir(base, "demo") }
func claudeLink(base string) string { return filepath.Join(base, ".claude", "skills", "demo") }

// TestLocalInstallWritesCopyAndLinks: an absent slot gets the canonical copy,
// the canonical "agents" entry gets no link (the copy serves it), and claude
// gets a relative link resolving to the copy.
func TestLocalInstallWritesCopyAndLinks(t *testing.T) {
	home, base, src, agents := localTestSetup(t)

	action, err := vendorOne(home, "demo", src, agents, agentdir.Local, base, false, false, "local")
	if err != nil {
		t.Fatalf("vendorOne: %v", err)
	}
	if action != vendorWrote {
		t.Fatalf("action = %v, want vendorWrote", action)
	}

	// The canonical slot is a real directory with the skill's content.
	b, err := os.ReadFile(filepath.Join(demoSlot(base), "SKILL.md"))
	if err != nil || string(b) != "demo body\n" {
		t.Fatalf("canonical copy content = %q err=%v", b, err)
	}
	fi, _ := os.Lstat(demoSlot(base))
	if fi == nil || !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("canonical slot should be a real dir")
	}

	// claude got a relative link resolving to the copy.
	raw, err := os.Readlink(claudeLink(base))
	if err != nil {
		t.Fatalf("claude link missing: %v", err)
	}
	if filepath.IsAbs(raw) {
		t.Fatalf("claude link is absolute (%q), want relative", raw)
	}
	resolved := filepath.Clean(filepath.Join(filepath.Dir(claudeLink(base)), raw))
	if resolved != filepath.Clean(demoSlot(base)) {
		t.Fatalf("claude link resolves to %s, want canonical copy", resolved)
	}

	// servedAgents sees both the canonical agent and the linked one.
	names := servedAgents(home, "demo", agents, agentdir.Local, base)
	if strings.Join(names, ",") != "agents,claude" && strings.Join(names, ",") != "claude,agents" {
		t.Fatalf("served agents = %v, want agents+claude", names)
	}
}

// TestLocalInstallConvertsLegacyHomeSymlink: a pre-refactor absolute symlink
// into Home at the canonical slot is converted to a real copy without force.
func TestLocalInstallConvertsLegacyHomeSymlink(t *testing.T) {
	home, base, src, agents := localTestSetup(t)

	if err := os.MkdirAll(filepath.Dir(demoSlot(base)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, "skills", "demo"), demoSlot(base)); err != nil {
		t.Fatal(err)
	}

	action, err := vendorOne(home, "demo", src, agents, agentdir.Local, base, false, false, "local")
	if err != nil {
		t.Fatalf("vendorOne: %v", err)
	}
	if action != vendorConverted {
		t.Fatalf("action = %v, want vendorConverted", action)
	}
	fi, err := os.Lstat(demoSlot(base))
	if err != nil || fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
		t.Fatalf("converted slot should be a real dir; mode=%v err=%v", fi.Mode(), err)
	}
}

// TestLocalInstallForeignDirBlockedThenForced: an unrecorded foreign directory
// is a conflict — left untouched without force, adopted with it, refreshed
// when recorded.
func TestLocalInstallForeignDirBlockedThenForced(t *testing.T) {
	home, base, src, agents := localTestSetup(t)

	foreign := demoSlot(base)
	if err := os.MkdirAll(foreign, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(foreign, "MINE.txt"), []byte("hand\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if c := vendorConflict(home, "demo", agentdir.Local, base, false); c != foreign {
		t.Fatalf("vendorConflict = %q, want %q", c, foreign)
	}
	if c := vendorConflict(home, "demo", agentdir.Local, base, true); c != "" {
		t.Fatalf("recorded dir must not be a conflict, got %q", c)
	}

	// Not forced → blocked, nothing written, no links created.
	action, err := vendorOne(home, "demo", src, agents, agentdir.Local, base, false, false, "local")
	if err != nil {
		t.Fatalf("vendorOne (no force): %v", err)
	}
	if action != vendorBlocked {
		t.Fatalf("action = %v, want vendorBlocked", action)
	}
	if _, err := os.Stat(filepath.Join(foreign, "MINE.txt")); err != nil {
		t.Fatalf("blocked foreign dir must survive: %v", err)
	}
	if _, err := os.Lstat(claudeLink(base)); !os.IsNotExist(err) {
		t.Fatalf("no link may be created for a blocked install; lstat err = %v", err)
	}

	// Recorded → skillm's own copy → refreshed (overwritten).
	action, err = vendorOne(home, "demo", src, agents, agentdir.Local, base, true, false, "local")
	if err != nil {
		t.Fatalf("vendorOne (recorded): %v", err)
	}
	if action != vendorRefreshed {
		t.Fatalf("action = %v, want vendorRefreshed", action)
	}
	if _, err := os.Stat(filepath.Join(foreign, "MINE.txt")); !os.IsNotExist(err) {
		t.Fatalf("refresh should overwrite the dir; MINE.txt err = %v", err)
	}
}

// TestLocalRemove removes the agent links and the canonical copy, and is
// idempotent.
func TestLocalRemove(t *testing.T) {
	home, base, src, agents := localTestSetup(t)
	if _, err := vendorOne(home, "demo", src, agents, agentdir.Local, base, false, false, "local"); err != nil {
		t.Fatalf("seed install: %v", err)
	}

	removed, err := vendorRemove(home, "demo", agents, agentdir.Local, base, true, "local")
	if err != nil {
		t.Fatalf("vendorRemove: %v", err)
	}
	if !removed {
		t.Fatal("vendorRemove should report the copy removed")
	}
	if _, err := os.Lstat(demoSlot(base)); !os.IsNotExist(err) {
		t.Fatalf("canonical copy should be gone; err = %v", err)
	}
	if _, err := os.Lstat(claudeLink(base)); !os.IsNotExist(err) {
		t.Fatalf("claude link should be gone; err = %v", err)
	}
	// Idempotent.
	if again, _ := vendorRemove(home, "demo", agents, agentdir.Local, base, true, "local"); again {
		t.Fatal("second vendorRemove removed something")
	}
}

// TestLockEntrySync: installing writes a vercel-compatible lock entry; a
// re-upsert preserves foreign keys; removal drops the entry and deletes an
// emptied lockfile.
func TestLockEntrySync(t *testing.T) {
	home, base, src, agents := localTestSetup(t)
	if _, err := vendorOne(home, "demo", src, agents, agentdir.Local, base, false, false, "local"); err != nil {
		t.Fatalf("seed install: %v", err)
	}

	entry := state.SkillEntry{
		ID:       "demo",
		Kind:     state.KindGit,
		Source:   "https://github.com/owner/repo",
		Path:     "skills/demo",
		Ref:      "main",
		Revision: "abc",
	}
	upsertLockEntry(entry, base)

	lf, err := lockfile.Load(base)
	if err != nil {
		t.Fatal(err)
	}
	e := lf.Skills["demo"]
	if e == nil {
		t.Fatal("lock entry missing")
	}
	if e.Source != "owner/repo" || e.SourceType != "github" || e.Ref != "main" ||
		e.SkillPath != "skills/demo/SKILL.md" || len(e.ComputedHash) != 64 {
		t.Fatalf("lock entry mis-built: %+v", e)
	}

	// A foreign key on the entry survives a re-upsert.
	raw, _ := os.ReadFile(lockfile.Path(base))
	patched := strings.Replace(string(raw), "\"computedHash\"", "\"subagents\": [\"x\"],\n      \"computedHash\"", 1)
	if err := os.WriteFile(lockfile.Path(base), []byte(patched), 0o644); err != nil {
		t.Fatal(err)
	}
	upsertLockEntry(entry, base)
	raw, _ = os.ReadFile(lockfile.Path(base))
	if !strings.Contains(string(raw), "subagents") {
		t.Fatalf("re-upsert dropped foreign key:\n%s", raw)
	}

	removeLockEntry("demo", base)
	if _, err := os.Stat(lockfile.Path(base)); !os.IsNotExist(err) {
		t.Fatalf("emptied lockfile should be deleted; err = %v", err)
	}
}
