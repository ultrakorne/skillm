// Package config loads and saves skillm's user-owned preferences file,
// ~/.skillm/config.toml. The file is hand-editable and is the single source of
// truth for which Agents skillm knows about and *where* each one reads skills
// at every Scope. skillm seeds it with the built-in defaults the first time
// Home is created (see EnsureExists); when the file is absent, callers get
// those same defaults rather than an error, so "what is written" equals "what
// you fall back to". skillm otherwise avoids rewriting the file — only
// `skillm agent` does, to toggle the per-agent enabled flags.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/ultrakorne/skillm/internal/agentdir"
)

// FileName is the base name of the config file within Home.
const FileName = "config.toml"

// AgentDef is one agent's definition in config.toml: whether it is enabled and
// the skill-folder location it reads at each Scope. Both Global and Local are
// optional (at least one should be set); an empty one means the agent has no
// folder at that Scope and is skipped when linking there. Global expands a
// leading "~" to the user's home; Local is relative to the project base. skillm
// always appends the Skill ID to whichever location a Scope selects.
type AgentDef struct {
	// Enabled reports whether Links are applied to this agent. It is a pointer
	// so an omitted key reads as enabled (see IsEnabled) rather than false.
	Enabled *bool `toml:"enabled,omitempty"`
	// Global is the agent's user-level skill-folder location (may start with ~).
	Global string `toml:"global,omitempty"`
	// Local is the agent's project-level skill-folder location (relative).
	Local string `toml:"local,omitempty"`
}

// IsEnabled reports whether the agent is enabled. An omitted enabled key
// (nil pointer) defaults to true: if you bothered to define an agent, it is on
// unless you explicitly disable it.
func (d AgentDef) IsEnabled() bool { return d.Enabled == nil || *d.Enabled }

// Config mirrors ~/.skillm/config.toml. Agents maps each agent's name to its
// definition; it is the catalog skillm draws on for linking and for the
// `skillm agent` picker.
type Config struct {
	// Agents is the set of defined agents keyed by name (the [agents.<name>]
	// tables in config.toml).
	Agents map[string]AgentDef `toml:"agents"`
}

// boolPtr returns a pointer to b, for setting AgentDef.Enabled.
func boolPtr(b bool) *bool { return &b }

// Default returns a freshly allocated Config holding skillm's built-in
// defaults: the Claude and Codex agents, both enabled, with their conventional
// skill-folder locations. This value seeds a fresh config.toml and is also the
// fallback Load returns when the file is absent.
func Default() *Config {
	return &Config{
		Agents: map[string]AgentDef{
			"claude": {Enabled: boolPtr(true), Global: "~/.claude/skills", Local: ".claude/skills"},
			"codex":  {Enabled: boolPtr(true), Global: "~/.codex/skills", Local: ".codex/skills"},
		},
	}
}

// Path returns the absolute path to the config file inside homeDir.
func Path(homeDir string) string {
	return filepath.Join(homeDir, FileName)
}

// Load reads the config file from homeDir. If the file does not exist it
// returns Default() and a nil error: config is user-owned, so an absent file is
// not an error and must never be silently written here (see EnsureExists for
// seeding). When the file exists it is authoritative — it is parsed on its own,
// not merged onto Default(), so an agent omitted from the file is genuinely
// absent. Any I/O or parse error is returned.
func Load(homeDir string) (*Config, error) {
	path := Path(homeDir)

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Default(), nil
		}
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}

	var c Config
	if err := toml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	return &c, nil
}

// Save writes c to the config file in homeDir, creating homeDir if necessary.
// It writes the whole file (overwriting any existing one and dropping any
// hand-written comments), so callers should only invoke it in response to an
// explicit user action such as `skillm agent`.
func Save(homeDir string, c *Config) error {
	if c == nil {
		return errors.New("config: cannot save nil config")
	}

	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		return fmt.Errorf("config: create home %s: %w", homeDir, err)
	}

	data, err := toml.Marshal(c)
	if err != nil {
		return fmt.Errorf("config: marshal: %w", err)
	}

	path := Path(homeDir)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("config: write %s: %w", path, err)
	}
	return nil
}

// EnsureExists writes the default config to homeDir when no config file is
// present, making it the visible source of truth from the first run. It never
// overwrites an existing file, so hand edits survive. It is meant to be called
// once Home is bootstrapped (alongside store.EnsureHome).
func EnsureExists(homeDir string) error {
	path := Path(homeDir)
	switch _, err := os.Stat(path); {
	case err == nil:
		return nil // present — leave it untouched
	case errors.Is(err, fs.ErrNotExist):
		return Save(homeDir, Default())
	default:
		return fmt.Errorf("config: stat %s: %w", path, err)
	}
}

// AllAgents returns every defined agent as an agentdir.Agent, sorted by name so
// iteration and output are deterministic regardless of TOML/map ordering.
func (c *Config) AllAgents() []agentdir.Agent { return c.toAgents(false) }

// EnabledAgents returns the enabled defined agents as agentdir.Agents, sorted
// by name. This is the set Links are applied to.
func (c *Config) EnabledAgents() []agentdir.Agent { return c.toAgents(true) }

// AgentNames returns the names of every defined agent, sorted.
func (c *Config) AgentNames() []string { return c.names(false) }

// EnabledNames returns the names of the enabled defined agents, sorted.
func (c *Config) EnabledNames() []string { return c.names(true) }

// SetEnabled updates every defined agent's enabled flag from names: agents
// whose name is in names are enabled, the rest disabled. Each agent's locations
// are left intact, so toggling never loses a definition. This is how
// `skillm agent` writes a new selection back.
func (c *Config) SetEnabled(names []string) {
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[n] = true
	}
	for name, def := range c.Agents {
		def.Enabled = boolPtr(want[name])
		c.Agents[name] = def
	}
}

// toAgents converts the definition map into sorted agentdir.Agents, optionally
// keeping only enabled ones.
func (c *Config) toAgents(enabledOnly bool) []agentdir.Agent {
	names := c.names(enabledOnly)
	out := make([]agentdir.Agent, 0, len(names))
	for _, name := range names {
		d := c.Agents[name]
		out = append(out, agentdir.Agent{Name: name, Global: d.Global, Local: d.Local})
	}
	return out
}

// names returns the sorted agent names, optionally filtered to enabled ones.
func (c *Config) names(enabledOnly bool) []string {
	names := make([]string, 0, len(c.Agents))
	for name, def := range c.Agents {
		if enabledOnly && !def.IsEnabled() {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
