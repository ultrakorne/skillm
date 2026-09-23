package cmd

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/ultrakorne/skillm/internal/protocol"
)

// These tests drive install, update, import, uninstall and upgrade with
// --json, as a GUI does: no prompt, only protocol output on stdout, and
// nothing on stderr.

// needGit skips the test when git is not on PATH.
func needGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
}

// jsonOK runs a JSON command that must succeed and decodes its data into v.
func (e env) jsonOK(t *testing.T, dir string, v any, args ...string) {
	t.Helper()
	stdout, stderr, ok := e.runJSON(t, dir, nil, args...)
	if !ok || stderr != "" {
		t.Fatalf("skillm %s: ok=%v stderr=%q stdout=%q", strings.Join(args, " "), ok, stderr, stdout)
	}
	decodeData(t, decodeDoc(t, stdout), v)
}

// jsonFail runs a JSON command that must fail and returns its error.
func (e env) jsonFail(t *testing.T, dir string, args ...string) *protocol.Error {
	t.Helper()
	stdout, stderr, ok := e.runJSON(t, dir, nil, args...)
	if ok || stderr != "" {
		t.Fatalf("skillm %s: ok=%v stderr=%q stdout=%q, want a failure", strings.Join(args, " "), ok, stderr, stdout)
	}
	doc := decodeDoc(t, stdout)
	if doc.Error == nil || string(doc.Data) != "null" {
		t.Fatalf("skillm %s: envelope %s, want an error", strings.Join(args, " "), stdout)
	}
	return doc.Error
}

// decodeStream splits an --events stdout into its event lines and the
// result envelope on its last line.
func decodeStream(t *testing.T, stdout string) ([]protocol.EventLine, jsonEnvelope) {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
	var events []protocol.EventLine
	for _, line := range lines[:len(lines)-1] {
		var ev protocol.EventLine
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("decode event %q: %v", line, err)
		}
		if ev.Type != protocol.TypeEvent || ev.SchemaVersion != protocol.SchemaVersion {
			t.Fatalf("bad event line %q", line)
		}
		events = append(events, ev)
	}
	var res jsonEnvelope
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &res); err != nil {
		t.Fatalf("decode result %q: %v", lines[len(lines)-1], err)
	}
	if res.Type != protocol.TypeResult {
		t.Fatalf("last line is not the result: %q", lines[len(lines)-1])
	}
	return events, res
}

// itemDone returns the item_done events by skill id.
func itemDone(events []protocol.EventLine) map[string]protocol.EventLine {
	out := map[string]protocol.EventLine{}
	for _, ev := range events {
		if ev.Event == "item_done" {
			out[ev.SkillID] = ev
		}
	}
	return out
}

// TestJSONInstallRefusesQuestions: install --json never asks, so a run
// missing what a question would supply fails with code "usage" (or
// "not_installed") before anything is fetched or written.
func TestJSONInstallRefusesQuestions(t *testing.T) {
	needGit(t)
	_, url := initSkillRepo(t)
	e := env{home: t.TempDir(), userDir: t.TempDir(), bin: skillmBinary(t)}

	cases := []struct {
		name string
		args []string
		code string
	}{
		{"no scope", []string{"install", url, "alpha", "--json"}, protocol.CodeUsage},
		{"no ids from a source", []string{"install", url, "--json", "--global"}, protocol.CodeUsage},
		{"no ids at all", []string{"install", "--json", "--global"}, protocol.CodeUsage},
		{"two scopes", []string{"install", url, "alpha", "--json", "--global", "--project", e.userDir}, protocol.CodeUsage},
		{"--as without a source", []string{"install", "alpha", "--as", "x", "--json", "--global"}, protocol.CodeUsage},
		{"unknown id", []string{"install", "alpha", "--json", "--global"}, protocol.CodeNotInstalled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			perr := e.jsonFail(t, e.userDir, tc.args...)
			if perr.Code != tc.code {
				t.Fatalf("code = %q (%s), want %q", perr.Code, perr.Message, tc.code)
			}
		})
	}
	if perr := e.jsonFail(t, e.userDir, "install", "alpha", "--json", "--global"); perr.SkillID != "alpha" {
		t.Errorf("not_installed skill_id = %q, want alpha", perr.SkillID)
	}
	if perr := e.jsonFail(t, e.userDir, "install", url, "alpha", "--json", "--project", filepath.Join(e.userDir, "missing")); perr.Code != protocol.CodeError {
		t.Errorf("--project at a missing directory = %+v", perr)
	}
	if _, err := os.Stat(filepath.Join(e.userDir, ".agents")); !os.IsNotExist(err) {
		t.Fatalf("a refused install wrote %s: %v", filepath.Join(e.userDir, ".agents"), err)
	}

	// --all with nothing installed is an install of nothing.
	var none protocol.InstallData
	e.jsonOK(t, e.userDir, &none, "install", "--all", "--json", "--global")
	if none.Scope != "global" || none.Root != "" || len(none.Skills) != 0 {
		t.Fatalf("install --all of nothing = %+v", none)
	}
}

