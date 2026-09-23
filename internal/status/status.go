// Package status is the refresh cache: <home>/status.json, the outcome of the
// last `skillm refresh` (a Check of every skill plus the self-update status),
// kept current by the commands that change what it reports. Every GUI reads
// it for its badge: the macOS app through `skillm status --json`, a
// Quickshell widget by watching the file itself. So the file's JSON form is a
// contract like the protocol's (its golden fixture is
// internal/protocol/testdata/cache/status.json).
//
// This package holds the file format, its atomic load and save, and the
// policies every reader shares: the badge, when a refresh is due and when the
// cache is stale. Deciding what goes into it is core's job.
package status

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/ultrakorne/skillm/internal/store"
)

// FileName is the cache's file name inside Home.
const FileName = "status.json"

// SchemaVersion is the file's "schema_version". It changes only when the
// file's shape changes incompatibly.
const SchemaVersion = 1

// RetryAfterFailure is how soon a refresh is due again when none of its
// lookups succeeded (typically: the machine was offline), instead of the
// whole interval.
const RetryAfterFailure = time.Hour

// Skill statuses, as core.Check reports them.
const (
	SkillUpToDate        = "up_to_date"
	SkillUpdateAvailable = "update_available"
	SkillUntracked       = "untracked"
	SkillLocal           = "local"
	SkillError           = "error"
)

// CodeSelfCheck is the Problem code of a failed self-update lookup.
const CodeSelfCheck = "self_check"

// File is status.json. Times are RFC 3339 in UTC, whole seconds.
type File struct {
	SchemaVersion int `json:"schema_version"`
	// CheckedAt is when the last refresh started; zero (omitted) when no
	// refresh ever ran.
	CheckedAt time.Time `json:"checked_at,omitzero"`
	// NextDueAt is when `refresh --if-due` runs the next check: CheckedAt
	// plus the refresh interval, or plus RetryAfterFailure when every lookup
	// failed. Zero (omitted) means due now.
	NextDueAt time.Time `json:"next_due_at,omitzero"`
	// Skills are the registered skills' upstream statuses, in Registry
	// order. A skill installed since the last refresh may be missing.
	Skills []Skill `json:"skills"`
	// Updates counts the skills with status "update_available".
	Updates int `json:"updates"`
	// Self is the running skillm against the latest release; null when no
	// refresh ever ran.
	Self *Self `json:"self"`
	// Errors lists every problem the last refresh (or a later command)
	// left: each skill whose status is "error" or "untracked", and a failed
	// self lookup (code "self_check").
	Errors []Problem `json:"errors"`
	// Badge is the one badge policy every GUI shows: Updates > 0 or
	// Self.Available.
	Badge bool `json:"badge"`
}

// Skill is one skill's upstream status.
type Skill struct {
	ID string `json:"id"`
	// Status is "up_to_date", "update_available", "untracked", "local" or
	// "error". A failed lookup stays "error": it never reads as current.
	Status string `json:"status"`
	// InstalledRev is the Revision the status was judged against, and
	// UpstreamRev the upstream Revision found (git skills).
	InstalledRev string `json:"installed_rev,omitempty"`
	UpstreamRev  string `json:"upstream_rev,omitempty"`
	// Error is the lookup failure behind "error" or "untracked".
	Error string `json:"error,omitempty"`
}

// Self is the self-update status: the fields of `upgrade --check --json`'s
// data, plus the lookup's error.
type Self struct {
	Current    string `json:"current"`
	Latest     string `json:"latest,omitempty"`
	Available  bool   `json:"available"`
	Eligible   bool   `json:"eligible"`
	Method     string `json:"method"`
	Executable string `json:"executable,omitempty"`
	// Error is why the latest release could not be looked up; Available is
	// then false.
	Error string `json:"error,omitempty"`
}

// Problem is one entry of File.Errors.
type Problem struct {
	// Code is the skill's status ("error" or "untracked") or "self_check".
	Code    string `json:"code"`
	SkillID string `json:"skill_id,omitempty"`
	Message string `json:"message"`
}

// New returns an empty File: never checked, nothing to show.
func New() *File {
	return &File{SchemaVersion: SchemaVersion, Skills: []Skill{}, Errors: []Problem{}}
}

// Path is the cache's path in home.
func Path(home string) string { return filepath.Join(home, FileName) }

