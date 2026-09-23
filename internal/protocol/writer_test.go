package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/ultrakorne/skillm/internal/core"
)

// TestWriterDocument: a single document collects warn and error logs as
// warnings (not info logs or item events), writes nothing before the result,
// and ignores everything after it.
func TestWriterDocument(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf, false)
	w.Event(core.Event{Type: core.EventLog, Level: core.LevelInfo, Code: "lock_wait", Text: "waiting"})
	w.Event(core.Event{Type: core.EventItemDone, Level: core.LevelError, Code: "error", Text: "row"})
	w.Event(core.Event{Type: core.EventLog, Level: core.LevelWarn, Skill: "a", Code: "copy_failed", Text: "a: copy failed"})
	w.Event(core.Event{Type: core.EventLog, Level: core.LevelError, Code: "scan_failed", Text: "scan failed"})
	if buf.Len() != 0 {
		t.Fatalf("a document wrote before its result: %q", buf.String())
	}
	if w.Finished() {
		t.Fatal("Finished before Result")
	}
	if err := w.Result(map[string]int{"n": 1}); err != nil {
		t.Fatal(err)
	}
	if !w.Finished() {
		t.Fatal("not Finished after Result")
	}
	// Later output is ignored: one document only.
	w.Event(core.Event{Type: core.EventLog, Level: core.LevelWarn, Text: "late"})
	if err := w.Fail(errors.New("late")); err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("want 1 line, got %d: %q", len(lines), buf.String())
	}
	var env Envelope
	if err := json.Unmarshal([]byte(lines[0]), &env); err != nil {
		t.Fatal(err)
	}
	if env.Type != "" || env.Error != nil || env.SchemaVersion != SchemaVersion {
		t.Fatalf("envelope = %+v", env)
	}
	want := []Warning{
		{Code: "copy_failed", Message: "a: copy failed", SkillID: "a"},
		{Code: "scan_failed", Message: "scan failed"},
	}
	if len(env.Warnings) != len(want) || env.Warnings[0] != want[0] || env.Warnings[1] != want[1] {
		t.Fatalf("warnings = %+v, want %+v", env.Warnings, want)
	}
}

// TestWriterStream: every event is a line as it happens, and the stream ends
// with one result line carrying the error.
func TestWriterStream(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf, true)
	w.Event(core.Event{Type: core.EventBatch, Items: []string{"a"}})
	if !strings.Contains(buf.String(), `"event":"batch"`) {
		t.Fatalf("event not written as it happened: %q", buf.String())
	}
	w.Event(core.Event{Type: core.EventItemStart, Index: 0, Skill: "a"})
	if err := w.Fail(errors.New("boom")); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("want 3 lines, got %q", buf.String())
	}
	var start EventLine
	if err := json.Unmarshal([]byte(lines[1]), &start); err != nil {
		t.Fatal(err)
	}
	if start.Index == nil || *start.Index != 0 || start.SkillID != "a" {
		t.Fatalf("item_start = %+v (index 0 must be present)", start)
	}
	var res Envelope
	if err := json.Unmarshal([]byte(lines[2]), &res); err != nil {
		t.Fatal(err)
	}
	if res.Type != TypeResult || res.Error == nil || res.Error.Code != CodeError || res.Data != nil {
		t.Fatalf("result = %+v", res)
	}
}
