package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ultrakorne/skillm/internal/agentdir"
	"github.com/ultrakorne/skillm/internal/linker"
	"github.com/ultrakorne/skillm/internal/state"
	"github.com/ultrakorne/skillm/internal/store"
)

// These tests exercise the `agent` reconcile helpers in-process. The command's
// multiselect needs a TTY (covered by the binary's non-TTY contract elsewhere),
// but the enable/disable passes take an explicit Home and need no terminal, so
// they validate the core behaviour directly: enabling mirrors a peer's link
// footprint; disabling removes only that agent's links and never touches Home.

// testAgents returns claude and codex wired to isolated, absolute global folders
// under globalRoot (so they never touch the developer's dotfiles) and the
// conventional relative local folders.
func testAgents(globalRoot string) (claude, codex agentdir.Agent) {
	claude = agentdir.Agent{
		Name:   "claude",
		Global: filepath.Join(globalRoot, ".claude", "skills"),
		Local:  ".claude/skills",
	}
	codex = agentdir.Agent{
		Name:   "codex",
		Global: filepath.Join(globalRoot, ".codex", "skills"),
		Local:  ".codex/skills",
	}
	return claude, codex
}

// makeSkill creates a minimal skill directory (with a SKILL.md) in Home so links
// to it resolve to a real, readable target.
func makeSkill(t *testing.T, home, id string) {
	t.Helper()
	dir := store.SkillDir(home, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir skill %s: %v", id, err)
	}
	body := "---\nname: " + id + "\ndescription: " + id + " skill\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write SKILL.md for %s: %v", id, err)
	}
}

// mustLink links one skill for one agent at scope/base, failing on error.
func mustLink(t *testing.T, home, id string, a agentdir.Agent, scope agentdir.Scope, base string) {
	t.Helper()
	if _, err := linker.Link(home, id, []agentdir.Agent{a}, scope, base); err != nil {
		t.Fatalf("link %s for %s (%s): %v", id, a.Name, scope, err)
	}
}

// linkPath is the link path for skill id in agent a's folder at scope/base.
func linkPath(t *testing.T, a agentdir.Agent, scope agentdir.Scope, base, id string) string {
	t.Helper()
	p, ok := agentdir.LinkPath(a, scope, base, id)
	if !ok {
		t.Fatalf("%s defines no %s folder", a.Name, scope)
	}
	return p
}

// assertResolves verifies linkPath is a symlink into Home for id, and that the
// skill's SKILL.md is reachable through it (the link is live, not dangling).
func assertResolves(t *testing.T, home, lp, id string) {
	t.Helper()
	target, err := os.Readlink(lp)
	if err != nil {
		t.Fatalf("readlink %s: %v", lp, err)
	}
	if filepath.Clean(target) != filepath.Clean(store.SkillDir(home, id)) {
		t.Fatalf("link %s -> %q, want %q", lp, target, store.SkillDir(home, id))
	}
	if _, err := os.Stat(filepath.Join(lp, "SKILL.md")); err != nil {
		t.Fatalf("SKILL.md not reachable through %s: %v", lp, err)
	}
}

// assertAbsent fails if anything exists at lp.
func assertAbsent(t *testing.T, lp string) {
	t.Helper()
	if _, err := os.Lstat(lp); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be absent, lstat err = %v", lp, err)
	}
}

