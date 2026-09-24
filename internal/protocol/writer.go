package protocol

import (
	"encoding/json"
	"io"
	"sync"

	"github.com/ultrakorne/skillm/internal/core"
)

// EventLine is one core Event on an --events stream.
type EventLine struct {
	SchemaVersion int `json:"schema_version"`
	// Type is always "event"; the last line of a stream is an Envelope
	// with Type "result" instead.
	Type string `json:"type"`
	// Event is the core event type: "log", "batch", "item_start",
	// "item_done" or "progress".
	Event string `json:"event"`
	// Level is "info", "success", "warn" or "error" (log and item_done).
	Level string `json:"level,omitempty"`
	// Index is the item's index in the batch (item_start and item_done).
	Index *int `json:"index,omitempty"`
	// SkillID is the skill the event concerns, when there is one.
	SkillID string `json:"skill_id,omitempty"`
	// Code is the event's stable machine code, e.g. "update_available".
	Code string `json:"code,omitempty"`
	// Text is the human sentence for the event.
	Text string `json:"text,omitempty"`
	// Items lists a batch's skill ids; Items[i] is item i.
	Items []string `json:"items,omitempty"`
	// Done and Total are a progress event's counts.
	Done  *int `json:"done,omitempty"`
	Total *int `json:"total,omitempty"`
}

// NewEventLine converts a core Event.
func NewEventLine(ev core.Event) EventLine {
	l := EventLine{
		SchemaVersion: SchemaVersion,
		Type:          TypeEvent,
		Event:         string(ev.Type),
		Level:         string(ev.Level),
		SkillID:       ev.Skill,
		Code:          ev.Code,
		Text:          ev.Text,
		Items:         ev.Items,
	}
	switch ev.Type {
	case core.EventItemStart, core.EventItemDone:
		i := ev.Index
		l.Index = &i
	case core.EventProgress:
		d, t := ev.Done, ev.Total
		l.Done, l.Total = &d, &t
	}
	return l
}

// Writer writes one command's protocol output. It is the command's
// core.Reporter: every warn or error log event becomes a Warning in the final
// Envelope, and on a stream (--events) every event is also written as an
// EventLine as it happens. The command then ends with exactly one Result or
// Fail; whichever comes first wins, and later calls (and events) are
// ignored. A Writer is safe for concurrent use.
type Writer struct {
	mu       sync.Mutex
	enc      *json.Encoder
	stream   bool
	warnings []Warning
	finished bool
}

// NewWriter returns a Writer on w, writing an NDJSON stream when stream is
// set and a single document otherwise.
func NewWriter(w io.Writer, stream bool) *Writer {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return &Writer{enc: enc, stream: stream}
}

// Event implements core.Reporter.
func (w *Writer) Event(ev core.Event) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.finished {
		return
	}
	if ev.Type == core.EventLog && (ev.Level == core.LevelWarn || ev.Level == core.LevelError) {
		w.warnings = append(w.warnings, Warning{Code: ev.Code, Message: ev.Text, SkillID: ev.Skill})
	}
	if w.stream {
		// A failed write (a closed pipe) surfaces again at Result or Fail.
		_ = w.enc.Encode(NewEventLine(ev))
	}
}

// Result ends the output with data as the command's successful outcome.
func (w *Writer) Result(data any) error {
	return w.finish(data, nil)
}

// Fail ends the output with err (mapped by ErrorFrom) as the command's
// error.
func (w *Writer) Fail(err error) error {
	return w.finish(nil, ErrorFrom(err))
}

// Finished reports whether Result or Fail has been called.
func (w *Writer) Finished() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.finished
}

func (w *Writer) finish(data any, perr *Error) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.finished {
		return nil
	}
	w.finished = true
	env := Envelope{SchemaVersion: SchemaVersion, Data: data, Warnings: w.warnings, Error: perr}
	if env.Warnings == nil {
		env.Warnings = []Warning{}
	}
	if w.stream {
		env.Type = TypeResult
	}
	return w.enc.Encode(env)
}

// CodeLockWait is the code of the info log event a command reports when it
// has to wait for another skillm process to release Home's lock; its Text
// names the holder when known.
const CodeLockWait = "lock_wait"
