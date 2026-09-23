package protocol

import (
	"github.com/ultrakorne/skillm/internal/core"
	"github.com/ultrakorne/skillm/internal/status"
)

// StatusData is `skillm status --json`'s data: the refresh cache
// (<home>/status.json, whose fields it carries at the top level, including
// the cache's own schema_version) plus whether it is stale. Its self entry
// is judged for the running skillm, so it may differ from the file's when
// the app was upgraded since the last refresh.
type StatusData struct {
	status.File
	// Stale reports that the cache is older than the refresh interval (or
	// no refresh ever ran).
	Stale bool `json:"stale"`
}

// RefreshData is `skillm refresh --json`'s data: the cache after the run.
type RefreshData struct {
	StatusData
	// Refreshed reports that a check ran and rewrote the cache; false for
	// `--if-due` when no check was due (the data is then the cache as it was).
	Refreshed bool `json:"refreshed"`
}

// NewStatusData converts core.ReadStatus's result.
func NewStatusData(res core.StatusResult) StatusData {
	f := res.Status
	if f == nil {
		f = status.New()
	}
	return StatusData{File: *f, Stale: res.Stale}
}

// NewRefreshData converts core.Refresh's result.
func NewRefreshData(res core.StatusResult) RefreshData {
	return RefreshData{StatusData: NewStatusData(res), Refreshed: res.Refreshed}
}