// TestEnableAgentMirrorsFootprint: enabling codex links it at every place the
// before-enabled claude is linked — global and the tracked project — and leaves
// claude untouched. Places where claude has no link are not invented.
func TestEnableAgentMirrorsFootprint(t *testing.T) {
	home := t.TempDir()
	globalRoot := t.TempDir()
	projA := t.TempDir()
	cwd := t.TempDir() // an empty project: claude has nothing here, so nothing mirrors

	claude, codex := testAgents(globalRoot)
	makeSkill(t, home, "alpha")
	makeSkill(t, home, "beta")

	// claude footprint: alpha@global, beta@global, beta@local:projA.
	mustLink(t, home, "alpha", claude, agentdir.Global, cwd)
	mustLink(t, home, "beta", claude, agentdir.Global, cwd)
	mustLink(t, home, "beta", claude, agentdir.Local, projA)

	st := &state.State{LocalRoots: []string{projA}}
	enableAgent(home, codex, []agentdir.Agent{claude}, st, cwd)

	// codex now mirrors claude exactly.
	assertResolves(t, home, linkPath(t, codex, agentdir.Global, cwd, "alpha"), "alpha")
	assertResolves(t, home, linkPath(t, codex, agentdir.Global, cwd, "beta"), "beta")
	assertResolves(t, home, linkPath(t, codex, agentdir.Local, projA, "beta"), "beta")

	// No invented links: claude had no alpha in projA and nothing in cwd.
	assertAbsent(t, linkPath(t, codex, agentdir.Local, projA, "alpha"))
	assertAbsent(t, linkPath(t, codex, agentdir.Local, cwd, "alpha"))
	assertAbsent(t, linkPath(t, codex, agentdir.Local, cwd, "beta"))
	if len(st.LocalRoots) != 1 || st.LocalRoots[0] != projA {
		t.Fatalf("LocalRoots = %v, want only tracked project %s", st.LocalRoots, projA)
	}

	// claude's own links are untouched by enabling a peer.
	assertResolves(t, home, linkPath(t, claude, agentdir.Global, cwd, "alpha"), "alpha")
	assertResolves(t, home, linkPath(t, claude, agentdir.Local, projA, "beta"), "beta")
}

// TestEnableAgentRecordsUntrackedCwdWithLocalFootprint covers a project that
// already has a before-enabled agent's local link, but was not yet listed in
// state. Enabling a peer creates a new local link there, so the root must become
// tracked for later list/uninstall runs from another directory.
func TestEnableAgentRecordsUntrackedCwdWithLocalFootprint(t *testing.T) {
	home := t.TempDir()
	globalRoot := t.TempDir()
	cwd := t.TempDir()

	claude, codex := testAgents(globalRoot)
	makeSkill(t, home, "alpha")
	mustLink(t, home, "alpha", claude, agentdir.Local, cwd)

	st := &state.State{}
	if !enableAgent(home, codex, []agentdir.Agent{claude}, st, cwd) {
		t.Fatal("enableAgent did not report a state change")
	}

	assertResolves(t, home, linkPath(t, codex, agentdir.Local, cwd, "alpha"), "alpha")
	if len(st.LocalRoots) != 1 || st.LocalRoots[0] != cwd {
		t.Fatalf("LocalRoots = %v, want [%s]", st.LocalRoots, cwd)
	}
}

// TestEnableAgentFromHomeMirrorsGlobalOnly: enabling an agent while the working
// directory is the home directory must mirror the peer's links via the GLOBAL
// pass only, and must never record home as a tracked local root. testAgents uses
// globalRoot as the global parent, so running with cwd == globalRoot reproduces
// the alias condition (each agent's local folder there IS its global folder).
func TestEnableAgentFromHomeMirrorsGlobalOnly(t *testing.T) {
	home := t.TempDir()
	globalRoot := t.TempDir()

	claude, codex := testAgents(globalRoot)
	makeSkill(t, home, "alpha")
	mustLink(t, home, "alpha", claude, agentdir.Global, globalRoot)

	st := &state.State{}
	enableAgent(home, codex, []agentdir.Agent{claude}, st, globalRoot)

	// codex mirrors claude's global link.
	assertResolves(t, home, linkPath(t, codex, agentdir.Global, globalRoot, "alpha"), "alpha")

	// Home was never recorded as a local root: the local pass is skipped because
	// local aliases global there.
	if len(st.LocalRoots) != 0 {
		t.Fatalf("LocalRoots = %v, want empty (home must not be tracked as a local root)", st.LocalRoots)
	}
}

