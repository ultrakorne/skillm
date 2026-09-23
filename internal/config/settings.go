package config

import (
	"fmt"
	"strconv"
	"strings"
)

// The settings `skillm config get/set` reads and writes: the keys a GUI needs,
// not the whole file. Agents are changed through `skillm agent`, never here.

// Setting keys, as `skillm config get/set` names them.
const (
	// KeyRefreshEnabled turns the scheduled update check on or off.
	KeyRefreshEnabled = "refresh.enabled"
	// KeyRefreshIntervalHours is the time between scheduled checks, in hours.
	KeyRefreshIntervalHours = "refresh.interval_hours"
)

// Defaults and bounds of the [refresh] settings.
const (
	DefaultRefreshEnabled       = true
	DefaultRefreshIntervalHours = 24
	// MinRefreshIntervalHours and MaxRefreshIntervalHours bound
	// refresh.interval_hours: at most one check an hour, at least one every
	// 30 days.
	MinRefreshIntervalHours = 1
	MaxRefreshIntervalHours = 720
)

// Refresh is the [refresh] table of config.toml: the scheduled update check a
// GUI (or a timer running `skillm refresh --if-due`) performs. A missing key
// means its default, so a config.toml written before the table existed keeps
// working unchanged.
type Refresh struct {
	// Enabled turns the scheduled check on or off (default true).
	Enabled *bool `toml:"enabled,omitempty"`
	// IntervalHours is the time between checks in hours (default 24).
	IntervalHours *int `toml:"interval_hours,omitempty"`
}

func intPtr(i int) *int { return &i }

// Keys returns every setting key, in the order `skillm config get` lists them.
func Keys() []string {
	return []string{KeyRefreshEnabled, KeyRefreshIntervalHours}
}

// RefreshEnabled reports whether the scheduled update check is on: the
// refresh.enabled key, or true when it is absent.
func (c *Config) RefreshEnabled() bool {
	if c.Refresh == nil || c.Refresh.Enabled == nil {
		return DefaultRefreshEnabled
	}
	return *c.Refresh.Enabled
}

// RefreshIntervalHours is the time between scheduled checks in hours: the
// refresh.interval_hours key, or 24 when it is absent or outside
// [MinRefreshIntervalHours, MaxRefreshIntervalHours] (a hand edit to 0 or a
// negative number must not make a GUI check in a loop).
func (c *Config) RefreshIntervalHours() int {
	if c.Refresh == nil || c.Refresh.IntervalHours == nil {
		return DefaultRefreshIntervalHours
	}
	h := *c.Refresh.IntervalHours
	if h < MinRefreshIntervalHours || h > MaxRefreshIntervalHours {
		return DefaultRefreshIntervalHours
	}
	return h
}

// UnknownKeyError means a setting key is not one of Keys().
type UnknownKeyError struct {
	Key string
}

func (e *UnknownKeyError) Error() string {
	return fmt.Sprintf("unknown setting %q; known settings: %s", e.Key, strings.Join(Keys(), ", "))
}

// InvalidValueError means a value cannot be stored under its key.
type InvalidValueError struct {
	Key, Value string
	// Want describes the values the key accepts.
	Want string
}

func (e *InvalidValueError) Error() string {
	return fmt.Sprintf("invalid value %q for %s: want %s", e.Value, e.Key, e.Want)
}

// Get returns the effective value of the setting key: a bool for
// refresh.enabled, an int for refresh.interval_hours. An unknown key is an
// *UnknownKeyError.
func (c *Config) Get(key string) (any, error) {
	switch key {
	case KeyRefreshEnabled:
		return c.RefreshEnabled(), nil
	case KeyRefreshIntervalHours:
		return c.RefreshIntervalHours(), nil
	}
	return nil, &UnknownKeyError{Key: key}
}

// Set parses value for the setting key and stores it in c (the caller saves
// c). An unknown key is an *UnknownKeyError, a value the key does not accept
// an *InvalidValueError; either way c is unchanged.
func (c *Config) Set(key, value string) error {
	switch key {
	case KeyRefreshEnabled:
		b, err := strconv.ParseBool(strings.TrimSpace(value))
		if err != nil {
			return &InvalidValueError{Key: key, Value: value, Want: "true or false"}
		}
		c.refresh().Enabled = boolPtr(b)
		return nil
	case KeyRefreshIntervalHours:
		h, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil || h < MinRefreshIntervalHours || h > MaxRefreshIntervalHours {
			return &InvalidValueError{Key: key, Value: value,
				Want: fmt.Sprintf("a whole number of hours from %d to %d", MinRefreshIntervalHours, MaxRefreshIntervalHours)}
		}
		c.refresh().IntervalHours = intPtr(h)
		return nil
	}
	return &UnknownKeyError{Key: key}
}

// refresh returns c's [refresh] table, creating it when absent.
func (c *Config) refresh() *Refresh {
	if c.Refresh == nil {
		c.Refresh = &Refresh{}
	}
	return c.Refresh
}
