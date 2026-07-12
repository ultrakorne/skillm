// Package agentdir resolves an agent's on-disk skill folder at each Scope
// (Global or Local). It is pure path computation: it performs no filesystem
// mutation and holds no agent catalog of its own. The agents — and the
// per-scope locations below — come from config.toml (see the config package),
// so supporting a new agent is a config edit, not a source change. The seeded
// defaults are:
//
//	| Scope  | claude                      | agents                      |
//	|--------|-----------------------------|-----------------------------|
//	| Global | ~/.claude/skills/<id>       | ~/.agents/skills/<id>       |
//	| Local  | <base>/.claude/skills/<id>  | <base>/.agents/skills/<id>  |
//
// The "agents" entry is the cross-agent .agents/skills convention, read by
// Codex, Cursor, Amp, Gemini CLI and others (Codex does not read .codex/skills).
package agentdir

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// CanonicalLocalRel is the relative directory that holds the real files of an
// install at either scope: the cross-agent ".agents/skills" convention (the
// same canonical store vercel's skills CLI uses). Rooted at a project base it
// is the Local store; rooted at the user's home it is the Global store. Agents
// whose folder at a scope is exactly this directory read the copies natively;
// every other agent's folder gets a symlink into it. It is also the layout
// skills-lock.json describes, which is what makes skillm's local installs
// interoperable.
const CanonicalLocalRel = ".agents/skills"

// CanonicalLocalDir returns the canonical local skill store for a project
// base: <base>/.agents/skills.
func CanonicalLocalDir(base string) string {
	return filepath.Join(base, filepath.FromSlash(CanonicalLocalRel))
}

// CanonicalSkillDir returns the canonical on-disk location of one skill's
// Local install: <base>/.agents/skills/<id>.
func CanonicalSkillDir(base, id string) string {
	return filepath.Join(CanonicalLocalDir(base), id)
}

// CanonicalGlobalDir returns the canonical global skill store: ~/.agents/skills,
// the cross-agent convention rooted at the user's home.
func CanonicalGlobalDir() string {
	return CanonicalLocalDir(homeDir())
}

// CanonicalDirAt returns the canonical skill store for the given scope: the
// project store <base>/.agents/skills at Local, ~/.agents/skills at Global
// (base is ignored).
func CanonicalDirAt(scope Scope, base string) string {
	if scope == Local {
		return CanonicalLocalDir(base)
	}
	return CanonicalGlobalDir()
}

// CanonicalSkillDirAt returns the canonical on-disk location of one skill's
// install at the given scope (see CanonicalDirAt).
func CanonicalSkillDirAt(scope Scope, base, id string) string {
	return filepath.Join(CanonicalDirAt(scope, base), id)
}

// IsCanonicalLocal reports whether the agent's Local folder IS the canonical
// store — such an agent is served directly by the installed copy and never
// needs a link.
func IsCanonicalLocal(a Agent) bool {
	return a.Local != "" && filepath.ToSlash(filepath.Clean(filepath.FromSlash(a.Local))) == CanonicalLocalRel
}

// IsCanonicalGlobal reports whether the agent's Global folder IS the canonical
// global store (~/.agents/skills) — such an agent is served directly by the
// installed copy and never needs a link.
func IsCanonicalGlobal(a Agent) bool {
	return a.Global != "" && samePath(expandGlobal(a.Global), CanonicalGlobalDir())
}

// IsCanonicalAt reports whether the agent's folder at scope is the canonical
// store for that scope (see IsCanonicalLocal / IsCanonicalGlobal).
func IsCanonicalAt(a Agent, scope Scope) bool {
	if scope == Local {
		return IsCanonicalLocal(a)
	}
	return IsCanonicalGlobal(a)
}

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

// LocalAliasesGlobal reports whether the agent's Local skill folder at base
// resolves to the same directory as its Global folder. When it does, the agent
// has no distinct local scope at base: "local" would mean exactly the folder
// "global" already means (the canonical case is base == home, where e.g.
// <base>/.claude/skills is literally ~/.claude/skills). Callers treat such an
// agent as having no usable local scope there.
//
// It returns false unless the agent defines BOTH a Global and a Local folder —
// without both there is nothing to alias. The comparison is by absolute,
// cleaned path, case-insensitively on Windows and macOS (whose filesystems are
// case-insensitive) so paths differing only in case still count as the same
// folder; on Linux it is case-sensitive.
func LocalAliasesGlobal(a Agent, base string) bool {
	local, okL := SkillsFolder(a, Local, base)
	global, okG := SkillsFolder(a, Global, base)
	if !okL || !okG {
		return false
	}
	return samePath(local, global)
}

// samePath reports whether a and b denote the same directory, comparing their
// absolute, cleaned forms. On case-insensitive platforms (Windows, macOS) the
// comparison folds case, matching how those filesystems resolve names; on Linux
// it is exact. It does not resolve symlinks or 8.3 short names — the paths it
// compares are built from the same templates, so a plain form suffices.
func samePath(a, b string) bool {
	a = absClean(a)
	b = absClean(b)
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// absClean returns p in absolute, cleaned form, falling back to a plain Clean
// when the working directory cannot be resolved (filepath.Abs already Cleans).
func absClean(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return filepath.Clean(p)
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
