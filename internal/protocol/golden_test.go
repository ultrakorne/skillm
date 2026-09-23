package protocol

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ultrakorne/skillm/internal/agentdir"
	"github.com/ultrakorne/skillm/internal/config"
	"github.com/ultrakorne/skillm/internal/core"
	"github.com/ultrakorne/skillm/internal/status"
	"github.com/ultrakorne/skillm/internal/store"
)

// The golden fixtures in testdata/ are the cross-language contract: the GUI's
// tests decode the same files. Each case writes a document through Writer
// exactly as a command does. A .json fixture holds one document, indented for
// review (compared after indenting the compact output); a .ndjson fixture
// holds a stream byte for byte. Regenerate with SKILLM_UPDATE_GOLDEN=1.

func TestGoldenFixtures(t *testing.T) {
	installed := time.Date(2026, 9, 1, 10, 30, 0, 0, time.FixedZone("CEST", 2*60*60))

	list := core.ListResult{Skills: []core.ListedSkill{
		{
			ID: "grill-with-docs", Kind: "git",
			Source: "https://github.com/acme/skills", Subpath: "skills/grill-with-docs",
			Ref: "main", Revision: "4b825dc642cb6eb9a060e54bf8d69288fbee4904",
			InstalledAt: installed,
			Installs: []core.Install{
				{Scope: core.ScopeGlobal, Path: "/Users/me/.agents/skills/grill-with-docs",
					Agents: []string{"agents", "claude"}, Recorded: true, Exists: true},
				{Scope: core.ScopeLocal, Root: "/Users/me/src/app",
					Path:   "/Users/me/src/app/.agents/skills/grill-with-docs",
					Agents: []string{"agents"}, Recorded: true, Exists: true},
			},
		},
		{
			ID: "notes", Kind: "local", Source: "/Users/me/skills/notes",
			Installs: []core.Install{
				{Scope: core.ScopeLocal, Root: "/Users/me/src/other",
					Path: "/Users/me/src/other/.agents/skills/notes", Agents: nil, Recorded: true},
			},
		},
	}}

	check := core.CheckResult{Skills: []core.CheckedSkill{
		{ID: "alpha", Kind: "git", Status: core.StatusUpToDate, InstalledRev: "aaa1", UpstreamRev: "aaa1"},
		{ID: "beta", Kind: "git", Status: core.StatusUpdateAvailable, InstalledRev: "bbb1", UpstreamRev: "bbb2"},
		{ID: "gamma", Kind: "git", Status: core.StatusUntracked, InstalledRev: "ccc1",
			Err: errors.New(`gitx: "gamma" not found at "main"`)},
		{ID: "delta", Kind: "git", Status: core.StatusError, InstalledRev: "ddd1",
			Err: errors.New("git clone failed: repository not found")},
		{ID: "omega", Kind: "local", Status: core.StatusLocal},
	}}

	install := core.InstallResult{Scope: agentdir.Local, Base: "/Users/me/src/app", Skills: []core.InstalledSkill{
		{ID: "alpha", Action: core.VendorWrote},
		{ID: "beta", Action: core.VendorRefreshed},
		{ID: "gamma", Action: core.VendorConverted},
		{ID: "delta", Action: core.VendorAdopted},
		{ID: "omega", Action: core.VendorBlocked},
	}}

	update := core.UpdateResult{
		Imported: []core.ImportedSkill{
			{ID: "shared", Root: "/Users/me/src/app", Outcome: core.ImportImported},
			{ID: "npm-thing", Root: "/Users/me/src/app", Outcome: core.ImportSkipped,
				Err: errors.New("not a git remote (source type \"node_modules\")")},
		},
		Skills: []core.UpdatedSkill{
			{ID: "alpha", Kind: "git", Outcome: core.OutcomeUpdated, Revision: "aaa2", Advanced: true,
				Warnings: []error{errors.New("/Users/me/src/app: skills-lock.json not updated: permission denied")}},
			{ID: "beta", Kind: "git", Outcome: core.OutcomeUpToDate, Revision: "bbb1"},
			{ID: "gamma", Kind: "git", Outcome: core.OutcomePruned, Revision: "ccc2", Advanced: true,
				Pruned: []string{"global", "/Users/me/src/old"}},
			{ID: "delta", Kind: "git", Outcome: core.OutcomeDriftCheckSkipped, Revision: "ddd1",
				Err: errors.New("stage delta: disk full")},
			{ID: "omega", Kind: "local", Outcome: core.OutcomeSynced},
		},
		Synced: true,
	}

	imported := core.ImportResult{Root: "/Users/me/src/app", Entries: 3, Skills: update.Imported}

	uninstall := core.UninstallResult{Skills: []core.UninstalledSkill{
		{ID: "alpha", RemovedCopies: []string{"global", "/Users/me/src/app"}},
		{ID: "beta", RemovedCopies: []string{"global"},
			Warnings: []error{errors.New("/Users/me/.claude/skills/beta: not a skillm link; left in place")}},
	}}

	cache := statusCache()

	cases := []struct {
		name  string
		write func(w *Writer) error
	}{
		{"version.json", func(w *Writer) error {
			return w.Result(VersionData{Version: "0.4.0", APIVersion: APIVersion,
				Capabilities: []string{"agent ls", "agent set", "check", "config get", "config set", "events",
					"import", "install", "list", "refresh", "source inspect", "status", "uninstall", "update", "upgrade", "version"}})
		}},
		{"list.json", func(w *Writer) error { return w.Result(NewListData(list)) }},
		{"list_empty.json", func(w *Writer) error { return w.Result(NewListData(core.ListResult{})) }},
		{"check.json", func(w *Writer) error { return w.Result(NewCheckData(check)) }},
		{"error_home_locked.json", func(w *Writer) error {
			return w.Fail(&store.LockTimeoutError{Path: "/Users/me/.skillm/.lock",
				Holder: "pid 4242: skillm update", Timeout: 30 * time.Second})
		}},
		{"error_cancelled.json", func(w *Writer) error { return w.Fail(context.Canceled) }},
		{"error_foreign_files.json", func(w *Writer) error {
			w.Event(core.Event{Type: core.EventLog, Level: core.LevelWarn, Skill: "beta",
				Code: core.CodeLinkRefused, Text: "beta: /Users/me/.claude/skills/beta is not a skillm link; left alone"})
			return w.Fail(&core.ForeignFilesError{Paths: []string{"/Users/me/.agents/skills/beta"}})
		}},
		{"install.json", func(w *Writer) error { return w.Result(NewInstallData(install)) }},
		{"install_global_empty.json", func(w *Writer) error {
			return w.Result(NewInstallData(core.InstallResult{Scope: agentdir.Global}))
		}},
		{"install_events.ndjson", func(w *Writer) error {
			w.Event(core.Event{Type: core.EventBatch, Items: []string{"alpha"}})
			w.Event(core.Event{Type: core.EventItemStart, Index: 0, Skill: "alpha"})
			w.Event(core.Event{Type: core.EventLog, Level: core.LevelSuccess, Skill: "alpha",
				Code: core.CodeInstalled, Text: "installed alpha in ~/.agents/skills (global)"})
			w.Event(core.Event{Type: core.EventItemDone, Index: 0, Skill: "alpha", Level: core.LevelSuccess,
				Code: core.CodeInstalled, Text: "installed alpha in ~/.agents/skills (global)"})
			return w.Result(NewInstallData(core.InstallResult{Scope: agentdir.Global,
				Skills: []core.InstalledSkill{{ID: "alpha", Action: core.VendorWrote}}}))
		}},
		{"update.json", func(w *Writer) error { return w.Result(NewUpdateData(update)) }},
		{"update_events.ndjson", func(w *Writer) error {
			w.Event(core.Event{Type: core.EventBatch, Items: []string{"alpha"}})
			w.Event(core.Event{Type: core.EventItemStart, Index: 0, Skill: "alpha"})
			w.Event(core.Event{Type: core.EventItemDone, Index: 0, Skill: "alpha", Level: core.LevelSuccess,
				Code: string(core.OutcomeUpdated), Text: "alpha: updated"})
			return w.Result(NewUpdateData(core.UpdateResult{Skills: update.Skills[:1], Synced: true}))
		}},
		{"import.json", func(w *Writer) error { return w.Result(NewImportData(imported)) }},
		{"uninstall.json", func(w *Writer) error { return w.Result(NewUninstallData(uninstall)) }},
		{"self_status.json", func(w *Writer) error {
			return w.Result(NewSelfStatusData(core.SelfStatus{Current: "0.4.0", Latest: "0.5.0", Available: true,
				Method: core.MethodBundled, Executable: "/Applications/skillm.app/Contents/Helpers/skillm"}))
		}},
		{"self_status_dev.json", func(w *Writer) error {
			return w.Result(NewSelfStatusData(core.SelfStatus{Current: "dev", Method: core.MethodDev,
				Executable: "/Users/me/go/bin/skillm"}))
		}},
		{"upgrade.json", func(w *Writer) error {
			return w.Result(UpgradeData{Upgraded: true, From: "0.4.0", To: "0.5.0", Path: "/usr/local/bin/skillm"})
		}},
		{"error_needs_confirm.json", func(w *Writer) error {
			return w.Fail(&core.UninstallScopeChangedError{Roots: []string{"/Users/me/src/app", "/Users/me/src/new"}})
		}},
		{"error_update_failed.json", func(w *Writer) error {
			w.Event(core.Event{Type: core.EventLog, Level: core.LevelError, Skill: "alpha",
				Code: core.CodeUpdateFailed, Text: "alpha: git fetch failed"})
			w.Event(core.Event{Type: core.EventLog, Level: core.LevelError, Skill: "beta",
				Code: core.CodeUpdateFailed, Text: "beta: git fetch failed"})
			return w.Fail(&core.UpdateFailedError{IDs: []string{"alpha", "beta"},
				Failures: []string{"alpha: git fetch failed", "beta: git fetch failed"}})
		}},
		{"inspect.json", func(w *Writer) error {
			return w.Result(NewInspectData(&core.Inspection{
				Source: "https://github.com/acme/skills", Kind: "git", Ref: "main",
				Commit: "9fceb02d0ae598e95dc970b74767f19372d61af8",
				Skills: []core.InspectedSkill{
					{ID: "grill-with-docs", Name: "grill-with-docs", Description: "Interview the user against the docs", Path: "skills/grill-with-docs"},
					{ID: "notes", Path: "notes"},
				},
			}))
		}},
		{"inspect_local.json", func(w *Writer) error {
			return w.Result(NewInspectData(&core.Inspection{
				Source: "/Users/me/skills/notes", Kind: "local",
				Skills: []core.InspectedSkill{{ID: "notes", Name: "notes", Description: "Take notes", Path: "/Users/me/skills/notes"}},
			}))
		}},
		{"agents.json", func(w *Writer) error {
			return w.Result(NewAgentsData([]core.AgentInfo{
				{Name: "agents", Enabled: true, Global: "~/.agents/skills", Local: ".agents/skills"},
				{Name: "claude", Enabled: true, Global: "~/.claude/skills", Local: ".claude/skills"},
				{Name: "opencode", Enabled: false, Global: "~/.config/opencode/skill"},
			}))
		}},
		{"agents_set.json", func(w *Writer) error {
			w.Event(core.Event{Type: core.EventLog, Level: core.LevelWarn, Code: core.CodeLinkSkipped,
				Text: "opencode: /Users/me/.config/opencode/skill/notes is not a skillm link; skipped"})
			return w.Result(NewAgentsSetData(core.AgentsResult{
				Enabled: []string{"agents", "opencode"},
				Changes: []core.AgentChange{
					{Name: "opencode", Enabled: true, Skills: []string{"grill-with-docs", "notes"},
						Places:   []string{"global", "local: /Users/me/src/app"},
						Warnings: []error{errors.New("opencode: /Users/me/.config/opencode/skill/notes is not a skillm link; skipped")}},
					{Name: "claude", Enabled: false, Skills: []string{"grill-with-docs"}, Places: []string{"global"}},
				},
			}))
		}},
		{"agents_set_unchanged.json", func(w *Writer) error {
			return w.Result(NewAgentsSetData(core.AgentsResult{Enabled: []string{"agents", "claude"}}))
		}},
		{"config.json", func(w *Writer) error {
			c := config.Default()
			if err := c.Set(config.KeyRefreshIntervalHours, "12"); err != nil {
				return err
			}
			return w.Result(NewConfigData(c))
		}},
		{"error_invalid_value.json", func(w *Writer) error {
			return w.Fail(&config.InvalidValueError{Key: config.KeyRefreshIntervalHours, Value: "0",
				Want: "a whole number of hours from 1 to 720"})
		}},
		{"status.json", func(w *Writer) error {
			return w.Result(NewStatusData(core.StatusResult{Status: cache}))
		}},
		{"status_never.json", func(w *Writer) error {
			return w.Result(NewStatusData(core.StatusResult{Status: status.New(), Stale: true}))
		}},
		{"refresh.json", func(w *Writer) error {
			w.Event(core.Event{Type: core.EventLog, Level: core.LevelWarn, Code: core.CodeSelfCheckFailed,
				Text: "check for a newer skillm: dial tcp: no route to host"})
			f := *cache
			f.Self = &status.Self{Current: "0.4.0", Method: "bundled",
				Executable: "/Applications/skillm.app/Contents/Helpers/skillm",
				Error:      "check for a newer skillm: dial tcp: no route to host"}
			f.Recount()
			return w.Result(NewRefreshData(core.StatusResult{Status: &f, Refreshed: true}))
		}},
		{"check_events.ndjson", func(w *Writer) error {
			w.Event(core.Event{Type: core.EventBatch, Items: []string{"alpha", "beta"}})
			w.Event(core.Event{Type: core.EventItemStart, Index: 0, Skill: "alpha"})
			w.Event(core.Event{Type: core.EventItemStart, Index: 1, Skill: "beta"})
			w.Event(core.Event{Type: core.EventItemDone, Index: 1, Skill: "beta", Level: core.LevelWarn,
				Code: "update_available", Text: "beta: update available (https://github.com/acme/skills//beta)"})
			w.Event(core.Event{Type: core.EventItemDone, Index: 0, Skill: "alpha", Level: core.LevelSuccess,
				Code: "up_to_date", Text: "alpha: up-to-date"})
			w.Event(core.Event{Type: core.EventProgress, Done: 2, Total: 2})
			return w.Result(NewCheckData(core.CheckResult{Skills: check.Skills[:2]}))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			stream := filepath.Ext(tc.name) == ".ndjson"
			if err := tc.write(NewWriter(&buf, stream)); err != nil {
				t.Fatalf("write: %v", err)
			}
			got := buf.Bytes()
			if !stream {
				var ind bytes.Buffer
				if err := json.Indent(&ind, got, "", "  "); err != nil {
					t.Fatalf("indent %s: %v", got, err)
				}
				got = ind.Bytes()
			}
			golden(t, tc.name, got)
		})
	}
}

