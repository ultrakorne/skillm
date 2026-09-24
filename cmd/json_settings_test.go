package cmd

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ultrakorne/skillm/internal/protocol"
)

// These tests drive the commands a GUI sets itself up with — source inspect,
// agent ls/set and config get/set — and install's --commit, with --json.

// TestJSONSourceInspectThenInstall: the Add Skill flow. Inspect a repo, then
// install exactly the inspected commit; once the branch moves on, the same
// install fails with commit_mismatch and writes nothing.
func TestJSONSourceInspectThenInstall(t *testing.T) {
	needGit(t)
	repo, url := initSkillRepo(t)
	e := env{home: t.TempDir(), userDir: t.TempDir(), bin: skillmBinary(t)}
	head := runGit(t, repo, "rev-parse", "HEAD")

	var insp protocol.InspectData
	e.jsonOK(t, e.userDir, &insp, "source", "inspect", url, "--json")
	if insp.Kind != "git" || insp.Ref != "main" || insp.Commit != head {
		t.Fatalf("inspect = %+v, want git at main@%s", insp, head)
	}
	var ids []string
	for _, s := range insp.Skills {
		ids = append(ids, s.ID)
		if s.Name != s.ID || s.Description != s.ID+" skill" || s.Path != s.ID {
			t.Errorf("inspected skill = %+v", s)
		}
	}
	if strings.Join(ids, ",") != "alpha,beta,gamma" {
		t.Fatalf("inspected ids = %v", ids)
	}
	// Inspecting writes nothing to Home.
	if _, err := os.Stat(filepath.Join(e.home, "state.toml")); !os.IsNotExist(err) {
		t.Fatalf("inspect wrote the Registry: %v", err)
	}

	var inst protocol.InstallData
	e.jsonOK(t, e.userDir, &inst, "install", url, "alpha", "--ref", insp.Ref, "--commit", insp.Commit[:12], "--global", "--json")
	if len(inst.Skills) != 1 || inst.Skills[0].ID != "alpha" {
		t.Fatalf("install = %+v", inst)
	}
	// The branch, not the commit, is what an update follows.
	if st := loadState(t, e); st.Skills[0].Ref != "main" {
		t.Fatalf("recorded ref = %q, want main", st.Skills[0].Ref)
	}

	writeSkillMD(t, filepath.Join(repo, "beta"), "beta", "beta v2")
	runGit(t, repo, "commit", "-q", "-am", "beta v2")
	perr := e.jsonFail(t, e.userDir, "install", url, "beta", "--ref", "main", "--commit", insp.Commit, "--global", "--json")
	if perr.Code != protocol.CodeCommitMismatch {
		t.Fatalf("stale --commit = %+v, want commit_mismatch", perr)
	}
	if _, err := os.Lstat(filepath.Join(e.userDir, ".agents", "skills", "beta")); !os.IsNotExist(err) {
		t.Fatalf("a commit_mismatch install wrote beta: %v", err)
	}

	// --commit needs a git Source and a commit SHA; a malformed value is a
	// usage error, not a commit_mismatch that would send a GUI re-inspecting.
	for _, args := range [][]string{
		{"install", url, "alpha", "--commit", "main", "--global", "--json"},
		{"install", url, "alpha", "--commit", "abc", "--global", "--json"},
		{"install", url, "alpha", "--commit", "  " + insp.Commit[:8], "--global", "--json"},
		{"install", url, "alpha", "--commit", insp.Commit[:8] + "XYZ", "--global", "--json"},
		{"install", "alpha", "--commit", head, "--global", "--json"},
		{"install", filepath.Join(repo, "gamma"), "gamma", "--commit", head, "--global", "--json"},
	} {
		if perr := e.jsonFail(t, e.userDir, args...); perr.Code != protocol.CodeUsage {
			t.Errorf("skillm %v = %+v, want usage", args, perr)
		}
	}
}

func TestJSONSourceInspectLocal(t *testing.T) {
	needGit(t)
	e := env{home: t.TempDir(), userDir: t.TempDir(), bin: skillmBinary(t)}
	dir := t.TempDir()
	writeSkillMD(t, filepath.Join(dir, "notes"), "notes", "notes body")

	var insp protocol.InspectData
	e.jsonOK(t, e.userDir, &insp, "source", "inspect", dir, "--json")
	if insp.Kind != "local" || insp.Ref != "" || insp.Commit != "" || len(insp.Skills) != 1 ||
		insp.Skills[0].ID != "notes" || !filepath.IsAbs(insp.Skills[0].Path) {
		t.Fatalf("local inspect = %+v", insp)
	}
	if perr := e.jsonFail(t, e.userDir, "source", "inspect", dir, "--ref", "main", "--json"); perr.Code != protocol.CodeUsage {
		t.Errorf("--ref on a local source = %+v, want usage", perr)
	}
	if perr := e.jsonFail(t, e.userDir, "source", "inspect", filepath.Join(dir, "missing"), "--json"); perr.Code != protocol.CodeError {
		t.Errorf("a missing source = %+v", perr)
	}

	// The terminal lists the skills.
	out := e.run(t, "source", "inspect", dir)
	if !strings.Contains(out, "notes — notes skill") || !strings.Contains(out, "(local)") {
		t.Fatalf("source inspect output = %q", out)
	}
}

