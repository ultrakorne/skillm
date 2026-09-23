package core

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ultrakorne/skillm/internal/gitx"
	"github.com/ultrakorne/skillm/internal/state"
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
			if got := SourceLabel(tc.e.Kind, tc.e.Source, tc.e.Path); got != tc.want {
				t.Fatalf("SourceLabel = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestCheckOne exercises the full revision-comparison path against a real
// local git repo used as a clone source, including the two failure statuses:
// a subdir missing upstream (untracked) and an unreadable source (error).
func TestCheckOne(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	repo := t.TempDir()
	gitInit(t, repo)
	writeFile(t, filepath.Join(repo, "myskill", "SKILL.md"), "---\nname: My Skill\n---\nbody\n")
	gitCommit(t, repo, "init")

	ctx := context.Background()
	branch := defaultBranch(t, repo)

	// Recorded revision matches HEAD -> up to date.
	cur := subtreeSHAViaGit(t, repo, branch, "myskill")
	e := state.SkillEntry{
		ID:       "myskill",
		Kind:     state.KindGit,
		Source:   repo,
		Path:     "myskill",
		Ref:      branch,
		Revision: cur,
	}
	if got := checkOne(ctx, e); got.Status != StatusUpToDate || got.UpstreamRev != cur || got.Err != nil {
		t.Fatalf("matching revision: got %+v, want up to date at %s", got, cur)
	}

	// A stale recorded revision -> update available, with both revisions.
	stale := e
	stale.Revision = "0000000000000000000000000000000000000000"
	got := checkOne(ctx, stale)
	if got.Status != StatusUpdateAvailable || got.UpstreamRev != cur || got.InstalledRev != stale.Revision {
		t.Fatalf("stale revision: got %+v, want update available", got)
	}

	// A subdir that does not exist upstream -> untracked, carrying the cause.
	gone := e
	gone.Path = "nope"
	got = checkOne(ctx, gone)
	var nf *gitx.NotFoundError
	if got.Status != StatusUntracked || !errors.As(got.Err, &nf) {
		t.Fatalf("missing subdir: got %+v, want untracked with a NotFoundError", got)
	}

	// A source that cannot be read -> error, no longer collapsed into untracked.
	lost := e
	lost.Source = filepath.Join(t.TempDir(), "no-such-repo")
	if got := checkOne(ctx, lost); got.Status != StatusError || got.Err == nil {
		t.Fatalf("unreadable source: got %+v, want error with a cause", got)
	}

	// A local skill has no upstream.
	if got := checkOne(ctx, state.SkillEntry{ID: "l", Kind: state.KindLocal}); got.Status != StatusLocal {
		t.Fatalf("local skill: got %+v, want local", got)
	}
}

// TestInterrupted verifies a failed lookup on a cancelled ctx counts as
// interrupted even when its error does not wrap ctx's, while a real answer
// (a missing subdir) and a failure on a live ctx do not.
func TestInterrupted(t *testing.T) {
	live := context.Background()
	done, cancel := context.WithCancel(context.Background())
	cancel()
	plain := errors.New("gitx: could not determine default branch")
	tests := []struct {
		name string
		ctx  context.Context
		cs   CheckedSkill
		want bool
	}{
		{"error after cancel, not wrapping ctx", done, CheckedSkill{Status: StatusError, Err: plain}, true},
		{"error after cancel, wrapping ctx", done, CheckedSkill{Status: StatusError, Err: context.Canceled}, true},
		{"untracked after cancel", done, CheckedSkill{Status: StatusUntracked, Err: &gitx.NotFoundError{}}, false},
		{"up to date after cancel", done, CheckedSkill{Status: StatusUpToDate}, false},
		{"error on a live ctx", live, CheckedSkill{Status: StatusError, Err: plain}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := interrupted(tc.ctx, tc.cs); got != tc.want {
				t.Fatalf("interrupted = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestCheckEventError verifies a failed lookup's event names its cause
// instead of calling the skill untracked.
func TestCheckEventError(t *testing.T) {
	e := state.SkillEntry{ID: "x", Kind: state.KindGit, Source: "https://example.com/r", Path: "x"}
	ev := checkEvent(e, CheckedSkill{Status: StatusError, Err: errors.New("boom")})
	want := "x: could not read upstream (https://example.com/r//x): boom"
	if ev.Code != string(StatusError) || ev.Level != LevelError || ev.Text != want {
		t.Fatalf("event = %+v, want code error with text %q", ev, want)
	}
	ev = checkEvent(e, CheckedSkill{Status: StatusUntracked, Err: &gitx.NotFoundError{}})
	if ev.Text != UntrackedText("x", "https://example.com/r//x") {
		t.Fatalf("untracked text = %q", ev.Text)
	}
}

// recorder is a Reporter that keeps every Event.
type recorder struct {
	mu     sync.Mutex
	events []Event
}

func (r *recorder) Event(ev Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, ev)
}

// TestCheckEvents verifies Check's event stream: one EventBatch naming every
// skill in Registry order before anything else, then an ItemStart and an
// ItemDone per skill carrying its index, code and the CLI's line.
func TestCheckEvents(t *testing.T) {
	home := t.TempDir()
	st := &state.State{Skills: []state.SkillEntry{
		{ID: "one", Kind: state.KindLocal, Source: "/src/one"},
		{ID: "two", Kind: state.KindLocal, Source: "/src/two"},
	}}
	if err := state.Save(home, st); err != nil {
		t.Fatalf("save state: %v", err)
	}

	rec := &recorder{}
	res, err := Check(context.Background(), Options{Home: home}, rec)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(res.Skills) != 2 || res.Skills[0].ID != "one" || res.Skills[1].Status != StatusLocal {
		t.Fatalf("result = %+v, want both skills local in Registry order", res.Skills)
	}

	if len(rec.events) != 5 {
		t.Fatalf("got %d events, want batch + 2×(start, done): %+v", len(rec.events), rec.events)
	}
	if b := rec.events[0]; b.Type != EventBatch || strings.Join(b.Items, ",") != "one,two" {
		t.Fatalf("first event = %+v, want a batch of one,two", b)
	}
	started := map[int]bool{}
	for _, ev := range rec.events[1:] {
		switch ev.Type {
		case EventItemStart:
			started[ev.Index] = true
		case EventItemDone:
			if !started[ev.Index] {
				t.Fatalf("ItemDone for %d before its ItemStart", ev.Index)
			}
			want := st.Skills[ev.Index].ID
			if ev.Skill != want || ev.Code != string(StatusLocal) || ev.Level != LevelWarn ||
				!strings.HasPrefix(ev.Text, want+": local skill") {
				t.Fatalf("ItemDone = %+v, want %s's local-skill line", ev, want)
			}
		default:
			t.Fatalf("unexpected event %+v", ev)
		}
	}
}

// TestCheckEmptyHome verifies a Home with no skills reports nothing.
func TestCheckEmptyHome(t *testing.T) {
	rec := &recorder{}
	res, err := Check(context.Background(), Options{Home: t.TempDir()}, rec)
	if err != nil || len(res.Skills) != 0 || len(rec.events) != 0 {
		t.Fatalf("empty Home: res=%+v err=%v events=%+v, want nothing", res, err, rec.events)
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
	return runGitT(t, dir, "rev-parse", "--abbrev-ref", "HEAD")
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