// statusCache is the refresh cache the status fixtures show: a skill of
// every status and a newer bundled skillm.
func statusCache() *status.File {
	f := status.New()
	f.CheckedAt = time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	f.NextDueAt = f.CheckedAt.Add(24 * time.Hour)
	f.Skills = []status.Skill{
		{ID: "alpha", Status: status.SkillUpToDate, InstalledRev: "aaa1", UpstreamRev: "aaa1"},
		{ID: "beta", Status: status.SkillUpdateAvailable, InstalledRev: "bbb1", UpstreamRev: "bbb2"},
		{ID: "gamma", Status: status.SkillUntracked, InstalledRev: "ccc1", Error: `gitx: "gamma" not found at "main"`},
		{ID: "delta", Status: status.SkillError, InstalledRev: "ddd1", Error: "git clone failed: repository not found"},
		{ID: "omega", Status: status.SkillLocal},
	}
	f.Self = &status.Self{Current: "0.4.0", Latest: "0.5.0", Available: true, Method: "bundled",
		Executable: "/Applications/skillm.app/Contents/Helpers/skillm"}
	f.Recount()
	return f
}

// TestStatusFileGolden pins <home>/status.json byte for byte as status.Save
// writes it: GUIs that watch the file (Quickshell) decode it directly.
func TestStatusFileGolden(t *testing.T) {
	b, err := status.Marshal(statusCache())
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "status_file.json", b)
}

