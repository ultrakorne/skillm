package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDefault(t *testing.T) {
	c := Default()
	if !reflect.DeepEqual(c.Agents, []string{"claude", "codex"}) {
		t.Errorf("Default().Agents = %v, want [claude codex]", c.Agents)
	}
	if c.DefaultScope != "global" {
		t.Errorf("Default().DefaultScope = %q, want %q", c.DefaultScope, "global")
	}
}

func TestLoadAbsentReturnsDefault(t *testing.T) {
	home := t.TempDir()

	c, err := Load(home)
	if err != nil {
		t.Fatalf("Load on empty home: %v", err)
	}
	if !reflect.DeepEqual(c, Default()) {
		t.Errorf("Load absent = %+v, want Default() %+v", c, Default())
	}

	// Load must not write the file (config is user-owned).
	if _, err := os.Stat(Path(home)); !os.IsNotExist(err) {
		t.Errorf("Load created %s; it must not write when absent", Path(home))
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	home := t.TempDir()

	want := &Config{
		Agents:       []string{"claude"},
		DefaultScope: "local",
	}
	if err := Save(home, want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load(home)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round-trip mismatch:\n got = %+v\nwant = %+v", got, want)
	}
}

func TestSaveCreatesHome(t *testing.T) {
	home := filepath.Join(t.TempDir(), "nested", "skillm")

	if err := Save(home, Default()); err != nil {
		t.Fatalf("Save into nonexistent home: %v", err)
	}
	if _, err := os.Stat(Path(home)); err != nil {
		t.Errorf("config file not written: %v", err)
	}
}

func TestSaveNilErrors(t *testing.T) {
	if err := Save(t.TempDir(), nil); err == nil {
		t.Error("Save(nil) = nil error, want error")
	}
}
