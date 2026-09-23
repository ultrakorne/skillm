package core

import (
	"context"
	"errors"
	"testing"

	"github.com/ultrakorne/skillm/internal/state"
)

// TestUninstallSkipMissing: an id that is not installed refuses the whole
// batch, unless SkipMissing skips it with a warning and uninstalls the rest.
func TestUninstallSkipMissing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	home := t.TempDir()
	st := &state.State{}
	st.Upsert(state.SkillEntry{ID: "demo", Kind: state.KindLocal, Source: t.TempDir()})
	if err := state.Save(home, st); err != nil {
		t.Fatal(err)
	}
	opts := Options{Home: home, Cwd: t.TempDir()}

	_, err := Uninstall(context.Background(), opts, nil, UninstallRequest{IDs: []string{"gone", "demo"}})
	var notInst *NotInstalledError
	if !errors.As(err, &notInst) || len(notInst.IDs) != 1 || notInst.IDs[0] != "gone" {
		t.Fatalf("err = %v, want a NotInstalledError for gone", err)
	}

	rec := &recorder{}
	res, err := Uninstall(context.Background(), opts, rec, UninstallRequest{IDs: []string{"gone", "demo"}, SkipMissing: true})
	if err != nil || len(res.Skills) != 1 || res.Skills[0].ID != "demo" {
		t.Fatalf("res=%+v err=%v, want demo uninstalled", res, err)
	}
	warn := eventsWith(rec, CodeNotInstalled)
	if len(warn) != 1 || warn[0].Skill != "gone" || warn[0].Level != LevelWarn {
		t.Fatalf("not_installed events = %+v, want one warning for gone", warn)
	}
	after, err := state.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := after.Get("demo"); ok {
		t.Fatal("demo is still in the Registry")
	}
}
