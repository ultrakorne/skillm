// Package agentdir maps the supported AI agents to the on-disk skill folders
// they read, at each Scope (Global or Local). It is pure path computation: it
// performs no filesystem mutation and only resolves the conventional skill
// folder locations defined in the PLAN folder table.
//
//	| Scope  | Claude                      | Codex                      |
//	|--------|-----------------------------|----------------------------|
//	| Global | ~/.claude/skills/<id>       | ~/.codex/skills/<id>       |
//	| Local  | <cwd>/.claude/skills/<id>   | <cwd>/.codex/skills/<id>   |
package agentdir

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Scope is where a skill is made available to an agent.
type Scope int

const (
	// Global makes a skill available to the agent everywhere, via the agent's
	// user-level skill folder (under the user's home directory).
	Global Scope = iota
	// Local makes a skill available only within one project, via the agent's
	// project-level skill folder (under the current working directory).
	Local
)

// String returns the canonical lowercase name of the scope, matching the
// values used by the --global/--local flags.
func (s Scope) String() string {
	switch s {
	case Global:
		return "global"
	case Local:
		return "local"
	default:
		return fmt.Sprintf("Scope(%d)", int(s))
	}
}

// Agent is a tool that consumes skills by reading them from a skill folder.
// Name is the stable identifier used in config.toml's agents list (e.g.
// "claude", "codex"); dir is the agent's conventional dotfolder name (e.g.
// ".claude") relative to either the user's home (Global) or the cwd (Local).
type Agent struct {
	// Name is the lowercase identifier persisted in config.agents.
	Name string
	// dir is the agent's conventional dotfolder (e.g. ".claude").
	dir string
}

// registry is the ordered set of agents skillm supports. Order is stable so
// All and Enabled return agents deterministically.
var registry = []Agent{
	{Name: "claude", dir: ".claude"},
	{Name: "codex", dir: ".codex"},
}

// All returns every agent skillm supports, in a stable order. The returned
// slice is a copy; callers may not mutate the registry through it.
func All() []Agent {
	out := make([]Agent, len(registry))
	copy(out, registry)
	return out
}

// Enabled returns the supported agents whose Name appears in names, preserving
// the registry's stable order. Names that do not match a supported agent are
// ignored. Typical use: agentdir.Enabled(cfg.Agents).
func Enabled(names []string) []Agent {
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[n] = true
	}
	var out []Agent
	for _, a := range registry {
		if want[a.Name] {
			out = append(out, a)
		}
	}
	return out
}

// SkillsFolder returns the absolute skill folder for the agent at the given
// scope. For Global it is <home>/<agent-dir>/skills, where home is the user's
// home directory (with a leading "~" expanded). For Local it is
// <cwd>/<agent-dir>/skills. The path is cleaned but not created.
//
// cwd is the project root used for Local scope; it is ignored for Global.
func SkillsFolder(a Agent, scope Scope, cwd string) string {
	switch scope {
	case Local:
		return filepath.Join(cwd, a.dir, "skills")
	default: // Global
		return filepath.Join(homeDir(), a.dir, "skills")
	}
}

// LinkPath returns the absolute path of the symlink for skill id inside the
// agent's skill folder at the given scope — i.e. SkillsFolder(a, scope, cwd)
// joined with id. This is where the symlink into Home is created.
func LinkPath(a Agent, scope Scope, cwd, id string) string {
	return filepath.Join(SkillsFolder(a, scope, cwd), id)
}

// ParseScope maps a scope string to a Scope. It accepts "global"/"g" → Global
// and "local"/"l" → Local (case-insensitive, trimmed). Any other value,
// including the empty string, is an error.
func ParseScope(s string) (Scope, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "global", "g":
		return Global, nil
	case "local", "l":
		return Local, nil
	default:
		return Global, fmt.Errorf("invalid scope %q: want \"global\" or \"local\"", s)
	}
}

// homeDir resolves the user's home directory, expanding to "~" only as a last
// resort so paths stay usable even if the lookup fails.
func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return h
	}
	return "~"
}
