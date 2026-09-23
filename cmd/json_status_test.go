package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ultrakorne/skillm/internal/protocol"
	"github.com/ultrakorne/skillm/internal/status"
)

// statusOf returns skill id's status in data ("" when it has no row).
func statusOf(data protocol.StatusData, id string) string {
	if s, ok := data.Skill(id); ok {
		return s.Status
	}
	return ""
}

// TestJSONRefreshStatus drives the badge cycle a GUI runs: status before any
// refresh, a refresh that finds an update, --if-due that does nothing, then
// update and uninstall clearing the badge without another check.
func TestJSONRefreshStatus(t *testing.T) {
	needGit(t)
	repo, url := initSkillRepo(t)
	e := env{home: t.TempDir(), userDir: t.TempDir(), bin: skillmBinary(t)}
	e.run(t, "install", url, "alpha", "beta", "--global")

	// Never refreshed: empty and stale, and no git needed to say so.
	noGit := []string{"PATH=" + t.TempDir()}
	stdout, stderr, ok := e.runJSON(t, e.userDir, noGit, "status", "--json")
	if !ok || stderr != "" {
		t.Fatalf("status --json without git: ok=%v stderr=%q stdout=%q", ok, stderr, stdout)
	}
	var never protocol.StatusData
	decodeData(t, decodeDoc(t, stdout), &never)
	if !never.Stale || never.Badge || never.Self != nil || len(never.Skills) != 0 || !never.CheckedAt.IsZero() {
		t.Fatalf("never refreshed = %+v", never)
	}

	writeSkillMD(t, filepath.Join(repo, "beta"), "beta", "beta body v2")
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-q", "-m", "change beta")

	var refreshed protocol.RefreshData
	e.jsonOK(t, e.userDir, &refreshed, "refresh", "--json")
	if !refreshed.Refreshed || refreshed.Stale || !refreshed.Badge || refreshed.Updates != 1 ||
		statusOf(refreshed.StatusData, "alpha") != status.SkillUpToDate ||
		statusOf(refreshed.StatusData, "beta") != status.SkillUpdateAvailable {
		t.Fatalf("refresh = %+v", refreshed)
	}
	// A test binary is a source build: no release is looked up.
	if refreshed.Self == nil || refreshed.Self.Method != "dev" || refreshed.Self.Available || len(refreshed.Errors) != 0 {
		t.Fatalf("refresh self = %+v errors = %+v", refreshed.Self, refreshed.Errors)
	}
	if !refreshed.NextDueAt.Equal(refreshed.CheckedAt.Add(24 * time.Hour)) {
		t.Fatalf("next_due_at %v, checked_at %v", refreshed.NextDueAt, refreshed.CheckedAt)
	}
	if _, err := os.Stat(filepath.Join(e.home, status.FileName)); err != nil {
		t.Fatalf("cache not written: %v", err)
	}

	// Not due: the cache as it was.
	var again protocol.RefreshData
	e.jsonOK(t, e.userDir, &again, "refresh", "--if-due", "--json")
	if again.Refreshed || !again.CheckedAt.Equal(refreshed.CheckedAt) || !again.Badge {
		t.Fatalf("refresh --if-due = %+v", again)
	}

	// The update clears the badge without another check.
	var upd protocol.UpdateData
	e.jsonOK(t, e.userDir, &upd, "update", "beta", "--json")
	var st protocol.StatusData
	e.jsonOK(t, e.userDir, &st, "status", "--json")
	if st.Badge || st.Updates != 0 || statusOf(st, "beta") != status.SkillUpToDate || !st.CheckedAt.Equal(refreshed.CheckedAt) {
		t.Fatalf("status after update = %+v", st)
	}

	// An uninstall drops its row.
	var un protocol.UninstallData
	e.jsonOK(t, e.userDir, &un, "uninstall", "alpha", "--yes", "--json")
	e.jsonOK(t, e.userDir, &st, "status", "--json")
	if statusOf(st, "alpha") != "" || statusOf(st, "beta") != status.SkillUpToDate {
		t.Fatalf("status after uninstall = %+v", st)
	}

	// An install from a source adds an up-to-date row.
	e.run(t, "install", url, "gamma", "--global")
	e.jsonOK(t, e.userDir, &st, "status", "--json")
	if statusOf(st, "gamma") != status.SkillUpToDate {
		t.Fatalf("status after install = %+v", st)
	}

	// The terminal renders the same cache.
	out := e.run(t, "status")
	if !strings.Contains(out, "Checked ") || !strings.Contains(out, "Everything is up to date.") {
		t.Fatalf("skillm status:\n%s", out)
	}
	out = e.run(t, "refresh", "--if-due")
	if !strings.Contains(out, "No check due before ") {
		t.Fatalf("skillm refresh --if-due:\n%s", out)
	}
}

// TestJSONRefreshIfDueDisabled: with scheduled checks off, --if-due never
// checks, even when no refresh ever ran.
func TestJSONRefreshIfDueDisabled(t *testing.T) {
	needGit(t)
	e := env{home: t.TempDir(), userDir: t.TempDir(), bin: skillmBinary(t)}
	var cfg protocol.ConfigData
	e.jsonOK(t, e.userDir, &cfg, "config", "set", "refresh.enabled", "false", "--json")
	var res protocol.RefreshData
	e.jsonOK(t, e.userDir, &res, "refresh", "--if-due", "--json")
	if res.Refreshed || !res.Stale {
		t.Fatalf("refresh --if-due with checks off = %+v", res)
	}
	if _, err := os.Stat(filepath.Join(e.home, status.FileName)); !os.IsNotExist(err) {
		t.Fatalf("cache written with checks off: %v", err)
	}
	if out := e.run(t, "refresh", "--if-due"); !strings.Contains(out, "scheduled checks are off") {
		t.Fatalf("skillm refresh --if-due:\n%s", out)
	}
}
