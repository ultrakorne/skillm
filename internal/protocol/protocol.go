// Package protocol is skillm's JSON protocol: the machine-readable output of
// `skillm <command> --json` that GUIs (the macOS app, a Quickshell widget)
// read instead of the terminal output.
//
// A command's output is one Envelope document on stdout. With --events it is
// NDJSON instead: one EventLine per core Event as it happens, then one
// Envelope with Type "result" as the last line. Nothing else is written to
// stdout in JSON mode. Keys are snake_case and times RFC 3339 in UTC.
//
// The golden fixtures in testdata/ are the cross-language contract: the Go
// tests pin them, and the GUI's tests decode the same files.
//
// protocol sits between cmd and core: it imports core (for the Reporter and
// the result types it serializes) but never the terminal UI or cobra.
package protocol

// SchemaVersion is the "schema_version" of every document and event line. It
// changes only when the envelope or event-line shape changes incompatibly.
const SchemaVersion = 1

// APIVersion is the "api_version" `skillm version --json` reports. A GUI
// refuses a CLI whose APIVersion it does not know. It changes when a
// command's arguments or data change incompatibly.
const APIVersion = 1

// TypeResult is the Envelope Type of the last line of an --events stream.
const TypeResult = "result"

// TypeEvent is the "type" of every EventLine.
const TypeEvent = "event"

// Envelope is a command's outcome: its Data on success, or its Error. A
// failed command still writes one (and exits non-zero). Warnings lists the
// warn and error log events the command reported, in order, either way.
type Envelope struct {
	SchemaVersion int `json:"schema_version"`
	// Type is "result" on the last line of an --events stream and empty
	// (omitted) for a single document.
	Type     string    `json:"type,omitempty"`
	Data     any       `json:"data"`
	Warnings []Warning `json:"warnings"`
	Error    *Error    `json:"error"`
}

// Warning is a problem the command reported without failing: one of core's
// warn or error log events.
type Warning struct {
	// Code is the event's stable machine code (e.g. "copy_failed").
	Code    string `json:"code"`
	Message string `json:"message"`
	SkillID string `json:"skill_id,omitempty"`
}

// Error is a failed command's error. Code is the stable machine code a GUI
// switches on (see the Code constants); Message is the human sentence the
// CLI would print.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	// SkillID names the skill the error is about, when there is one.
	SkillID string `json:"skill_id,omitempty"`
	// Path names the file or directory the error is about, when there is one.
	Path string `json:"path,omitempty"`
	// Paths lists several, e.g. the foreign entries of "foreign_files".
	Paths []string `json:"paths,omitempty"`
	// Retryable says the same command may succeed if run again unchanged
	// (e.g. Home was locked, or the run was cancelled).
	Retryable bool `json:"retryable"`
}

// Error implements error, so cmd can return an *Error with an exact code.
func (e *Error) Error() string { return e.Message }