func TestJSONAgentLsSet(t *testing.T) {
	needGit(t)
	_, url := initSkillRepo(t)
	e := env{home: t.TempDir(), userDir: t.TempDir(), bin: skillmBinary(t)}
	e.jsonOK(t, e.userDir, &protocol.InstallData{}, "install", url, "alpha", "--global", "--json")
	claudeLink := claudeGlobalLink(e, "alpha")

	ls := func() map[string]bool {
		t.Helper()
		var data protocol.AgentsData
		e.jsonOK(t, e.userDir, &data, "agent", "ls", "--json")
		out := map[string]bool{}
		for _, a := range data.Agents {
			out[a.Name] = a.Enabled
		}
		return out
	}
	if got := ls(); !got["claude"] || !got["agents"] || len(got) != 2 {
		t.Fatalf("agent ls = %v", got)
	}

	// Disabling removes links, so JSON mode wants the GUI's confirmation.
	if perr := e.jsonFail(t, e.userDir, "agent", "set", "--disable", "claude", "--json"); perr.Code != protocol.CodeUsage {
		t.Fatalf("disable without --yes = %+v, want usage", perr)
	}
	var set protocol.AgentsSetData
	e.jsonOK(t, e.userDir, &set, "agent", "set", "--disable", "claude", "--yes", "--json")
	if strings.Join(set.Enabled, ",") != "agents" || len(set.Changes) != 1 ||
		set.Changes[0].Name != "claude" || set.Changes[0].Enabled || !slices.Contains(set.Changes[0].Skills, "alpha") {
		t.Fatalf("disable claude = %+v", set)
	}
	if _, err := os.Lstat(claudeLink); !os.IsNotExist(err) {
		t.Fatalf("claude's link survived the disable: %v", err)
	}
	if got := ls(); got["claude"] || !got["agents"] {
		t.Fatalf("agent ls after disable = %v", got)
	}

	cases := []struct {
		name string
		args []string
		code string
	}{
		{"unknown agent", []string{"agent", "set", "--enable", "nope", "--json"}, protocol.CodeUnknownAgent},
		{"no agent left", []string{"agent", "set", "--disable", "agents", "--yes", "--json"}, protocol.CodeNoAgentEnabled},
		{"nothing asked", []string{"agent", "set", "--json"}, protocol.CodeUsage},
		{"both ways", []string{"agent", "set", "--enable", "claude", "--disable", "claude", "--yes", "--json"}, protocol.CodeUsage},
	}
	for _, tc := range cases {
		if perr := e.jsonFail(t, e.userDir, tc.args...); perr.Code != tc.code {
			t.Errorf("%s = %+v, want %s", tc.name, perr, tc.code)
		}
	}

	// Enabling needs no confirmation and relinks the skill.
	set = protocol.AgentsSetData{}
	e.jsonOK(t, e.userDir, &set, "agent", "set", "--enable", "claude", "--json")
	if len(set.Changes) != 1 || !set.Changes[0].Enabled || !slices.Contains(set.Changes[0].Places, "global") {
		t.Fatalf("enable claude = %+v", set)
	}
	if _, err := os.Lstat(claudeLink); err != nil {
		t.Fatalf("claude's link not restored: %v", err)
	}
	// Asking for the current state changes nothing.
	set = protocol.AgentsSetData{}
	e.jsonOK(t, e.userDir, &set, "agent", "set", "--enable", "claude,agents", "--json")
	if len(set.Changes) != 0 || strings.Join(set.Enabled, ",") != "agents,claude" {
		t.Fatalf("no-op set = %+v", set)
	}

	// Without --json the same commands print for a person.
	if out := e.run(t, "agent", "ls"); !strings.Contains(out, "claude") || !strings.Contains(out, "enabled") {
		t.Fatalf("agent ls output = %q", out)
	}
	if out := e.run(t, "agent", "set", "--disable", "claude"); !strings.Contains(out, "enabled agents: agents") {
		t.Fatalf("agent set output = %q", out)
	}
}

