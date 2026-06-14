// Package config loads and saves skillm's user-owned preferences file,
// ~/.skillm/config.toml. The file is hand-editable; skillm reads it and avoids
// rewriting it unless explicitly asked (e.g. via `skillm agent`). When the file
// is absent, callers get the built-in defaults rather than an error so that a
// fresh Home works out of the box without writing anything.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	toml "github.com/pelletier/go-toml/v2"
)

// FileName is the base name of the config file within Home.
const FileName = "config.toml"

// Config mirrors ~/.skillm/config.toml. Fields map to the snake_case TOML keys
// described in the PLAN (§2).
type Config struct {
	// Agents is the set of Enabled agents that Links are applied to. Managed
	// interactively via `skillm agent`.
	Agents []string `toml:"agents"`
	// DefaultScope is the scope used by `link`/`add` when neither --global nor
	// --local is given. Expected values: "global" or "local".
	DefaultScope string `toml:"default_scope"`
}

// Default returns a freshly allocated Config holding skillm's built-in
// defaults: both supported agents enabled and the global scope as default.
func Default() *Config {
	return &Config{
		Agents:       []string{"claude", "codex"},
		DefaultScope: "global",
	}
}

// Path returns the absolute path to the config file inside homeDir.
func Path(homeDir string) string {
	return filepath.Join(homeDir, FileName)
}

// Load reads the config file from homeDir. If the file does not exist it
// returns Default() and a nil error: config is user-owned, so an absent file is
// not an error and must never be silently written. Any other I/O or parse
// error is returned.
func Load(homeDir string) (*Config, error) {
	path := Path(homeDir)

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Default(), nil
		}
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}

	c := Default()
	if err := toml.Unmarshal(data, c); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	return c, nil
}

// Save writes c to the config file in homeDir, creating homeDir if necessary.
// It writes the whole file (overwriting any existing one), so callers should
// only invoke it in response to an explicit user action such as `skillm agent`.
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
