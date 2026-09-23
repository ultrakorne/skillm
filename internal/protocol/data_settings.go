package protocol

import (
	"github.com/ultrakorne/skillm/internal/config"
	"github.com/ultrakorne/skillm/internal/core"
)

// The data of the commands a GUI sets itself up with: source inspect (the
// Add Skill picker), agent ls/set and config get/set (Settings). Every list
// is present (never null).

// InspectData is `skillm source inspect --json`'s data: a Source read once,
// with the skills found in it.
type InspectData struct {
	// Source is the Source as skillm records it: the canonical git remote
	// (a GitHub owner/repo shorthand expanded) or the absolute directory of
	// a local Source.
	Source string `json:"source"`
	// Kind is "git" or "local".
	Kind string `json:"kind"`
	// Ref is the branch or tag an install records for updates: the --ref
	// asked for, or the repository's default branch. Omitted for local.
	Ref string `json:"ref,omitempty"`
	// Commit is the full SHA that was inspected. An install passes it as
	// --commit, so it copies exactly what was shown. Omitted for local.
	Commit string `json:"commit,omitempty"`
	// Skills are the skills found, in discovery order.
	Skills []InspectedSkill `json:"skills"`
}

// InspectedSkill is one skill found in a Source.
type InspectedSkill struct {
	// ID is the Skill ID it installs under (its directory name).
	ID string `json:"id"`
	// Name and Description come from its SKILL.md frontmatter; either may
	// be empty.
	Name        string `json:"name"`
	Description string `json:"description"`
	// Path locates it in the Source: the repo-relative directory with
	// forward slashes ("" for the repository root) for git, the absolute
	// directory for local.
	Path string `json:"path"`
}

// NewInspectData converts core.Inspect's result.
func NewInspectData(insp *core.Inspection) InspectData {
	out := InspectData{
		Source: insp.Source,
		Kind:   insp.Kind,
		Ref:    insp.Ref,
		Commit: insp.Commit,
		Skills: make([]InspectedSkill, 0, len(insp.Skills)),
	}
	for _, s := range insp.Skills {
		out.Skills = append(out.Skills, InspectedSkill{ID: s.ID, Name: s.Name, Description: s.Description, Path: s.Path})
	}
	return out
}

// AgentsData is `skillm agent ls --json`'s data: every agent defined in
// config.toml, sorted by name.
type AgentsData struct {
	Agents []Agent `json:"agents"`
}

// Agent is one agent defined in config.toml.
type Agent struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	// Global and Local are its skill-folder locations as config.toml writes
	// them (Global may start with "~", Local is relative to a project).
	// Omitted when the agent has no folder at that scope.
	Global string `json:"global,omitempty"`
	Local  string `json:"local,omitempty"`
}

// NewAgentsData converts core.Agents' result.
func NewAgentsData(agents []core.AgentInfo) AgentsData {
	out := AgentsData{Agents: make([]Agent, 0, len(agents))}
	for _, a := range agents {
		out.Agents = append(out.Agents, Agent{Name: a.Name, Enabled: a.Enabled, Global: a.Global, Local: a.Local})
	}
	return out
}

// AgentsSetData is `skillm agent set --json`'s data.
type AgentsSetData struct {
	// Enabled lists the enabled agents afterwards, sorted.
	Enabled []string `json:"enabled"`
	// Changes has one entry per agent whose state changed: the enabled ones
	// first, then the disabled ones, each sorted by name. Empty means
	// nothing changed and nothing was written.
	Changes []AgentChange `json:"changes"`
}

// AgentChange is what agent set did for one agent.
type AgentChange struct {
	Name string `json:"name"`
	// Enabled is the agent's new state.
	Enabled bool `json:"enabled"`
	// Skills are the skills it was linked to (enabled) or unlinked from
	// (disabled), sorted.
	Skills []string `json:"skills"`
	// Places are where that happened: "global", or "local: <project root>".
	Places []string `json:"places"`
	// Warnings are the spots the reconcile skipped (a partial success: those
	// links were left as they were).
	Warnings []string `json:"warnings"`
}

// NewAgentsSetData converts core.SetAgents' result.
func NewAgentsSetData(res core.AgentsResult) AgentsSetData {
	out := AgentsSetData{Enabled: nonNil(res.Enabled), Changes: make([]AgentChange, 0, len(res.Changes))}
	for _, c := range res.Changes {
		out.Changes = append(out.Changes, AgentChange{
			Name:     c.Name,
			Enabled:  c.Enabled,
			Skills:   nonNil(c.Skills),
			Places:   nonNil(c.Places),
			Warnings: errTexts(c.Warnings),
		})
	}
	return out
}

// ConfigData is `skillm config get --json` and `skillm config set --json`'s
// data: the effective value of every setting (the default where config.toml
// has none), whichever keys were asked for.
type ConfigData struct {
	Refresh RefreshSettings `json:"refresh"`
}

// RefreshSettings is the [refresh] table: the scheduled update check.
type RefreshSettings struct {
	// Enabled is refresh.enabled.
	Enabled bool `json:"enabled"`
	// IntervalHours is refresh.interval_hours.
	IntervalHours int `json:"interval_hours"`
}

// NewConfigData reads the settings out of c.
func NewConfigData(c *config.Config) ConfigData {
	return ConfigData{Refresh: RefreshSettings{
		Enabled:       c.RefreshEnabled(),
		IntervalHours: c.RefreshIntervalHours(),
	}}
}