// Load reads the cache in home. A missing file is (nil, nil): no refresh ever
// ran. A file that cannot be decoded is an error.
func Load(home string) (*File, error) {
	b, err := os.ReadFile(Path(home))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	f := New()
	if err := json.Unmarshal(b, f); err != nil {
		return nil, fmt.Errorf("read %s: %w", Path(home), err)
	}
	if f.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("read %s: unknown schema_version %d", Path(home), f.SchemaVersion)
	}
	if f.Skills == nil {
		f.Skills = []Skill{}
	}
	f.Recount()
	return f, nil
}

// Save recounts f and replaces the cache in home atomically, so a reader
// (a GUI watching the file) never sees a partial file. Home must exist.
func Save(home string, f *File) error {
	f.Recount()
	b, err := Marshal(f)
	if err != nil {
		return err
	}
	return store.WriteFileAtomic(Path(home), b, 0o644)
}

// Marshal is f as Save writes it: indented JSON with a trailing newline.
func Marshal(f *File) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(f); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Recount derives Updates, Errors and Badge from Skills and Self, and
// normalizes the times to UTC whole seconds.
func (f *File) Recount() {
	f.SchemaVersion = SchemaVersion
	f.CheckedAt = Stamp(f.CheckedAt)
	f.NextDueAt = Stamp(f.NextDueAt)
	f.Updates = 0
	f.Errors = []Problem{}
	for _, s := range f.Skills {
		switch s.Status {
		case SkillUpdateAvailable:
			f.Updates++
		case SkillError, SkillUntracked:
			f.Errors = append(f.Errors, Problem{Code: s.Status, SkillID: s.ID, Message: s.Error})
		}
	}
	if f.Self != nil && f.Self.Error != "" {
		f.Errors = append(f.Errors, Problem{Code: CodeSelfCheck, Message: f.Self.Error})
	}
	f.Badge = f.Updates > 0 || (f.Self != nil && f.Self.Available)
}

// Stamp is t in UTC, truncated to whole seconds (the zero time stays zero).
func Stamp(t time.Time) time.Time {
	if t.IsZero() {
		return t
	}
	return t.UTC().Truncate(time.Second)
}

// Skill returns the row for id, if any.
func (f *File) Skill(id string) (Skill, bool) {
	for _, s := range f.Skills {
		if s.ID == id {
			return s, true
		}
	}
	return Skill{}, false
}

// SetSkill replaces the row for s.ID, or appends it.
func (f *File) SetSkill(s Skill) {
	for i := range f.Skills {
		if f.Skills[i].ID == s.ID {
			f.Skills[i] = s
			return
		}
	}
	f.Skills = append(f.Skills, s)
}

// Due reports whether a scheduled refresh (`refresh --if-due`) should run at
// now. It never is while refresh is disabled. Otherwise it is due when no
// refresh ever ran (f is nil or never checked), at or after NextDueAt, at or
// after CheckedAt plus the current interval (the interval may have been
// shortened since), or when CheckedAt is in the future (the clock went back).
//
// Which skillm wrote the cache does not matter: several binaries may share
// one Home (the app's bundled CLI and a terminal install, at different
// versions), and each reader re-judges the self entry for itself offline.
func Due(f *File, now time.Time, enabled bool, interval time.Duration) bool {
	if !enabled {
		return false
	}
	if f == nil || f.CheckedAt.IsZero() {
		return true
	}
	if now.Before(f.CheckedAt) {
		return true
	}
	return !now.Before(DueAt(f, interval))
}

// DueAt is when the next scheduled refresh is due: NextDueAt, or CheckedAt
// plus interval when that is sooner (the interval was shortened since). It
// is the zero time (due now) when no refresh ever ran.
func DueAt(f *File, interval time.Duration) time.Time {
	if f == nil || f.CheckedAt.IsZero() {
		return time.Time{}
	}
	due := f.CheckedAt.Add(interval)
	if !f.NextDueAt.IsZero() && f.NextDueAt.Before(due) {
		due = f.NextDueAt
	}
	return due
}

// Stale reports whether the cache is older than the refresh interval at now
// (or was never written, or is dated in the future).
func Stale(f *File, now time.Time, interval time.Duration) bool {
	if f == nil || f.CheckedAt.IsZero() || now.Before(f.CheckedAt) {
		return true
	}
	return now.Sub(f.CheckedAt) >= interval
}
