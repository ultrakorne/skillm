package config

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// A config.toml written before [refresh] existed reads as the defaults, and
// rewriting it (as `skillm agent` does) adds no [refresh] table.
func TestRefreshDefaultsWhenAbsent(t *testing.T) {
	home := t.TempDir()
	body := "[agents.claude]\nenabled = true\nglobal = \"~/.claude/skills\"\nlocal = \".claude/skills\"\n"
	if err := os.WriteFile(Path(home), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if !c.RefreshEnabled() || c.RefreshIntervalHours() != 24 {
		t.Fatalf("absent [refresh] = %v, %d; want true, 24", c.RefreshEnabled(), c.RefreshIntervalHours())
	}
	if err := Save(home, c); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	if containsRefresh(string(b)) {
		t.Fatalf("rewrite added a [refresh] table:\n%s", b)
	}
}

func containsRefresh(s string) bool { return strings.Contains(s, "[refresh]") }

// A fresh config.toml shows the [refresh] defaults, so the setting is
// discoverable by hand.
func TestDefaultSeedsRefresh(t *testing.T) {
	home := t.TempDir()
	if err := EnsureExists(home); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(Path(home))
	if err != nil {
		t.Fatal(err)
	}
	if !containsRefresh(string(b)) {
		t.Fatalf("seeded config.toml has no [refresh]:\n%s", b)
	}
}

func TestSetGetRoundTrip(t *testing.T) {
	home := t.TempDir()
	c := Default()
	if err := c.Set(KeyRefreshEnabled, "false"); err != nil {
		t.Fatal(err)
	}
	if err := c.Set(KeyRefreshIntervalHours, " 6 "); err != nil {
		t.Fatal(err)
	}
	if err := Save(home, c); err != nil {
		t.Fatal(err)
	}
	got, err := Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := got.Get(KeyRefreshEnabled); v != false {
		t.Errorf("refresh.enabled = %v, want false", v)
	}
	if v, _ := got.Get(KeyRefreshIntervalHours); v != 6 {
		t.Errorf("refresh.interval_hours = %v, want 6", v)
	}
	// The agents survive a settings change.
	if len(got.Agents) != 2 {
		t.Errorf("agents lost: %+v", got.Agents)
	}
}

func TestSetRefusesBadInput(t *testing.T) {
	cases := []struct {
		key, value string
		unknown    bool
	}{
		{"refresh.enabled", "maybe", false},
		{"refresh.interval_hours", "0", false},
		{"refresh.interval_hours", "721", false},
		{"refresh.interval_hours", "1.5", false},
		{"refresh.bogus", "1", true},
		{"agents.claude.enabled", "true", true},
	}
	for _, tc := range cases {
		c := Default()
		before := *c.Refresh.IntervalHours
		err := c.Set(tc.key, tc.value)
		var unk *UnknownKeyError
		var bad *InvalidValueError
		switch {
		case tc.unknown && !errors.As(err, &unk):
			t.Errorf("Set(%q, %q) = %v, want *UnknownKeyError", tc.key, tc.value, err)
		case !tc.unknown && !errors.As(err, &bad):
			t.Errorf("Set(%q, %q) = %v, want *InvalidValueError", tc.key, tc.value, err)
		}
		if *c.Refresh.IntervalHours != before || !*c.Refresh.Enabled {
			t.Errorf("Set(%q, %q) changed the config on error", tc.key, tc.value)
		}
	}
	if _, err := Default().Get("nope"); err == nil {
		t.Error("Get of an unknown key succeeded")
	}
}

// A hand-edited interval out of range reads as the default.
func TestRefreshIntervalOutOfRange(t *testing.T) {
	for _, h := range []int{0, -3, 100000} {
		c := &Config{Refresh: &Refresh{IntervalHours: intPtr(h)}}
		if got := c.RefreshIntervalHours(); got != DefaultRefreshIntervalHours {
			t.Errorf("interval_hours = %d reads as %d, want the default", h, got)
		}
	}
}