// TestEnableAgentSkipsAliasedPeerFootprintAtHome guards the *source* side of the
// home invariant: a before-enabled peer whose local folder aliases its global
// one at the root (here claude at globalRoot) must NOT have its global links
// read as a local footprint and mirrored into a newly enabled agent that has a
// genuine local folder there (weird, whose global/local templates diverge). Such
// a mirror would invent local links for global-only skills and resurrect the
// bogus root.
func TestEnableAgentSkipsAliasedPeerFootprintAtHome(t *testing.T) {
	home := t.TempDir()
	globalRoot := t.TempDir()

	// claude aliases local->global at base == globalRoot (the home-equivalent).
	claude := agentdir.Agent{
		Name:   "claude",
		Global: filepath.Join(globalRoot, ".claude", "skills"),
		Local:  ".claude/skills",
	}
	// weird's global and local templates diverge, so it keeps a *real* local
	// scope even at globalRoot — it is not skipped by the target-side guard.
	weird := agentdir.Agent{
		Name:   "weird",
		Global: filepath.Join(globalRoot, ".weird-global", "skills"),
		Local:  ".weird-local/skills",
	}

	makeSkill(t, home, "alpha")
	// claude has alpha only GLOBALLY; its global folder == its local folder at
	// globalRoot, so a naive local scan there would surface alpha as "local".
	mustLink(t, home, "alpha", claude, agentdir.Global, globalRoot)

	st := &state.State{}
	enableAgent(home, weird, []agentdir.Agent{claude}, st, globalRoot)

	// weird mirrors alpha via the GLOBAL pass — that part is correct.
	assertResolves(t, home, linkPath(t, weird, agentdir.Global, globalRoot, "alpha"), "alpha")

	// But weird must get NO local link at globalRoot: claude's global-only alpha
	// is not a real local footprint, so nothing is mirrored locally...
	assertAbsent(t, linkPath(t, weird, agentdir.Local, globalRoot, "alpha"))
	// ...and the bogus home root is never recorded.
	if len(st.LocalRoots) != 0 {
		t.Fatalf("LocalRoots = %v, want empty (a phantom local footprint must not resurrect home)", st.LocalRoots)
	}
}

// TestDisableAgentUnlinksButKeepsHome: disabling claude removes its links across
// global and the tracked project, but the Home copies survive and codex's links
// are untouched — disabling an agent is not uninstalling a skill.
func TestDisableAgentUnlinksButKeepsHome(t *testing.T) {
	home := t.TempDir()
	globalRoot := t.TempDir()
	projA := t.TempDir()
	cwd := t.TempDir()

	claude, codex := testAgents(globalRoot)
	makeSkill(t, home, "alpha")
	makeSkill(t, home, "beta")

	// Both agents linked at global; claude also linked in projA.
	for _, id := range []string{"alpha", "beta"} {
		mustLink(t, home, id, claude, agentdir.Global, cwd)
		mustLink(t, home, id, codex, agentdir.Global, cwd)
	}
	mustLink(t, home, "alpha", claude, agentdir.Local, projA)

	st := &state.State{LocalRoots: []string{projA}}
	disableAgent(home, claude, st, cwd)

	// claude's links are gone everywhere.
	assertAbsent(t, linkPath(t, claude, agentdir.Global, cwd, "alpha"))
	assertAbsent(t, linkPath(t, claude, agentdir.Global, cwd, "beta"))
	assertAbsent(t, linkPath(t, claude, agentdir.Local, projA, "alpha"))

	// Home copies survive — this is not uninstall.
	if !store.Exists(home, "alpha") || !store.Exists(home, "beta") {
		t.Fatal("disabling an agent must not delete skills from Home")
	}

	// codex is untouched.
	assertResolves(t, home, linkPath(t, codex, agentdir.Global, cwd, "alpha"), "alpha")
	assertResolves(t, home, linkPath(t, codex, agentdir.Global, cwd, "beta"), "beta")
}

// TestAgentSwapTransfersFootprint reproduces the one-shot swap (disable claude +
// enable codex) at the helper level in the order runAgent applies it — enable
// pass first, then disable pass — proving codex inherits claude's footprint
// before claude is torn down.
func TestAgentSwapTransfersFootprint(t *testing.T) {
	home := t.TempDir()
	globalRoot := t.TempDir()
	projA := t.TempDir()
	cwd := t.TempDir()

	claude, codex := testAgents(globalRoot)
	makeSkill(t, home, "alpha")
	mustLink(t, home, "alpha", claude, agentdir.Global, cwd)
	mustLink(t, home, "alpha", claude, agentdir.Local, projA)

	st := &state.State{LocalRoots: []string{projA}}

	// before-enabled = {claude}; enable pass copies claude's links to codex while
	// they are still on disk, then the disable pass removes claude's.
	enableAgent(home, codex, []agentdir.Agent{claude}, st, cwd)
	disableAgent(home, claude, st, cwd)

	// codex inherited the whole footprint.
	assertResolves(t, home, linkPath(t, codex, agentdir.Global, cwd, "alpha"), "alpha")
	assertResolves(t, home, linkPath(t, codex, agentdir.Local, projA, "alpha"), "alpha")

	// claude has nothing left, but alpha stays in Home.
	assertAbsent(t, linkPath(t, claude, agentdir.Global, cwd, "alpha"))
	assertAbsent(t, linkPath(t, claude, agentdir.Local, projA, "alpha"))
	if !store.Exists(home, "alpha") {
		t.Fatal("swap must not delete alpha from Home")
	}
}

