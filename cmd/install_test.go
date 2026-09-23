package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ultrakorne/skillm/internal/agentdir"
	"github.com/ultrakorne/skillm/internal/config"
	"github.com/ultrakorne/skillm/internal/state"
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

	agents := config.Default().AllAgents() // agents, claude (sorted)
	a := agents[1]                         // claude: a non-canonical agent, so its folder is distinct from the .agents store
	cwd := t.TempDir()

	// Nothing linked yet.
	if got := installedMark(home, id, agents, cwd, false); got != "" {
		t.Fatalf("unlinked mark = %q, want empty", got)
	}

	// A local link in a DIFFERENT directory must NOT count as installed.
	linkInto(t, home, a, agentdir.Local, t.TempDir(), id)
	if got := installedMark(home, id, agents, cwd, false); got != "" {
		t.Fatalf("local link elsewhere counted as installed: mark = %q, want empty", got)
	}

	// A local link in cwd counts as installed (local).
	linkInto(t, home, a, agentdir.Local, cwd, id)
	if got := installedMark(home, id, agents, cwd, false); got != " (installed: local)" {
		t.Fatalf("local-in-cwd mark = %q, want %q", got, " (installed: local)")
	}

	// Adding a global link makes it both, in scope order.
	linkInto(t, home, a, agentdir.Global, "", id)
	if got := installedMark(home, id, agents, cwd, false); got != " (installed: global, local)" {
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

	agents := config.Default().AllAgents() // agents, claude (sorted)
	a := agents[1]                         // claude: a non-canonical agent, so its folder is distinct from the .agents store

	// A single global link, scanned with cwd == HOME.
	linkInto(t, home, a, agentdir.Global, "", id)
	if got := installedMark(home, id, agents, fakeHome, false); got != " (installed: global)" {
		t.Fatalf("mark from home = %q, want %q", got, " (installed: global)")
	}
}

// TestInstalledMarkGlobalRecordedCopy verifies that a recorded Global install
// counts as "installed: global" through its canonical ~/.agents/skills copy
// alone — the case where only .agents-native agents are enabled, so no agent
// holds a link at all.
func TestInstalledMarkGlobalRecordedCopy(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("USERPROFILE", fakeHome)

	home := t.TempDir()
	if err := store.EnsureHome(home); err != nil {
		t.Fatalf("EnsureHome: %v", err)
	}
	const id = "demo"
	agents := config.Default().AllAgents()
	cwd := t.TempDir()

	// The canonical global copy exists but is not recorded: not installed.
	if err := os.MkdirAll(agentdir.CanonicalSkillDirAt(agentdir.Global, "", id), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := installedMark(home, id, agents, cwd, false); got != "" {
		t.Fatalf("unrecorded copy counted as installed: mark = %q, want empty", got)
	}
	// Recorded: installed globally.
	if got := installedMark(home, id, agents, cwd, true); got != " (installed: global)" {
		t.Fatalf("recorded copy mark = %q, want %q", got, " (installed: global)")
	}
}

// linkInto creates a skillm-style symlink for agent a at the given scope and
// base directory, pointing at the legacy Home skills path (<home>/skills/<id>)
// — a shape the linker still recognizes as its own. Using the legacy target
// keeps the link scan under test without materializing a real canonical copy,
// which at cwd==home would sit at the same path as the local canonical store
// and blur the global/local distinction these tests pin down. The folder is
// created as needed.
func linkInto(t *testing.T, home string, a agentdir.Agent, scope agentdir.Scope, base, id string) {
	t.Helper()
	target := filepath.Join(home, "skills", id)
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

// localTestSetup builds a temp Home plus a source directory holding skill
// "demo"'s content, and returns the home, a fresh project base distinct from
// HOME (so each agent's local folder is real, not aliased to its global one),
// the source dir core.VendorOne copies from, and the default agents (claude+agents).
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

func claudeLink(base string) string { return filepath.Join(base, ".claude", "skills", "demo") }

// TestInstallYesDoesNotTakeOverAgentLinks: --yes only answers prompts, so an
// install with --yes (but not --force) must leave a hand-made skill at an
// agent's link path alone; only --force takes it over.
func TestInstallYesDoesNotTakeOverAgentLinks(t *testing.T) {
	home, base, src, agents := localTestSetup(t)
	if err := os.MkdirAll(claudeLink(base), 0o755); err != nil {
		t.Fatal(err)
	}
	notes := filepath.Join(claudeLink(base), "NOTES.md")
	if err := os.WriteFile(notes, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	items := []stagedSkill{{entry: state.SkillEntry{ID: "demo", Kind: state.KindLocal, Source: src}, dir: src}}

	oldYes, oldForce := flagYes, flagForce
	t.Cleanup(func() { flagYes, flagForce = oldYes, oldForce })

	flagYes, flagForce = true, false
	if err := installVendored(home, &state.State{}, items, agents, agentdir.Local, base, "local"); err != nil {
		t.Fatalf("installVendored --yes: %v", err)
	}
	if _, err := os.Stat(notes); err != nil {
		t.Fatalf("--yes must not take over the agent link path: %v", err)
	}

	flagYes, flagForce = false, true
	if err := installVendored(home, &state.State{}, items, agents, agentdir.Local, base, "local"); err != nil {
		t.Fatalf("installVendored --force: %v", err)
	}
	if fi, err := os.Lstat(claudeLink(base)); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("--force must take over the agent link path (err=%v)", err)
	}
}
