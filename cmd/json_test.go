package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ultrakorne/skillm/internal/protocol"
)

// These tests drive the built binary with --json, as a GUI does, and decode
// its stdout with the protocol types: stdout must hold only protocol output,
// and stderr nothing at all.

// jsonEnvelope is protocol.Envelope with its data left raw for a second decode.
type jsonEnvelope struct {
	SchemaVersion int                `json:"schema_version"`
	Type          string             `json:"type"`
	Data          json.RawMessage    `json:"data"`
	Warnings      []protocol.Warning `json:"warnings"`
	Error         *protocol.Error    `json:"error"`
}

// runJSON runs the binary in dir with extra environment entries and returns
// stdout, stderr and whether it exited 0.
func (e env) runJSON(t *testing.T, dir string, extraEnv []string, args ...string) (stdout, stderr string, ok bool) {
	t.Helper()
	cmd := exec.Command(e.bin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"HOME="+e.userDir,
		"USERPROFILE="+e.userDir,
		"SKILLM_HOME="+e.home,
		"GIT_CONFIG_GLOBAL="+filepath.Join(e.userDir, ".gitconfig"),
		"GIT_CONFIG_SYSTEM="+os.DevNull,
		"GIT_TERMINAL_PROMPT=0",
	)
	cmd.Env = append(cmd.Env, extraEnv...)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	if err != nil {
		if _, exit := err.(*exec.ExitError); !exit {
			t.Fatalf("run skillm %s: %v", strings.Join(args, " "), err)
		}
	}
	return out.String(), errOut.String(), err == nil
}

// decodeDoc decodes a single-document stdout: exactly one line holding an
// envelope with no "type".
func decodeDoc(t *testing.T, stdout string) jsonEnvelope {
	t.Helper()
	if strings.Count(stdout, "\n") != 1 || !strings.HasSuffix(stdout, "\n") {
		t.Fatalf("want exactly one JSON line on stdout, got %q", stdout)
	}
	var env jsonEnvelope
	if err := json.Unmarshal([]byte(stdout), &env); err != nil {
		t.Fatalf("decode %q: %v", stdout, err)
	}
	if env.SchemaVersion != protocol.SchemaVersion || env.Type != "" || env.Warnings == nil {
		t.Fatalf("bad envelope header: %s", stdout)
	}
	return env
}

// decodeData decodes a successful envelope's data into v.
func decodeData(t *testing.T, env jsonEnvelope, v any) {
	t.Helper()
	if env.Error != nil {
		t.Fatalf("unexpected error envelope: %+v", env.Error)
	}
	dec := json.NewDecoder(bytes.NewReader(env.Data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		t.Fatalf("decode data %s: %v", env.Data, err)
	}
}

func TestJSONVersion(t *testing.T) {
	e := env{home: t.TempDir(), userDir: t.TempDir(), bin: skillmBinary(t)}
	// An empty PATH: version needs no git, which is how a GUI learns the
	// CLI's api_version before it can report git missing.
	noGit := []string{"PATH=" + t.TempDir()}
	stdout, stderr, ok := e.runJSON(t, e.userDir, noGit, "version", "--json")
	if !ok || stderr != "" {
		t.Fatalf("version --json failed: ok=%v stderr=%q stdout=%q", ok, stderr, stdout)
	}
	var v protocol.VersionData
	decodeData(t, decodeDoc(t, stdout), &v)
	if v.Version != "dev" || v.APIVersion != protocol.APIVersion {
		t.Fatalf("version data = %+v", v)
	}
	for _, c := range []string{"check", "events", "list", "version"} {
		if !slices.Contains(v.Capabilities, c) {
			t.Errorf("capabilities %v lack %q", v.Capabilities, c)
		}
	}
	if !slices.IsSorted(v.Capabilities) {
		t.Errorf("capabilities not sorted: %v", v.Capabilities)
	}

	// Without --json it prints the --version line.
	plain, _, ok := e.runJSON(t, e.userDir, noGit, "version")
	if !ok || plain != "skillm version dev\n" {
		t.Fatalf("version = %q (ok=%v)", plain, ok)
	}
}

// TestJSONErrors: a failure writes the error envelope to stdout, nothing to
// stderr, and exits non-zero — for a missing git, a command with no JSON mode
// and a bad command line, whatever the flag order.
func TestJSONErrors(t *testing.T) {
	e := env{home: t.TempDir(), userDir: t.TempDir(), bin: skillmBinary(t)}
	cases := []struct {
		name     string
		extraEnv []string
		args     []string
		code     string
	}{
		{"git missing", []string{"PATH=" + t.TempDir()}, []string{"list", "--json"}, protocol.CodeGitMissing},
		{"no json mode", nil, []string{"install", "alpha", "--json"}, protocol.CodeJSONUnsupported},
		{"unknown flag after --json", nil, []string{"list", "--json", "--bogus"}, protocol.CodeUsage},
		{"unknown flag before --json", nil, []string{"list", "--bogus", "--json"}, protocol.CodeUsage},
		{"extra argument", nil, []string{"check", "extra", "--json"}, protocol.CodeUsage},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, ok := e.runJSON(t, e.userDir, tc.extraEnv, tc.args...)
			if ok {
				t.Fatalf("skillm %v exited 0: %q", tc.args, stdout)
			}
			if stderr != "" {
				t.Fatalf("stderr = %q, want nothing in JSON mode", stderr)
			}
			env := decodeDoc(t, stdout)
			if env.Error == nil || env.Error.Code != tc.code || env.Error.Message == "" || string(env.Data) != "null" {
				t.Fatalf("envelope = %s, want error code %q", stdout, tc.code)
			}
		})
	}

	// --events without --json is a plain CLI error.
	stdout, stderr, ok := e.runJSON(t, e.userDir, nil, "list", "--events")
	if ok || stdout != "" || !strings.Contains(stderr, "requires --json") {
		t.Fatalf("list --events: ok=%v stdout=%q stderr=%q", ok, stdout, stderr)
	}
}