// TestJSONInstallUpdateUninstall drives a GUI's flow: install from a source
// with events, install into a project by path, the foreign-files retry,
// update with events, and uninstall with its project confirmation.
func TestJSONInstallUpdateUninstall(t *testing.T) {
	needGit(t)
	repo, url := initSkillRepo(t)
	project := t.TempDir()
	e := env{home: t.TempDir(), userDir: t.TempDir(), bin: skillmBinary(t)}

	// Global install from a source, streamed: a batch, then a start and a
	// done per skill, then the result.
	stdout, stderr, ok := e.runJSON(t, e.userDir, nil, "install", url, "alpha", "beta", "--global", "--json", "--events")
	if !ok || stderr != "" {
		t.Fatalf("install --events: ok=%v stderr=%q stdout=%q", ok, stderr, stdout)
	}
	events, res := decodeStream(t, stdout)
	if events[0].Event != "batch" || !slices.Equal(events[0].Items, []string{"alpha", "beta"}) {
		t.Fatalf("first event = %+v, want the batch", events[0])
	}
	done := itemDone(events)
	for i, id := range []string{"alpha", "beta"} {
		if ev := done[id]; ev.Code != "installed" || ev.Index == nil || *ev.Index != i {
			t.Errorf("item_done for %s = %+v", id, ev)
		}
	}
	var global protocol.InstallData
	decodeData(t, res, &global)
	if global.Scope != "global" || len(global.Skills) != 2 || global.Skills[0].Action != protocol.ActionInstalled {
		t.Fatalf("global install = %+v", global)
	}
	if _, err := os.Stat(filepath.Join(e.userDir, ".agents", "skills", "alpha", "SKILL.md")); err != nil {
		t.Fatalf("global copy: %v", err)
	}

	// A Local install by --project, run from elsewhere (a GUI's cwd).
	var local protocol.InstallData
	e.jsonOK(t, e.userDir, &local, "install", url, "alpha", "--project", project, "--json")
	if local.Scope != "local" || local.Root != project || len(local.Skills) != 1 {
		t.Fatalf("project install = %+v", local)
	}
	if _, err := os.Stat(filepath.Join(project, ".agents", "skills", "alpha", "SKILL.md")); err != nil {
		t.Fatalf("project copy: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, "skills-lock.json")); err != nil {
		t.Fatalf("project lockfile: %v", err)
	}

	// Files skillm did not create: foreign_files with the path and nothing
	// written; then --skip-foreign skips the skill, and --yes overwrites it.
	other := t.TempDir()
	slot := filepath.Join(other, ".agents", "skills", "beta")
	mine := filepath.Join(slot, "MINE.md")
	if err := os.MkdirAll(slot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mine, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	perr := e.jsonFail(t, e.userDir, "install", url, "beta", "--project", other, "--json")
	if perr.Code != protocol.CodeForeignFiles || !slices.Equal(perr.Paths, []string{slot}) {
		t.Fatalf("foreign files = %+v, want foreign_files naming %s", perr, slot)
	}
	var skipped protocol.InstallData
	e.jsonOK(t, e.userDir, &skipped, "install", url, "beta", "--project", other, "--json", "--skip-foreign")
	if len(skipped.Skills) != 1 || skipped.Skills[0].Action != protocol.ActionSkipped {
		t.Fatalf("--skip-foreign = %+v", skipped)
	}
	if _, err := os.Stat(mine); err != nil {
		t.Fatalf("a skipped skill's files must stay: %v", err)
	}
	var overwritten protocol.InstallData
	e.jsonOK(t, e.userDir, &overwritten, "install", url, "beta", "--project", other, "--json", "--yes")
	if len(overwritten.Skills) != 1 || overwritten.Skills[0].Action != protocol.ActionOverwritten {
		t.Fatalf("--yes = %+v", overwritten)
	}

	// Update, streamed: alpha changed upstream, beta did not.
	writeSkillMD(t, filepath.Join(repo, "alpha"), "alpha", "alpha body v2")
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-q", "-m", "change alpha")
	stdout, stderr, ok = e.runJSON(t, e.userDir, nil, "update", "--json", "--events")
	if !ok || stderr != "" {
		t.Fatalf("update --events: ok=%v stderr=%q stdout=%q", ok, stderr, stdout)
	}
	events, res = decodeStream(t, stdout)
	if ev := itemDone(events)["alpha"]; ev.Code != "updated" {
		t.Errorf("alpha item_done = %+v, want updated", ev)
	}
	var upd protocol.UpdateData
	decodeData(t, res, &upd)
	outcomes := map[string]string{}
	for _, s := range upd.Skills {
		outcomes[s.ID] = s.Outcome
	}
	if outcomes["alpha"] != "updated" || outcomes["beta"] != "up_to_date" || upd.Updated != 1 {
		t.Fatalf("update = %+v", upd)
	}
	body, err := os.ReadFile(filepath.Join(project, ".agents", "skills", "alpha", "SKILL.md"))
	if err != nil || !strings.Contains(string(body), "alpha body v2") {
		t.Fatalf("project copy not updated (%v): %s", err, body)
	}
	var single protocol.UpdateData
	e.jsonOK(t, e.userDir, &single, "update", "alpha", "--json")
	if len(single.Skills) != 1 || single.Skills[0].Outcome != "up_to_date" {
		t.Fatalf("update alpha = %+v", single)
	}
	if perr := e.jsonFail(t, e.userDir, "update", "nope", "--json"); perr.Code != protocol.CodeNotInstalled || perr.SkillID != "nope" {
		t.Fatalf("update of an unknown skill = %+v", perr)
	}

	// Uninstall: --yes is required; a confirmation that named no project
	// fails with the projects to confirm; confirming those proceeds.
	if perr := e.jsonFail(t, e.userDir, "uninstall", "alpha", "--json"); perr.Code != protocol.CodeUsage {
		t.Fatalf("uninstall without --yes = %+v", perr)
	}
	perr = e.jsonFail(t, e.userDir, "uninstall", "alpha", "--json", "--yes", "--confirmed-root=")
	if perr.Code != protocol.CodeNeedsConfirm || len(perr.Paths) != 1 || perr.Paths[0] != project {
		t.Fatalf("uninstall with no confirmed project = %+v, want needs_confirm naming %s", perr, project)
	}
	if _, err := os.Stat(filepath.Join(project, ".agents", "skills", "alpha")); err != nil {
		t.Fatalf("a refused uninstall removed the project copy: %v", err)
	}
	var un protocol.UninstallData
	e.jsonOK(t, e.userDir, &un, "uninstall", "alpha", "--json", "--yes", "--confirmed-root", perr.Paths[0])
	if len(un.Skills) != 1 || un.Skills[0].ID != "alpha" ||
		!slices.Contains(un.Skills[0].RemovedCopies, "global") || !slices.Contains(un.Skills[0].RemovedCopies, project) {
		t.Fatalf("uninstall = %+v", un)
	}
	if perr := e.jsonFail(t, e.userDir, "uninstall", "alpha", "--json", "--yes"); perr.Code != protocol.CodeNotInstalled {
		t.Fatalf("uninstall of a removed skill = %+v", perr)
	}
	// --all with --yes and no confirmation list clears every project.
	var rest protocol.UninstallData
	e.jsonOK(t, e.userDir, &rest, "uninstall", "--all", "--json", "--yes")
	if len(rest.Skills) != 1 || rest.Skills[0].ID != "beta" {
		t.Fatalf("uninstall --all = %+v", rest)
	}
	var empty protocol.UninstallData
	e.jsonOK(t, e.userDir, &empty, "uninstall", "--all", "--json", "--yes")
	if len(empty.Skills) != 0 {
		t.Fatalf("uninstall --all of nothing = %+v", empty)
	}
}

// TestJSONImport: import --json adopts a project's lockfile into a fresh
// Home, and a directory without one reports 0 entries.
func TestJSONImport(t *testing.T) {
	needGit(t)
	_, url := initSkillRepo(t)
	project := t.TempDir()
	teammate := env{home: t.TempDir(), userDir: t.TempDir(), bin: skillmBinary(t)}
	teammate.jsonOK(t, teammate.userDir, &protocol.InstallData{}, "install", url, "alpha", "--project", project, "--json")

	e := env{home: t.TempDir(), userDir: t.TempDir(), bin: skillmBinary(t)}
	var imp protocol.ImportData
	e.jsonOK(t, e.userDir, &imp, "import", project, "--json")
	if imp.Root != project || imp.Entries != 1 || len(imp.Skills) != 1 ||
		imp.Skills[0].ID != "alpha" || imp.Skills[0].Outcome != "imported" {
		t.Fatalf("import = %+v", imp)
	}
	var none protocol.ImportData
	e.jsonOK(t, e.userDir, &none, "import", t.TempDir(), "--json")
	if none.Entries != 0 || len(none.Skills) != 0 {
		t.Fatalf("import of a directory with no lockfile = %+v", none)
	}
}

// TestJSONUpgrade: a source build reports method "dev" to --check without a
// network request and refuses an upgrade with source_build; a release build
// inside an app bundle refuses with managed_by_app before the network.
func TestJSONUpgrade(t *testing.T) {
	e := env{home: t.TempDir(), userDir: t.TempDir(), bin: skillmBinary(t)}
	var st protocol.SelfStatusData
	e.jsonOK(t, e.userDir, &st, "upgrade", "--check", "--json")
	if st.Method != "dev" || st.Current != "dev" || st.Latest != "" || st.Available || st.Eligible {
		t.Fatalf("upgrade --check (dev) = %+v", st)
	}
	if perr := e.jsonFail(t, e.userDir, "upgrade", "--json"); perr.Code != protocol.CodeSourceBuild {
		t.Fatalf("upgrade (dev) = %+v", perr)
	}

	name := "skillm"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	bundled := filepath.Join(t.TempDir(), "skillm.app", "Contents", "Helpers", name)
	buildReleaseBinary(t, bundled, "0.2.0")
	bundle := env{home: t.TempDir(), userDir: t.TempDir(), bin: bundled}
	offline := []string{"HTTPS_PROXY=http://127.0.0.1:1", "HTTP_PROXY=http://127.0.0.1:1", "NO_PROXY=", "no_proxy="}
	stdout, stderr, ok := bundle.runJSON(t, bundle.userDir, offline, "upgrade", "--json")
	if ok || stderr != "" {
		t.Fatalf("upgrade --json in a bundle: ok=%v stderr=%q stdout=%q", ok, stderr, stdout)
	}
	if doc := decodeDoc(t, stdout); doc.Error == nil || doc.Error.Code != protocol.CodeManagedByApp {
		t.Fatalf("upgrade --json in a bundle = %s, want managed_by_app", stdout)
	}
}
