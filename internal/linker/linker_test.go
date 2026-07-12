package linker

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ultrakorne/skillm/internal/agentdir"
	"github.com/ultrakorne/skillm/internal/store"
)

// fixture wires a temp Home (with a populated skill dir) and a temp project cwd
// so links land entirely inside the test sandbox. Local-scope links point at
// the project's canonical copy, so newFixture materializes one there too.
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
	// Materialize the skill dir in Home so global links have a real target.
	if err := os.MkdirAll(store.SkillDir(home, id), 0o755); err != nil {
		t.Fatalf("create skill dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(store.SkillDir(home, id), "SKILL.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	fx := fixture{home: home, cwd: t.TempDir()}
	fx.materializeCanonical(t, id)
	return fx
}

// materializeCanonical writes the canonical local copy of id at cwd, the
// target every local link resolves to.
func (fx fixture) materializeCanonical(t *testing.T, id string) {
	t.Helper()
	dir := agentdir.CanonicalSkillDir(fx.cwd, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// claude returns a claude test agent. Agent definitions come from config;
// tests construct them directly with the conventional locations.
func claude(t *testing.T) agentdir.Agent {
	t.Helper()
	return agentdir.Agent{Name: "claude", Global: "~/.claude/skills", Local: ".claude/skills"}
}

// agents returns claude plus a second non-canonical agent, so multi-agent
// linking is exercised.
func agents(t *testing.T) []agentdir.Agent {
	t.Helper()
	return []agentdir.Agent{
		claude(t),
		{Name: "cursor", Global: "~/.cursor/skills", Local: ".cursor/skills"},
	}
}

func TestLink_LocalCreatesRelativeSymlinkToCanonical(t *testing.T) {
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
		// The link must be RELATIVE (committable) and resolve to the canonical copy.
		raw, err := os.Readlink(ar.Path)
		if err != nil {
			t.Fatalf("readlink %s: %v", ar.Path, err)
		}
		if filepath.IsAbs(raw) {
			t.Errorf("local link %s is absolute (%q), want relative", ar.Path, raw)
		}
		resolved := filepath.Clean(filepath.Join(filepath.Dir(ar.Path), raw))
		want := filepath.Clean(agentdir.CanonicalSkillDir(fx.cwd, id))
		if resolved != want {
			t.Errorf("link %s resolves to %s, want %s", ar.Path, resolved, want)
		}
		// And it must resolve to a real directory.
		fi, err := os.Stat(ar.Path)
		if err != nil || !fi.IsDir() {
			t.Errorf("link %s does not resolve to a dir: %v", ar.Path, err)
		}
	}
}

func TestLink_LocalSkipsCanonicalAgent(t *testing.T) {
	const id = "demo"
	fx := newFixture(t, id)
	ag := []agentdir.Agent{{Name: "agents", Global: "~/.agents/skills", Local: ".agents/skills"}}

	res, err := Link(fx.home, id, ag, agentdir.Local, fx.cwd)
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	if len(res.Agents) != 0 {
		t.Fatalf("canonical agent must be skipped, got %+v", res.Agents)
	}
	// Nothing may have been written over the canonical copy.
	fi, err := os.Lstat(agentdir.CanonicalSkillDir(fx.cwd, id))
	if err != nil || !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		t.Errorf("canonical copy disturbed: %v %v", fi, err)
	}
}

func TestLink_GlobalCreatesAbsoluteSymlinkToHome(t *testing.T) {
	const id = "demo"
	fx := newFixture(t, id)
	// An absolute Global template keeps the test inside the sandbox.
	globalDir := filepath.Join(t.TempDir(), "skills")
	ag := []agentdir.Agent{{Name: "claude", Global: globalDir, Local: ".claude/skills"}}

	res, err := Link(fx.home, id, ag, agentdir.Global, fx.cwd)
	if err != nil {
		t.Fatalf("Link: %v", err)
	}
	if len(res.Agents) != 1 || res.Agents[0].Action != ActionCreated {
		t.Fatalf("global link not created: %+v", res.Agents)
	}
	got, err := os.Readlink(res.Agents[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Clean(got) != filepath.Clean(store.SkillDir(fx.home, id)) {
		t.Errorf("global link -> %s, want Home skill dir", got)
	}
}

func TestLink_IdempotentRelink(t *testing.T) {
	const id = "demo"
	fx := newFixture(t, id)
	ag := []agentdir.Agent{claude(t)}

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
	folder, _ := agentdir.SkillsFolder(ag[0], agentdir.Local, fx.cwd)
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

	folder, _ := agentdir.SkillsFolder(ag[0], agentdir.Local, fx.cwd)
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

	// A symlink pointing outside Home and outside the canonical store -> must
	// not be touched.
	foreign := t.TempDir()
	folder, _ := agentdir.SkillsFolder(ag[0], agentdir.Local, fx.cwd)
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

func TestLink_RepointsLegacyHomeLinkToCanonical(t *testing.T) {
	// A pre-refactor local install was an absolute symlink into Home. Link
	// recognizes it as skillm's and repoints it to the canonical relative form.
	const id = "demo"
	fx := newFixture(t, id)
	ag := []agentdir.Agent{claude(t)}

	folder, _ := agentdir.SkillsFolder(ag[0], agentdir.Local, fx.cwd)
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(folder, id)
	if err := os.Symlink(store.SkillDir(fx.home, id), linkPath); err != nil {
		t.Fatal(err)
	}

	res, err := Link(fx.home, id, ag, agentdir.Local, fx.cwd)
	if err != nil {
		t.Fatalf("Link should repoint a legacy Home link, got %v", err)
	}
	if res.Agents[0].Action != ActionCreated {
		t.Errorf("action = %s, want created (repointed)", res.Agents[0].Action)
	}
	raw, _ := os.Readlink(linkPath)
	if filepath.IsAbs(raw) {
		t.Errorf("repointed link still absolute: %q", raw)
	}
	resolved := filepath.Clean(filepath.Join(filepath.Dir(linkPath), raw))
	if resolved != filepath.Clean(agentdir.CanonicalSkillDir(fx.cwd, id)) {
		t.Errorf("link not repointed to canonical copy: %q", raw)
	}
}

func TestUnlink_RemovesOwnedSymlink(t *testing.T) {
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
	// The canonical copy must survive an unlink pass.
	if fi, err := os.Stat(agentdir.CanonicalSkillDir(fx.cwd, id)); err != nil || !fi.IsDir() {
		t.Errorf("canonical copy disturbed by Unlink: %v", err)
	}
}

func TestUnlink_RemovesLegacyHomeLink(t *testing.T) {
	const id = "demo"
	fx := newFixture(t, id)
	ag := []agentdir.Agent{claude(t)}

	folder, _ := agentdir.SkillsFolder(ag[0], agentdir.Local, fx.cwd)
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(store.SkillDir(fx.home, id), filepath.Join(folder, id)); err != nil {
		t.Fatal(err)
	}

	res, err := Unlink(fx.home, id, ag, agentdir.Local, fx.cwd)
	if err != nil {
		t.Fatalf("Unlink: %v", err)
	}
	if len(res.Agents) != 1 || res.Agents[0].Action != ActionRemoved {
		t.Fatalf("legacy Home link not removed: %+v", res.Agents)
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

func TestUnlink_SkipsCanonicalAgent(t *testing.T) {
	const id = "demo"
	fx := newFixture(t, id)
	ag := []agentdir.Agent{{Name: "agents", Global: "~/.agents/skills", Local: ".agents/skills"}}

	res, err := Unlink(fx.home, id, ag, agentdir.Local, fx.cwd)
	if err != nil {
		t.Fatalf("Unlink must skip the canonical agent, got %v", err)
	}
	if len(res.Agents) != 0 {
		t.Fatalf("canonical agent must be skipped, got %+v", res.Agents)
	}
	if _, err := os.Stat(agentdir.CanonicalSkillDir(fx.cwd, id)); err != nil {
		t.Fatalf("canonical copy wrongly removed: %v", err)
	}
}

func TestUnlink_RefusesForeignSymlink(t *testing.T) {
	const id = "demo"
	fx := newFixture(t, id)
	ag := []agentdir.Agent{claude(t)}

	foreign := t.TempDir()
	folder, _ := agentdir.SkillsFolder(ag[0], agentdir.Local, fx.cwd)
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

	folder, _ := agentdir.SkillsFolder(ag[0], agentdir.Local, fx.cwd)
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

	// Link only on claude (first agent); cursor stays unlinked.
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
	if filepath.Clean(byName["claude"].Target) != filepath.Clean(agentdir.CanonicalSkillDir(fx.cwd, id)) {
		t.Errorf("claude target = %q, want canonical copy", byName["claude"].Target)
	}
	if byName["cursor"].Action != ActionAbsent {
		t.Errorf("cursor action = %s, want absent", byName["cursor"].Action)
	}
}

func TestScanLinks_IgnoresForeignSymlink(t *testing.T) {
	const id = "demo"
	fx := newFixture(t, id)
	ag := []agentdir.Agent{claude(t)}

	foreign := t.TempDir()
	folder, _ := agentdir.SkillsFolder(ag[0], agentdir.Local, fx.cwd)
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
	// A second skill in Home and in the canonical store.
	if err := os.MkdirAll(store.SkillDir(fx.home, "beta"), 0o755); err != nil {
		t.Fatal(err)
	}
	fx.materializeCanonical(t, "beta")
	ag := []agentdir.Agent{claude(t)}

	if _, err := Link(fx.home, "alpha", ag, agentdir.Local, fx.cwd); err != nil {
		t.Fatal(err)
	}
	if _, err := Link(fx.home, "beta", ag, agentdir.Local, fx.cwd); err != nil {
		t.Fatal(err)
	}

	// Drop a foreign symlink and a real dir that must NOT be reported.
	folder, _ := agentdir.SkillsFolder(ag[0], agentdir.Local, fx.cwd)
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
	// A relative symlink that climbs out of both Home's skills/ subtree and the
	// canonical store must be treated as foreign (regression guard for the
	// relative path resolution in ownedLink).
	const id = "demo"
	fx := newFixture(t, id)
	ag := []agentdir.Agent{claude(t)}

	folder, _ := agentdir.SkillsFolder(ag[0], agentdir.Local, fx.cwd)
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(folder, id)
	// Relative target escaping the folder to somewhere outside Home and the
	// canonical store.
	if err := os.Symlink("../../../elsewhere", linkPath); err != nil {
		t.Fatal(err)
	}

	if _, err := Link(fx.home, id, ag, agentdir.Local, fx.cwd); err == nil {
		t.Fatal("expected refusal for relative foreign symlink")
	}
}