// TestJSONListCheck drives list and check with --json (and check with
// --events) over git and local skills installed at both scopes.
func TestJSONListCheck(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	repo, url := initSkillRepo(t)
	localSrc := t.TempDir()
	writeSkillMD(t, filepath.Join(localSrc, "omega"), "omega", "omega body")
	project := t.TempDir()
	e := env{home: t.TempDir(), userDir: t.TempDir(), bin: skillmBinary(t)}

	e.run(t, "install", url, "alpha", "beta", "--global")
	e.runIn(t, project, "install", url, "alpha", "--local")
	e.run(t, "install", filepath.Join(localSrc, "omega"), "--global")
	writeSkillMD(t, filepath.Join(repo, "beta"), "beta", "beta body v2")
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-q", "-m", "change beta")

	// list --json, run from inside the project.
	stdout, stderr, ok := e.runJSON(t, project, nil, "list", "--json")
	if !ok || stderr != "" {
		t.Fatalf("list --json: ok=%v stderr=%q stdout=%q", ok, stderr, stdout)
	}
	var list protocol.ListData
	decodeData(t, decodeDoc(t, stdout), &list)
	ids := make([]string, 0, len(list.Skills))
	for _, s := range list.Skills {
		ids = append(ids, s.ID)
	}
	if !slices.Equal(ids, []string{"alpha", "beta", "omega"}) {
		t.Fatalf("listed ids = %v", ids)
	}
	alpha := list.Skills[0]
	if alpha.Kind != "git" || alpha.Subpath != "alpha" || alpha.Ref == "" || alpha.Revision == "" ||
		alpha.SourceLabel != alpha.Source+"//alpha" || !strings.HasSuffix(alpha.InstalledAt, "Z") {
		t.Fatalf("alpha = %+v", alpha)
	}
	if len(alpha.Installs) != 2 {
		t.Fatalf("alpha installs = %+v", alpha.Installs)
	}
	g, l := alpha.Installs[0], alpha.Installs[1]
	if g.Scope != "global" || g.Root != "" || !g.Recorded || !g.Exists || !slices.Equal(g.Agents, []string{"agents", "claude"}) {
		t.Fatalf("alpha global install = %+v", g)
	}
	if l.Scope != "local" || evalProject(t, l.Root) != evalProject(t, project) || !l.Recorded || !l.Exists ||
		!slices.Equal(l.Agents, []string{"agents", "claude"}) {
		t.Fatalf("alpha local install = %+v", l)
	}
	if omega := list.Skills[2]; omega.Kind != "local" || omega.Ref != "" || omega.SourceLabel != omega.Source {
		t.Fatalf("omega = %+v", omega)
	}

	// check --json.
	stdout, stderr, ok = e.runJSON(t, e.userDir, nil, "check", "--json")
	if !ok || stderr != "" {
		t.Fatalf("check --json: ok=%v stderr=%q stdout=%q", ok, stderr, stdout)
	}
	var check protocol.CheckData
	decodeData(t, decodeDoc(t, stdout), &check)
	wantStatus := map[string]string{"alpha": "up_to_date", "beta": "update_available", "omega": "local"}
	if len(check.Skills) != len(wantStatus) || check.Updates != 1 {
		t.Fatalf("check = %+v", check)
	}
	for _, s := range check.Skills {
		if s.Status != wantStatus[s.ID] {
			t.Errorf("%s status = %q, want %q", s.ID, s.Status, wantStatus[s.ID])
		}
	}
	if b := check.Skills[1]; b.InstalledRev == "" || b.UpstreamRev == "" || b.InstalledRev == b.UpstreamRev {
		t.Errorf("beta revisions = %+v", b)
	}

	// check --json --events: a batch, a start and a done per skill, then
	// the result line.
	stdout, stderr, ok = e.runJSON(t, e.userDir, nil, "check", "--json", "--events")
	if !ok || stderr != "" {
		t.Fatalf("check --json --events: ok=%v stderr=%q stdout=%q", ok, stderr, stdout)
	}
	lines := strings.Split(strings.TrimSuffix(stdout, "\n"), "\n")
	counts := map[string]int{}
	for _, line := range lines[:len(lines)-1] {
		var ev protocol.EventLine
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("decode event %q: %v", line, err)
		}
		if ev.Type != protocol.TypeEvent || ev.SchemaVersion != protocol.SchemaVersion {
			t.Fatalf("bad event line %q", line)
		}
		counts[ev.Event]++
		if ev.Event == "item_done" && (ev.Index == nil || ev.Code != wantStatus[ev.SkillID]) {
			t.Errorf("item_done %q: want index and code %q", line, wantStatus[ev.SkillID])
		}
	}
	if counts["batch"] != 1 || counts["item_start"] != 3 || counts["item_done"] != 3 {
		t.Fatalf("event counts = %v in %q", counts, stdout)
	}
	var res jsonEnvelope
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &res); err != nil {
		t.Fatal(err)
	}
	if res.Type != protocol.TypeResult {
		t.Fatalf("last line is not the result: %q", lines[len(lines)-1])
	}
	var streamed protocol.CheckData
	decodeData(t, res, &streamed)
	if streamed.Updates != 1 || len(streamed.Skills) != 3 {
		t.Fatalf("streamed result = %+v", streamed)
	}
}