// golden compares got with testdata/name, or rewrites the file when
// SKILLM_UPDATE_GOLDEN=1.
func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if os.Getenv("SKILLM_UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (regenerate with SKILLM_UPDATE_GOLDEN=1): %v", err)
	}
	// A checkout with CRLF line endings (Windows autocrlf) is still a match.
	want = bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))
	if !bytes.Equal(got, want) {
		t.Fatalf("%s differs\n--- got\n%s\n--- want\n%s", path, got, want)
	}
}

// TestFixturesDecode decodes every fixture back into the protocol types with
// unknown fields refused, so a fixture can never drift from the Go structs.
func TestFixturesDecode(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "*"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no fixtures: %v", err)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if filepath.Base(f) == "status_file.json" {
			// The refresh cache itself, not an envelope.
			if err := strictUnmarshal(b, &status.File{}); err != nil {
				t.Errorf("%s: %v", f, err)
			}
			continue
		}
		var docs [][]byte
		if filepath.Ext(f) == ".ndjson" {
			docs = bytes.Split(bytes.TrimSpace(b), []byte("\n"))
		} else {
			docs = [][]byte{b}
		}
		for i, doc := range docs {
			if err := decodeStrict(f, doc, i == len(docs)-1); err != nil {
				t.Errorf("%s line %d: %v", f, i+1, err)
			}
		}
	}
}

