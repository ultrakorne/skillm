// Package agentdir resolves an agent's on-disk skill folder at each Scope
// (Global or Local). It is pure path computation: it performs no filesystem
// mutation and holds no agent catalog of its own. The agents — and the
// per-scope locations below — come from config.toml (see the config package),
// so supporting a new agent is a config edit, not a source change. The seeded
// defaults are:
//
//	| Scope  | Claude                      | Codex                      |
//	|--------|-----------------------------|----------------------------|
//	| Global | ~/.claude/skills/<id>       | ~/.codex/skills/<id>       |
//	| Local  | <base>/.claude/skills/<id>  | <base>/.codex/skills/<id>  |
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

// Agent is a tool that consumes skills by reading them from a skill folder. It
// is built from a config.toml definition: Name is the agent's identifier, and
// Global/Local are the skill-folder location templates it reads at each Scope.
// An empty Global or Local means the agent has no folder at that Scope.
type Agent struct {
	// Name is the agent's identifier (the [agents.<name>] key).
	Name string
	// Global is the user-level location template; a leading "~" expands to the
	// user's home, and a relative value is rooted at home. Empty = no Global folder.
	Global string
	// Local is the project-level location template, relative to the project
	// base. Empty = no Local folder.
	Local string
}

// Supports reports whether the agent defines a skill folder at scope.
func (a Agent) Supports(scope Scope) bool {
	if scope == Local {
		return a.Local != ""
	}
	return a.Global != ""
}

// SkillsFolder returns the absolute skill folder for the agent at the given
// scope and true, or ("", false) when the agent defines no folder for that
// scope. For Global it expands a leading "~" to the user's home and roots a
// relative location at home. For Local it joins the agent's relative location
// to base (the project root). The path is cleaned but not created.
//
// base is the project root used for Local scope; it is ignored for Global.
func SkillsFolder(a Agent, scope Scope, base string) (string, bool) {
	switch scope {
	case Local:
		if a.Local == "" {
			return "", false
		}
		return filepath.Join(base, a.Local), true
	default: // Global
		if a.Global == "" {
			return "", false
		}
		return expandGlobal(a.Global), true
	}
}

// LinkPath returns the absolute path of the symlink for skill id inside the
// agent's skill folder at the given scope — SkillsFolder joined with id — and
// true, or ("", false) when the agent has no folder at that scope.
func LinkPath(a Agent, scope Scope, base, id string) (string, bool) {
	folder, ok := SkillsFolder(a, scope, base)
	if !ok {
		return "", false
	}
	return filepath.Join(folder, id), true
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

// expandGlobal resolves a Global location template to an absolute path: a
// leading "~" (or "~/") expands to the user's home, an absolute path is taken
// as-is, and a relative path is rooted at the user's home. The result is cleaned.
func expandGlobal(p string) string {
	switch {
	case p == "~":
		return homeDir()
	case strings.HasPrefix(p, "~/"):
		return filepath.Join(homeDir(), p[2:])
	case filepath.IsAbs(p):
		return filepath.Clean(p)
	default:
		return filepath.Join(homeDir(), p)
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