func TestJSONConfigGetSet(t *testing.T) {
	e := env{home: t.TempDir(), userDir: t.TempDir(), bin: skillmBinary(t)}
	// Settings need no git.
	noGit := []string{"PATH=" + t.TempDir()}

	get := func() protocol.ConfigData {
		t.Helper()
		stdout, stderr, ok := e.runJSON(t, e.userDir, noGit, "config", "get", "--json")
		if !ok || stderr != "" {
			t.Fatalf("config get: ok=%v stderr=%q stdout=%q", ok, stderr, stdout)
		}
		var data protocol.ConfigData
		decodeData(t, decodeDoc(t, stdout), &data)
		return data
	}
	if got := get(); !got.Refresh.Enabled || got.Refresh.IntervalHours != 24 {
		t.Fatalf("defaults = %+v", got)
	}
	// Reading writes nothing.
	if _, err := os.Stat(filepath.Join(e.home, "config.toml")); !os.IsNotExist(err) {
		t.Fatalf("config get wrote config.toml: %v", err)
	}

	stdout, stderr, ok := e.runJSON(t, e.userDir, noGit, "config", "set", "refresh.interval_hours", "12", "--json")
	if !ok || stderr != "" {
		t.Fatalf("config set: ok=%v stderr=%q stdout=%q", ok, stderr, stdout)
	}
	var set protocol.ConfigData
	decodeData(t, decodeDoc(t, stdout), &set)
	if !set.Refresh.Enabled || set.Refresh.IntervalHours != 12 {
		t.Fatalf("config set data = %+v", set)
	}
	e.jsonOK(t, e.userDir, &set, "config", "set", "refresh.enabled", "false", "--json")
	if got := get(); got.Refresh.Enabled || got.Refresh.IntervalHours != 12 {
		t.Fatalf("after set = %+v", got)
	}
	// The agents survive the rewrite.
	var agents protocol.AgentsData
	e.jsonOK(t, e.userDir, &agents, "agent", "ls", "--json")
	if len(agents.Agents) != 2 {
		t.Fatalf("agents after config set = %+v", agents)
	}

	cases := []struct {
		args []string
		code string
	}{
		{[]string{"config", "set", "refresh.bogus", "1", "--json"}, protocol.CodeUnknownKey},
		{[]string{"config", "get", "refresh.bogus", "--json"}, protocol.CodeUnknownKey},
		{[]string{"config", "set", "refresh.enabled", "maybe", "--json"}, protocol.CodeInvalidValue},
		{[]string{"config", "set", "refresh.interval_hours", "0", "--json"}, protocol.CodeInvalidValue},
		{[]string{"config", "set", "refresh.enabled", "--json"}, protocol.CodeUsage},
	}
	for _, tc := range cases {
		if perr := e.jsonFail(t, e.userDir, tc.args...); perr.Code != tc.code {
			t.Errorf("skillm %v = %+v, want %s", tc.args, perr, tc.code)
		}
	}
	if got := get(); got.Refresh.Enabled || got.Refresh.IntervalHours != 12 {
		t.Fatalf("a refused set changed the settings: %+v", got)
	}

	// The terminal prints a single value bare, all of them as key = value.
	if out := e.run(t, "config", "get", "refresh.interval_hours"); out != "12\n" {
		t.Fatalf("config get key = %q", out)
	}
	if out := e.run(t, "config", "get"); out != "refresh.enabled = false\nrefresh.interval_hours = 12\n" {
		t.Fatalf("config get = %q", out)
	}
}

// TestJSONGroupCommandsRefused: a command that only groups others prints its
// help, so JSON mode refuses it; the new commands are capabilities.
func TestJSONGroupCommandsRefused(t *testing.T) {
	e := env{home: t.TempDir(), userDir: t.TempDir(), bin: skillmBinary(t)}
	for _, args := range [][]string{{"source", "--json"}, {"config", "--json"}, {"agent", "--json"}, {"completion", "--json"}} {
		if perr := e.jsonFail(t, e.userDir, args...); perr.Code != protocol.CodeJSONUnsupported {
			t.Errorf("skillm %v = %+v, want json_unsupported", args, perr)
		}
	}
	var v protocol.VersionData
	e.jsonOK(t, e.userDir, &v, "version", "--json")
	for _, c := range []string{"agent ls", "agent set", "config get", "config set", "source inspect"} {
		if !slices.Contains(v.Capabilities, c) {
			t.Errorf("capabilities %v lack %q", v.Capabilities, c)
		}
	}
	if slices.Contains(v.Capabilities, "agent") || slices.Contains(v.Capabilities, "source") || slices.Contains(v.Capabilities, "config") {
		t.Errorf("capabilities %v list a group command", v.Capabilities)
	}
}