// decodeStrict decodes one fixture document: an EventLine, or (last) an
// Envelope whose data matches the fixture's command.
func decodeStrict(file string, doc []byte, last bool) error {
	if !last {
		var l EventLine
		if err := strictUnmarshal(doc, &l); err != nil {
			return err
		}
		if l.Type != TypeEvent || l.SchemaVersion != SchemaVersion {
			return errors.New("not an event line")
		}
		return nil
	}
	var env struct {
		SchemaVersion int             `json:"schema_version"`
		Type          string          `json:"type"`
		Data          json.RawMessage `json:"data"`
		Warnings      []Warning       `json:"warnings"`
		Error         *Error          `json:"error"`
	}
	if err := strictUnmarshal(doc, &env); err != nil {
		return err
	}
	if env.SchemaVersion != SchemaVersion || env.Warnings == nil {
		return errors.New("bad envelope header")
	}
	if env.Error != nil {
		if string(env.Data) != "null" {
			return errors.New("a failed envelope carries data")
		}
		return nil
	}
	base := filepath.Base(file)
	switch {
	case base == "version.json":
		return strictUnmarshal(env.Data, &VersionData{})
	case base == "list.json" || base == "list_empty.json":
		return strictUnmarshal(env.Data, &ListData{})
	case base == "check.json" || base == "check_events.ndjson":
		return strictUnmarshal(env.Data, &CheckData{})
	case strings.HasPrefix(base, "install"):
		return strictUnmarshal(env.Data, &InstallData{})
	case strings.HasPrefix(base, "update"):
		return strictUnmarshal(env.Data, &UpdateData{})
	case base == "import.json":
		return strictUnmarshal(env.Data, &ImportData{})
	case base == "uninstall.json":
		return strictUnmarshal(env.Data, &UninstallData{})
	case strings.HasPrefix(base, "self_status"):
		return strictUnmarshal(env.Data, &SelfStatusData{})
	case base == "upgrade.json":
		return strictUnmarshal(env.Data, &UpgradeData{})
	case strings.HasPrefix(base, "inspect"):
		return strictUnmarshal(env.Data, &InspectData{})
	case base == "agents.json":
		return strictUnmarshal(env.Data, &AgentsData{})
	case strings.HasPrefix(base, "agents_set"):
		return strictUnmarshal(env.Data, &AgentsSetData{})
	case base == "config.json":
		return strictUnmarshal(env.Data, &ConfigData{})
	case base == "status.json" || base == "status_never.json":
		return strictUnmarshal(env.Data, &StatusData{})
	case base == "refresh.json":
		return strictUnmarshal(env.Data, &RefreshData{})
	}
	return errors.New("fixture with no known data type")
}

func strictUnmarshal(b []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}
