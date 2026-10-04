package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/ultrakorne/skillm/internal/agentdir"
	"github.com/ultrakorne/skillm/internal/config"
	"github.com/ultrakorne/skillm/internal/lockfile"
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

func TestScopedUninstallLegacyInstalls(t *testing.T) {
	for _, first := range []agentdir.Scope{agentdir.Global, agentdir.Local} {
		t.Run(first.String(), func(t *testing.T) {
			opts, base, insp := installSetup(t)
			// One canonical install and one legacy link without its marker.
			if _, err := InstallSkills(context.Background(), opts, nil, InstallRequest{Inspection: insp, IDs: []string{"demo"}, Scope: first, Base: base}); err != nil {
				t.Fatal(err)
			}
			second := agentdir.Local
			if first == agentdir.Local {
				second = agentdir.Global
			}
			legacy := filepath.Join(opts.Home, "skills", "demo")
			if err := os.MkdirAll(legacy, 0o755); err != nil {
				t.Fatal(err)
			}
			agents := config.Default().AllAgents()
			path := linkPath(t, agents[1], second, base, "demo")
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(legacy, path); err != nil {
				t.Fatal(err)
			}
			// Disabled agents still count toward retaining and removing links.
			cfg := config.Default()
			def := cfg.Agents["claude"]
			enabled := false
			def.Enabled = &enabled
			cfg.Agents["claude"] = def
			if err := config.Save(opts.Home, cfg); err != nil {
				t.Fatal(err)
			}
			res, err := Uninstall(context.Background(), opts, nil, UninstallRequest{IDs: []string{"demo"}, Scope: &first, Base: base})
			if err != nil || len(res.Skills) != 1 {
				t.Fatalf("first scope: %+v %v", res, err)
			}
			st, err := state.Load(opts.Home)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := st.Get("demo"); !ok {
				t.Fatal("remaining legacy install lost its registry entry")
			}
			if second == agentdir.Local && !slices.Contains(st.LocalRoots, base) {
				t.Fatal("discovered project must remain tracked")
			}
			if _, err := os.Lstat(path); err != nil {
				t.Fatalf("unselected legacy link was removed: %v", err)
			}
			res, err = Uninstall(context.Background(), opts, nil, UninstallRequest{IDs: []string{"demo"}, Scope: &second, Base: base})
			if err != nil || len(res.Skills) != 1 {
				t.Fatalf("legacy scope: %+v %v", res, err)
			}
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Fatalf("selected legacy link remains: %v", err)
			}
			st, _ = state.Load(opts.Home)
			if _, ok := st.Get("demo"); ok {
				t.Fatal("registry remains after the last legacy install")
			}
		})
	}
}

func TestUninstallOneScope(t *testing.T) {
	opts, base, insp := installSetup(t)
	other := t.TempDir()
	for _, req := range []InstallRequest{
		{Inspection: insp, IDs: []string{"demo"}, Scope: agentdir.Global},
		{Inspection: insp, IDs: []string{"demo"}, Scope: agentdir.Local, Base: base},
		{Inspection: insp, IDs: []string{"demo"}, Scope: agentdir.Local, Base: other},
	} {
		if _, err := InstallSkills(context.Background(), opts, nil, req); err != nil {
			t.Fatal(err)
		}
	}

	local, global := agentdir.Local, agentdir.Global
	req := UninstallRequest{IDs: []string{"demo"}, Scope: &local, Base: base, CheckRoots: true, ConfirmedRoots: []string{base}}
	res, err := Uninstall(context.Background(), opts, nil, req)
	if err != nil || len(res.Skills) != 1 || !slices.Equal(res.Skills[0].RemovedCopies, []string{base}) {
		t.Fatalf("local removal: res=%+v err=%v", res, err)
	}
	for _, path := range []string{demoSlot(base), claudeLink(base)} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("selected install still exists: %s: %v", path, err)
		}
	}
	lf, err := lockfile.Load(base)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := lf.Skills["demo"]; ok {
		t.Fatal("selected project's lock entry remains")
	}
	after, err := state.Load(opts.Home)
	if err != nil || !after.IsGlobal("demo") || !slices.Equal(after.VendoredRoots("demo"), []string{other}) || slices.Contains(after.LocalRoots, base) {
		t.Fatalf("remaining installs: state=%+v err=%v", after, err)
	}
	for _, path := range []string{demoSlot(other), claudeLink(other), agentdir.CanonicalSkillDirAt(global, "", "demo")} {
		if _, err := os.Stat(filepath.Join(path, "SKILL.md")); err != nil {
			t.Fatalf("other install was changed: %s: %v", path, err)
		}
	}
	lf, err = lockfile.Load(other)
	if err != nil || lf.Skills["demo"].Source == "" {
		t.Fatalf("other project's lock entry was changed: %v", err)
	}

	// Global removal ignores the unrelated cwd's local copy and confirmations.
	opts.Cwd = other
	res, err = Uninstall(context.Background(), opts, nil, UninstallRequest{IDs: []string{"demo"}, Scope: &global, CheckRoots: true})
	if err != nil || !slices.Equal(res.Skills[0].RemovedCopies, []string{"global"}) {
		t.Fatalf("global removal: res=%+v err=%v", res, err)
	}
	after, _ = state.Load(opts.Home)
	if after.IsGlobal("demo") || !slices.Equal(after.VendoredRoots("demo"), []string{other}) {
		t.Fatalf("global removal changed local records: %+v", after)
	}
	if _, err := os.Stat(filepath.Join(claudeLink(other), "SKILL.md")); err != nil {
		t.Fatalf("global removal changed local files: %v", err)
	}
	res, err = Uninstall(context.Background(), opts, nil, UninstallRequest{IDs: []string{"demo"}, Scope: &local, Base: other})
	if err != nil || len(res.Skills) != 1 {
		t.Fatalf("last install removal: %+v %v", res, err)
	}
	after, _ = state.Load(opts.Home)
	if _, ok := after.Get("demo"); ok {
		t.Fatal("registry entry remains after removing the last install")
	}
}

func TestUninstallMissingScopeIsAtomic(t *testing.T) {
	opts, base, insp := installSetup(t)
	if _, err := InstallSkills(context.Background(), opts, nil, InstallRequest{Inspection: insp, IDs: []string{"demo"}, Scope: agentdir.Local, Base: base}); err != nil {
		t.Fatal(err)
	}
	st, _ := state.Load(opts.Home)
	st.Upsert(state.SkillEntry{ID: "elsewhere", Global: true})
	if err := state.Save(opts.Home, st); err != nil {
		t.Fatal(err)
	}
	local := agentdir.Local
	req := UninstallRequest{IDs: []string{"demo", "elsewhere"}, Scope: &local, Base: base}
	_, err := Uninstall(context.Background(), opts, nil, req)
	var missing *NotInstalledError
	if !errors.As(err, &missing) || !slices.Equal(missing.IDs, []string{"elsewhere"}) {
		t.Fatalf("missing scope: %v", err)
	}
	if _, err := os.Stat(demoSlot(base)); err != nil {
		t.Fatalf("atomic refusal removed valid install: %v", err)
	}
	req.SkipMissing = true
	res, err := Uninstall(context.Background(), opts, nil, req)
	if err != nil || len(res.Skills) != 1 || res.Skills[0].ID != "demo" {
		t.Fatalf("skip missing scope: %+v %v", res, err)
	}
	st, _ = state.Load(opts.Home)
	if !st.IsGlobal("elsewhere") {
		t.Fatal("skipping a missing local install removed its global entry")
	}
}
