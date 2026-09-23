package protocol

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ultrakorne/skillm/internal/core"
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

	cases := []struct {
		name  string
		write func(w *Writer) error
	}{
		{"version.json", func(w *Writer) error {
			return w.Result(VersionData{Version: "0.4.0", APIVersion: APIVersion,
				Capabilities: []string{"check", "events", "list", "version"}})
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
	}
	return errors.New("fixture with no known data type")
}

func strictUnmarshal(b []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}
