package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ultrakorne/skillm/internal/agentdir"
	"github.com/ultrakorne/skillm/internal/config"
	"github.com/ultrakorne/skillm/internal/state"
	"github.com/ultrakorne/skillm/internal/store"
)

func TestSourceLabel(t *testing.T) {
	tests := []struct {
		name string
		e    state.SkillEntry
		want string
	}{
		{
			name: "git with subpath",
			e:    state.SkillEntry{Kind: state.KindGit, Source: "https://example.com/repo", Path: "sub"},
			want: "https://example.com/repo//sub",
		},
		{
			name: "git root (no subpath)",
			e:    state.SkillEntry{Kind: state.KindGit, Source: "https://example.com/repo"},
			want: "https://example.com/repo",
		},
		{
			name: "local",
			e:    state.SkillEntry{Kind: state.KindLocal, Source: "/home/me/skill", Path: "ignored"},
			want: "/home/me/skill",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := sourceLabel(tc.e); got != tc.want {
				t.Fatalf("sourceLabel = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestLinkedLabel verifies the live link scan + formatting: a skill linked at
// global scope for one agent should render that scope and agent, and a skill
// linked nowhere renders "-".
func TestLinkedLabel(t *testing.T) {
	// Redirect the user's home dir so the Global agent folder lands in a temp
	// location and the test never touches the real ~/.claude or ~/.agents.
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	home := t.TempDir()
	if err := store.EnsureHome(home); err != nil {
		t.Fatalf("EnsureHome: %v", err)
	}
	const id = "demo"
	skillDir := store.SkillDir(home, id)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill: %v", err)
	}

	agents := config.Default().AllAgents() // agents, claude (sorted)

	// Nothing linked yet.
	if got := linkedLabel(home, state.SkillEntry{ID: id}, agents, nil, t.TempDir()); got != "-" {
		t.Fatalf("unlinked label = %q, want %q", got, "-")
	}

	// Hand-build a global link for the first agent only, pointing into Home.
	a := agents[0]
	folder, _ := agentdir.SkillsFolder(a, agentdir.Global, "")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Skipf("cannot create global folder %s (no writable HOME?): %v", folder, err)
	}
	link := filepath.Join(folder, id)
	_ = os.Remove(link)
	if err := os.Symlink(skillDir, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(link) })

	got := linkedLabel(home, state.SkillEntry{ID: id}, agents, nil, t.TempDir())
	want := "global: " + a.Name
	if got != want {
		t.Fatalf("linked label = %q, want %q", got, want)
	}
}

// TestLinkedLabelLocalRoots verifies a local link in a tracked root that is NOT
// the current directory is discovered and rendered with its full path.
func TestLinkedLabelLocalRoots(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)

	home := t.TempDir()
	if err := store.EnsureHome(home); err != nil {
		t.Fatalf("EnsureHome: %v", err)
	}
	const id = "demo"
	skillDir := store.SkillDir(home, id)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill: %v", err)
	}

	agents := config.Default().AllAgents()
	a := agents[0]

	// Hand-build a local link in a project dir that is not the cwd we scan from.
	proj := t.TempDir()
	folder, _ := agentdir.SkillsFolder(a, agentdir.Local, proj)
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatalf("mkdir local folder: %v", err)
	}
	if err := os.Symlink(skillDir, filepath.Join(folder, id)); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	cwd := t.TempDir() // a different directory with no links of its own
	got := linkedLabel(home, state.SkillEntry{ID: id}, agents, []string{proj}, cwd)
	want := fmt.Sprintf("local(%s): %s", proj, a.Name)
	if got != want {
		t.Fatalf("linked label = %q, want %q", got, want)
	}
}

// TestLinkedLabelHomeAliasesGlobal verifies the home-directory invariant for
// `list`: scanned from cwd == HOME, a global link renders as "global: …" only —
// never also as "local", since each agent's local folder there is its global
// folder.
func TestLinkedLabelHomeAliasesGlobal(t *testing.T) {
	fakeHome := t.TempDir()
	t.Setenv("HOME", fakeHome)
	t.Setenv("USERPROFILE", fakeHome)

	home := t.TempDir()
	if err := store.EnsureHome(home); err != nil {
		t.Fatalf("EnsureHome: %v", err)
	}
	const id = "demo"
	skillDir := store.SkillDir(home, id)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("mkdir skill: %v", err)
	}

	agents := config.Default().AllAgents()
	a := agents[0]

	folder, _ := agentdir.SkillsFolder(a, agentdir.Global, "")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Skipf("cannot create global folder %s: %v", folder, err)
	}
	if err := os.Symlink(skillDir, filepath.Join(folder, id)); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	got := linkedLabel(home, state.SkillEntry{ID: id}, agents, nil, fakeHome)
	want := "global: " + a.Name
	if got != want {
		t.Fatalf("label from home = %q, want %q (a global link must not appear as local)", got, want)
	}
}

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
	skillDir := store.SkillDir(home, id)
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

// TestUpstreamStatus exercises the full revision-comparison path against a real
// local git repo used as a clone source.
func TestUpstreamStatus(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	repo := t.TempDir()
	gitInit(t, repo)
	writeFile(t, filepath.Join(repo, "myskill", "SKILL.md"), "---\nname: My Skill\n---\nbody\n")
	gitCommit(t, repo, "init")

	ctx := context.Background()
	branch := defaultBranch(t, repo)

	// Recorded revision matches HEAD -> up-to-date.
	cur := subtreeSHAViaGit(t, repo, branch, "myskill")
	e := state.SkillEntry{
		Kind:     state.KindGit,
		Source:   repo,
		Path:     "myskill",
		Ref:      branch,
		Revision: cur,
	}
	if got := upstreamStatus(ctx, e); got != statusUpToDate {
		t.Fatalf("matching revision: status = %q, want %q", got, statusUpToDate)
	}

	// A stale recorded revision -> update available.
	stale := e
	stale.Revision = "0000000000000000000000000000000000000000"
	if got := upstreamStatus(ctx, stale); got != statusUpdateAvailable {
		t.Fatalf("stale revision: status = %q, want %q", got, statusUpdateAvailable)
	}

	// A subdir that does not exist upstream -> untracked.
	gone := e
	gone.Path = "nope"
	if got := upstreamStatus(ctx, gone); got != statusUntracked {
		t.Fatalf("missing subdir: status = %q, want %q", got, statusUntracked)
	}
}

// --- git test helpers ---

func gitInit(t *testing.T, dir string) {
	t.Helper()
	runGitT(t, dir, "init", "-q")
	runGitT(t, dir, "config", "user.email", "test@example.com")
	runGitT(t, dir, "config", "user.name", "test")
}

func gitCommit(t *testing.T, dir, msg string) {
	t.Helper()
	runGitT(t, dir, "add", "-A")
	runGitT(t, dir, "commit", "-q", "-m", msg)
}

func defaultBranch(t *testing.T, dir string) string {
	t.Helper()
	out := runGitT(t, dir, "rev-parse", "--abbrev-ref", "HEAD")
	return out
}

func subtreeSHAViaGit(t *testing.T, dir, ref, sub string) string {
	t.Helper()
	out := runGitT(t, dir, "ls-tree", ref, sub)
	// "<mode> tree <sha>\t<path>"
	fields := strings.Fields(out)
	if len(fields) < 3 || fields[1] != "tree" {
		t.Fatalf("unexpected ls-tree output: %q", out)
	}
	return fields[2]
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func runGitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}