// TestEnableSkipsForeignObstruction: a real directory sitting where codex's link
// would go is left untouched and does not abort the sweep — the other links are
// still created.
func TestEnableSkipsForeignObstruction(t *testing.T) {
	home := t.TempDir()
	globalRoot := t.TempDir()
	cwd := t.TempDir()

	claude, codex := testAgents(globalRoot)
	makeSkill(t, home, "alpha")
	makeSkill(t, home, "beta")
	mustLink(t, home, "alpha", claude, agentdir.Global, cwd)
	mustLink(t, home, "beta", claude, agentdir.Global, cwd)

	// Plant a real directory where codex's alpha link would be created.
	obstruction := linkPath(t, codex, agentdir.Global, cwd, "alpha")
	if err := os.MkdirAll(obstruction, 0o755); err != nil {
		t.Fatalf("plant obstruction: %v", err)
	}
	sentinel := filepath.Join(obstruction, "mine.txt")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}

	st := &state.State{LocalRoots: nil}
	enableAgent(home, codex, []agentdir.Agent{claude}, st, cwd)

	// The obstruction is untouched (still a real directory with its file).
	fi, err := os.Lstat(obstruction)
	if err != nil || fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
		t.Fatalf("obstruction was modified: mode=%v err=%v", fi.Mode(), err)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("sentinel file lost: %v", err)
	}
	// The sweep continued: beta was still linked for codex.
	assertResolves(t, home, linkPath(t, codex, agentdir.Global, cwd, "beta"), "beta")
}

// TestFootprintIDs returns the de-duplicated, sorted union of ids linked across
// the given agents at a scope.
func TestFootprintIDs(t *testing.T) {
	home := t.TempDir()
	globalRoot := t.TempDir()
	cwd := t.TempDir()

	claude, codex := testAgents(globalRoot)
	for _, id := range []string{"alpha", "beta", "gamma"} {
		makeSkill(t, home, id)
	}
	mustLink(t, home, "beta", claude, agentdir.Global, cwd)
	mustLink(t, home, "alpha", claude, agentdir.Global, cwd)
	mustLink(t, home, "beta", codex, agentdir.Global, cwd) // overlaps with claude

	got := footprintIDs(home, []agentdir.Agent{claude, codex}, agentdir.Global, cwd)
	want := []string{"alpha", "beta"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("footprintIDs = %v, want %v", got, want)
	}
}

// TestConfirmAgentPrompt names the affected agents and reassures that Home is
// untouched, in both the disable-only and swap shapes.
func TestConfirmAgentPrompt(t *testing.T) {
	claude := agentdir.Agent{Name: "claude"}
	codex := agentdir.Agent{Name: "codex"}

	disableOnly := confirmAgentPrompt(nil, []agentdir.Agent{claude})
	if !strings.Contains(disableOnly, "claude") || !strings.Contains(disableOnly, "stay in Home") {
		t.Fatalf("disable-only prompt missing agent or Home reassurance: %q", disableOnly)
	}
	if strings.Contains(disableOnly, "Enable") {
		t.Fatalf("disable-only prompt should not mention enabling: %q", disableOnly)
	}

	swap := confirmAgentPrompt([]agentdir.Agent{codex}, []agentdir.Agent{claude})
	if !strings.Contains(swap, "Enable codex") || !strings.Contains(swap, "disable claude") {
		t.Fatalf("swap prompt missing enable/disable detail: %q", swap)
	}
}
