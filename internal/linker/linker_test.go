package linker

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ultrakorne/skillm/internal/agentdir"
	"github.com/ultrakorne/skillm/internal/store"
)

// fixture wires a temp Home (with a populated skill dir) and a temp project cwd
// so Local-scope links land entirely inside the test sandbox.
type fixture struct {
	home string
	cwd  string
}

func newFixture(t *testing.T, id string) fixture {
	t.Helper()
	home := filepath.Join(t.TempDir(), ".skillm")
	if err := store.EnsureHome(home); err != nil {
		t.Fatalf("EnsureHome: %v", err)
	}
	// Materialize the skill dir in Home so the link has a real target.
	if err := os.MkdirAll(store.SkillDir(home, id), 0o755); err != nil {
		t.Fatalf("create skill dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(store.SkillDir(home, id), "SKILL.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	return fixture{home: home, cwd: t.TempDir()}
}

// claude returns the claude agent from the registry.
func claude(t *testing.T) agentdir.Agent {
	t.Helper()
	for _, a := range agentdir.All() {
		if a.Name == "claude" {
			return a
		}
	}
	t.Fatal("claude not in registry")
	return agentdir.Agent{}
}

func agents(t *testing.T) []agentdir.Agent {
	t.Helper()
	return agentdir.Enabled([]string{"claude", "codex"})
}

func TestLink_FreshCreatesSymlink(t *testing.T) {
	const id = "demo"
	fx := newFixture(t, id)

	res, err := Link(fx.home, id, agents(t), agentdir.Local, fx.cwd)
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	if len(res.Agents) != 2 {
		t.Fatalf("expected 2 agent results, got %d", len(res.Agents))
	}
	for _, ar := range res.Agents {
		if ar.Action != ActionCreated {
			t.Errorf("agent %s: action = %s, want created", ar.Agent.Name, ar.Action)
		}
		// The link must exist and point at the Home skill dir.
		got, err := os.Readlink(ar.Path)
		if err != nil {
			t.Fatalf("readlink %s: %v", ar.Path, err)
		}
		want := store.SkillDir(fx.home, id)
		if filepath.Clean(got) != filepath.Clean(want) {
			t.Errorf("link %s -> %s, want %s", ar.Path, got, want)
		}
		// And it must resolve to a real directory.
		fi, err := os.Stat(ar.Path)
		if err != nil || !fi.IsDir() {
			t.Errorf("link %s does not resolve to a dir: %v", ar.Path, err)
		}
	}
}

func TestLink_IdempotentRelink(t *testing.T) {
	const id = "demo"
	fx := newFixture(t, id)
	ag := []agentdir.Agent{claude(t)}

	// Local scope keeps the link inside the temp cwd sandbox (Global would
	// resolve to the real ~/.claude/skills and leak across runs).
	if _, err := Link(fx.home, id, ag, agentdir.Local, fx.cwd); err != nil {
		t.Fatalf("first Link: %v", err)
	}
	res, err := Link(fx.home, id, ag, agentdir.Local, fx.cwd)
	if err != nil {
		t.Fatalf("second Link: %v", err)
	}
	if len(res.Agents) != 1 || res.Agents[0].Action != ActionAlreadyLinked {
		t.Fatalf("re-link action = %v, want already-linked", res.Agents)
	}
}

func TestLink_RefusesToClobberRealFile(t *testing.T) {
	const id = "demo"
	fx := newFixture(t, id)
	ag := []agentdir.Agent{claude(t)}

	// Put a real file exactly where the link would go.
	folder := agentdir.SkillsFolder(ag[0], agentdir.Local, fx.cwd)
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(folder, id)
	if err := os.WriteFile(linkPath, []byte("hand-written\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Link(fx.home, id, ag, agentdir.Local, fx.cwd)
	if err == nil {
		t.Fatal("expected refusal error when a real file occupies the link path")
	}
	// The user's file must be untouched.
	got, rerr := os.ReadFile(linkPath)
	if rerr != nil {
		t.Fatalf("user file vanished: %v", rerr)
	}
	if string(got) != "hand-written\n" {
		t.Errorf("user file clobbered: %q", string(got))
	}
}

func TestLink_RefusesToClobberRealDir(t *testing.T) {
	const id = "demo"
	fx := newFixture(t, id)
	ag := []agentdir.Agent{claude(t)}

	folder := agentdir.SkillsFolder(ag[0], agentdir.Local, fx.cwd)
	linkPath := filepath.Join(folder, id)
	if err := os.MkdirAll(linkPath, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(linkPath, "mine.txt")
	if err := os.WriteFile(sentinel, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Link(fx.home, id, ag, agentdir.Local, fx.cwd); err == nil {
		t.Fatal("expected refusal error when a real dir occupies the link path")
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("user directory contents disturbed: %v", err)
	}
}

func TestLink_RefusesForeignSymlink(t *testing.T) {
	const id = "demo"
	fx := newFixture(t, id)
	ag := []agentdir.Agent{claude(t)}

	// A symlink pointing OUTSIDE Home -> must not be touched.
	foreign := t.TempDir()
	folder := agentdir.SkillsFolder(ag[0], agentdir.Local, fx.cwd)
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(folder, id)
	if err := os.Symlink(foreign, linkPath); err != nil {
		t.Fatal(err)
	}

	if _, err := Link(fx.home, id, ag, agentdir.Local, fx.cwd); err == nil {
		t.Fatal("expected refusal error for a foreign symlink")
	}
	// Foreign link untouched.
	got, err := os.Readlink(linkPath)
	if err != nil {
		t.Fatalf("foreign link vanished: %v", err)
	}
	if filepath.Clean(got) != filepath.Clean(foreign) {
		t.Errorf("foreign link retargeted: %q", got)
	}
}

func TestLink_RepointsManagedLinkToDifferentSkill(t *testing.T) {
	const id = "demo"
	fx := newFixture(t, id)
	// A second skill in Home that the link currently (wrongly) points at.
	other := "other"
	if err := os.MkdirAll(store.SkillDir(fx.home, other), 0o755); err != nil {
		t.Fatal(err)
	}
	ag := []agentdir.Agent{claude(t)}

	folder := agentdir.SkillsFolder(ag[0], agentdir.Local, fx.cwd)
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(folder, id)
	// Existing managed link -> points into Home but at the wrong skill.
	if err := os.Symlink(store.SkillDir(fx.home, other), linkPath); err != nil {
		t.Fatal(err)
	}

	res, err := Link(fx.home, id, ag, agentdir.Local, fx.cwd)
	if err != nil {
		t.Fatalf("Link should repoint a managed link, got %v", err)
	}
	if res.Agents[0].Action != ActionCreated {
		t.Errorf("action = %s, want created (repointed)", res.Agents[0].Action)
	}
	got, _ := os.Readlink(linkPath)
	if filepath.Clean(got) != filepath.Clean(store.SkillDir(fx.home, id)) {
		t.Errorf("link not repointed: %q", got)
	}
}

func TestUnlink_RemovesOnlyHomePointingSymlink(t *testing.T) {
	const id = "demo"
	fx := newFixture(t, id)
	ag := agents(t)

	if _, err := Link(fx.home, id, ag, agentdir.Local, fx.cwd); err != nil {
		t.Fatalf("Link: %v", err)
	}
	res, err := Unlink(fx.home, id, ag, agentdir.Local, fx.cwd)
	if err != nil {
		t.Fatalf("Unlink: %v", err)
	}
	for _, ar := range res.Agents {
		if ar.Action != ActionRemoved {
			t.Errorf("agent %s: action = %s, want removed", ar.Agent.Name, ar.Action)
		}
		if _, err := os.Lstat(ar.Path); !os.IsNotExist(err) {
			t.Errorf("link %s still present after unlink: %v", ar.Path, err)
		}
	}
}

func TestUnlink_AbsentIsIdempotent(t *testing.T) {
	const id = "demo"
	fx := newFixture(t, id)
	res, err := Unlink(fx.home, id, agents(t), agentdir.Local, fx.cwd)
	if err != nil {
		t.Fatalf("Unlink on absent link should be nil, got %v", err)
	}
	for _, ar := range res.Agents {
		if ar.Action != ActionAbsent {
			t.Errorf("agent %s: action = %s, want absent", ar.Agent.Name, ar.Action)
		}
	}
}

func TestUnlink_RefusesForeignSymlink(t *testing.T) {
	const id = "demo"
	fx := newFixture(t, id)
	ag := []agentdir.Agent{claude(t)}

	foreign := t.TempDir()
	folder := agentdir.SkillsFolder(ag[0], agentdir.Local, fx.cwd)
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(folder, id)
	if err := os.Symlink(foreign, linkPath); err != nil {
		t.Fatal(err)
	}

	if _, err := Unlink(fx.home, id, ag, agentdir.Local, fx.cwd); err == nil {
		t.Fatal("expected refusal error: foreign symlink must not be removed")
	}
	if _, err := os.Lstat(linkPath); err != nil {
		t.Fatalf("foreign link wrongly removed: %v", err)
	}
}

func TestUnlink_RefusesRealFile(t *testing.T) {
	const id = "demo"
	fx := newFixture(t, id)
	ag := []agentdir.Agent{claude(t)}

	folder := agentdir.SkillsFolder(ag[0], agentdir.Local, fx.cwd)
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(folder, id)
	if err := os.WriteFile(linkPath, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Unlink(fx.home, id, ag, agentdir.Local, fx.cwd); err == nil {
		t.Fatal("expected refusal error: real file must not be removed")
	}
	if _, err := os.Stat(linkPath); err != nil {
		t.Fatalf("user file wrongly removed: %v", err)
	}
}

func TestScanLinks_DetectsLinks(t *testing.T) {
	const id = "demo"
	fx := newFixture(t, id)
	ag := agents(t)

	// Link only on claude (first agent); codex stays unlinked.
	if _, err := Link(fx.home, id, []agentdir.Agent{claude(t)}, agentdir.Local, fx.cwd); err != nil {
		t.Fatalf("Link: %v", err)
	}

	res, err := ScanLinks(fx.home, id, ag, agentdir.Local, fx.cwd)
	if err != nil {
		t.Fatalf("ScanLinks: %v", err)
	}
	if len(res.Agents) != 2 {
		t.Fatalf("expected 2 agent results, got %d", len(res.Agents))
	}
	byName := map[string]AgentResult{}
	for _, ar := range res.Agents {
		byName[ar.Agent.Name] = ar
	}
	if byName["claude"].Action != ActionFound {
		t.Errorf("claude action = %s, want found", byName["claude"].Action)
	}
	if filepath.Clean(byName["claude"].Target) != filepath.Clean(store.SkillDir(fx.home, id)) {
		t.Errorf("claude target = %q, want Home skill dir", byName["claude"].Target)
	}
	if byName["codex"].Action != ActionAbsent {
		t.Errorf("codex action = %s, want absent", byName["codex"].Action)
	}
}

func TestScanLinks_IgnoresForeignSymlink(t *testing.T) {
	const id = "demo"
	fx := newFixture(t, id)
	ag := []agentdir.Agent{claude(t)}

	foreign := t.TempDir()
	folder := agentdir.SkillsFolder(ag[0], agentdir.Local, fx.cwd)
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(foreign, filepath.Join(folder, id)); err != nil {
		t.Fatal(err)
	}

	res, err := ScanLinks(fx.home, id, ag, agentdir.Local, fx.cwd)
	if err != nil {
		t.Fatalf("ScanLinks: %v", err)
	}
	if res.Agents[0].Action != ActionAbsent {
		t.Errorf("foreign link reported as %s, want absent", res.Agents[0].Action)
	}
}

func TestScanAll_DiscoversEveryLinkedID(t *testing.T) {
	fx := newFixture(t, "alpha")
	// A second skill in Home.
	if err := os.MkdirAll(store.SkillDir(fx.home, "beta"), 0o755); err != nil {
		t.Fatal(err)
	}
	ag := []agentdir.Agent{claude(t)}

	if _, err := Link(fx.home, "alpha", ag, agentdir.Local, fx.cwd); err != nil {
		t.Fatal(err)
	}
	if _, err := Link(fx.home, "beta", ag, agentdir.Local, fx.cwd); err != nil {
		t.Fatal(err)
	}

	// Drop a foreign symlink and a real dir that must NOT be reported.
	folder := agentdir.SkillsFolder(ag[0], agentdir.Local, fx.cwd)
	if err := os.Symlink(t.TempDir(), filepath.Join(folder, "foreign")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(folder, "realdir"), 0o755); err != nil {
		t.Fatal(err)
	}

	links, err := ScanAll(fx.home, ag, agentdir.Local, fx.cwd)
	if err != nil {
		t.Fatalf("ScanAll: %v", err)
	}
	got := map[string]bool{}
	for _, l := range links {
		got[l.ID] = true
		if l.Scope != agentdir.Local || l.Agent.Name != "claude" {
			t.Errorf("link %s captured wrong agent/scope: %+v", l.ID, l)
		}
	}
	if !got["alpha"] || !got["beta"] {
		t.Errorf("ScanAll missed managed links: %v", got)
	}
	if got["foreign"] || got["realdir"] {
		t.Errorf("ScanAll reported non-managed entries: %v", got)
	}
}

func TestScanAll_MissingFolderSkipped(t *testing.T) {
	fx := newFixture(t, "alpha")
	links, err := ScanAll(fx.home, agents(t), agentdir.Local, fx.cwd)
	if err != nil {
		t.Fatalf("ScanAll with no folders should be nil err, got %v", err)
	}
	if len(links) != 0 {
		t.Errorf("expected no links, got %d", len(links))
	}
}

func TestLink_RelativeForeignSymlinkRefused(t *testing.T) {
	// A relative symlink that climbs out of Home's skills/ subtree must be
	// treated as foreign (regression guard for linkIntoHome's relative path
	// resolution).
	const id = "demo"
	fx := newFixture(t, id)
	ag := []agentdir.Agent{claude(t)}

	folder := agentdir.SkillsFolder(ag[0], agentdir.Local, fx.cwd)
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(folder, id)
	// Relative target escaping the folder to somewhere outside Home.
	if err := os.Symlink("../../../elsewhere", linkPath); err != nil {
		t.Fatal(err)
	}

	if _, err := Link(fx.home, id, ag, agentdir.Local, fx.cwd); err == nil {
		t.Fatal("expected refusal for relative foreign symlink")
	}
}
