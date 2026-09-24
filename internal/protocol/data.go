package protocol

import (
	"time"

	"github.com/ultrakorne/skillm/internal/core"
)

// VersionData is `skillm version --json`'s data.
type VersionData struct {
	// Version is the build version as stamped at link time ("dev" for an
	// unstamped source build).
	Version string `json:"version"`
	// APIVersion is APIVersion; a GUI refuses a CLI whose value it does not
	// know.
	APIVersion int `json:"api_version"`
	// Capabilities lists, sorted, the commands that have a JSON mode (by
	// command path, e.g. "check") and the protocol features ("events").
	Capabilities []string `json:"capabilities"`
}

// ListData is `skillm list --json`'s data: every registered skill, in
// Registry order.
type ListData struct {
	Skills []ListedSkill `json:"skills"`
}

// ListedSkill is one registered skill and where it is installed.
type ListedSkill struct {
	ID string `json:"id"`
	// Kind is "git" or "local".
	Kind   string `json:"kind"`
	Source string `json:"source"`
	// Subpath is the skill's directory inside a git Source.
	Subpath string `json:"subpath,omitempty"`
	// SourceLabel is the Source as the CLI shows it: the git URL with
	// "//subpath" appended for a catalog repo, or the local path.
	SourceLabel string `json:"source_label"`
	Ref         string `json:"ref,omitempty"`
	Revision    string `json:"revision,omitempty"`
	// InstalledAt is RFC 3339 UTC; omitted when unknown.
	InstalledAt string    `json:"installed_at,omitempty"`
	Installs    []Install `json:"installs"`
}

// Install is one place a skill is installed, read live from disk.
type Install struct {
	// Scope is "global" or "local".
	Scope string `json:"scope"`
	// Root is a Local install's project root; omitted for Global.
	Root string `json:"root,omitempty"`
	// Path is where the install's canonical copy lives.
	Path string `json:"path"`
	// Agents are the enabled agents the install serves, sorted.
	Agents []string `json:"agents"`
	// Recorded says the Registry records a canonical copy here.
	Recorded bool `json:"recorded"`
	// Exists says the recorded canonical copy is on disk.
	Exists bool `json:"exists"`
}

// NewListData converts core.List's result.
func NewListData(res core.ListResult) ListData {
	out := ListData{Skills: make([]ListedSkill, 0, len(res.Skills))}
	for _, s := range res.Skills {
		ls := ListedSkill{
			ID:          s.ID,
			Kind:        s.Kind,
			Source:      s.Source,
			Subpath:     s.Subpath,
			SourceLabel: s.SourceLabel(),
			Ref:         s.Ref,
			Revision:    s.Revision,
			InstalledAt: Time(s.InstalledAt),
			Installs:    make([]Install, 0, len(s.Installs)),
		}
		for _, in := range s.Installs {
			agents := in.Agents
			if agents == nil {
				agents = []string{}
			}
			ls.Installs = append(ls.Installs, Install{
				Scope:    string(in.Scope),
				Root:     in.Root,
				Path:     in.Path,
				Agents:   agents,
				Recorded: in.Recorded,
				Exists:   in.Exists,
			})
		}
		out.Skills = append(out.Skills, ls)
	}
	return out
}

// CheckData is `skillm check --json`'s data: every registered skill's
// upstream status, in Registry order.
type CheckData struct {
	Skills []CheckedSkill `json:"skills"`
	// Updates counts the skills with status "update_available".
	Updates int `json:"updates"`
}

// CheckedSkill is one skill's upstream status.
type CheckedSkill struct {
	ID string `json:"id"`
	// Kind is "git" or "local".
	Kind string `json:"kind"`
	// Status is "up_to_date", "update_available", "untracked" (the skill's
	// directory is gone upstream), "local" (no upstream to check) or "error"
	// (the upstream could not be read; Error says why).
	Status       string `json:"status"`
	InstalledRev string `json:"installed_rev,omitempty"`
	UpstreamRev  string `json:"upstream_rev,omitempty"`
	// Error is the lookup failure behind "untracked" or "error".
	Error string `json:"error,omitempty"`
}

// NewCheckData converts core.Check's result.
func NewCheckData(res core.CheckResult) CheckData {
	out := CheckData{Skills: make([]CheckedSkill, 0, len(res.Skills))}
	for _, s := range res.Skills {
		cs := CheckedSkill{
			ID:           s.ID,
			Kind:         s.Kind,
			Status:       string(s.Status),
			InstalledRev: s.InstalledRev,
			UpstreamRev:  s.UpstreamRev,
		}
		if s.Err != nil {
			cs.Error = s.Err.Error()
		}
		if s.Status == core.StatusUpdateAvailable {
			out.Updates++
		}
		out.Skills = append(out.Skills, cs)
	}
	return out
}

// Time formats t as RFC 3339 in UTC, whole seconds; the zero time is "".
func Time(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
