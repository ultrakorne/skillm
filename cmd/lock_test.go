package cmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/ultrakorne/skillm/internal/lockfile"
	"github.com/ultrakorne/skillm/internal/store"
)

// bgCmd is a command carrying a context, as cobra gives every RunE.
func bgCmd() *cobra.Command {
	c := &cobra.Command{}
	c.SetContext(context.Background())
	return c
}

// holdHomeLock points SKILLM_HOME at a fresh Home, takes its lock, and
// releases it after hold. It returns the time the lock was taken.
func holdHomeLock(t *testing.T, hold time.Duration) time.Time {
	t.Helper()
	home := t.TempDir()
	t.Setenv("SKILLM_HOME", home)
	unlock, err := store.Lock(context.Background(), home, "skillm test", nil)
	if err != nil {
		t.Fatalf("Lock: %v", err)
	}
	t.Cleanup(unlock)
	go func() {
		time.Sleep(hold)
		unlock()
	}()
	return time.Now()
}

// Every command that saves config or state takes Home's lock before loading
// anything: while another process holds it, the command waits. Each command
// here runs against an empty Home, so it does no real work once it gets the
// lock — its result does not matter, only that it waited. install and import
// are the exceptions: they fetch (and install prompts) before taking the lock
// for the write phase, and skip it when there is nothing to write, so install
// is given a skill to install and import a lockfile entry to consider.
func TestMutatingCommandsWaitForHomeLock(t *testing.T) {
	cmds := map[string]func(t *testing.T) error{
		"install": func(t *testing.T) error {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("USERPROFILE", t.TempDir())
			src := filepath.Join(t.TempDir(), "demo")
			if err := os.MkdirAll(src, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("---\nname: demo\ndescription: d\n---\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := runInstall(bgCmd(), []string{src}, true, false, true); err != nil {
				t.Errorf("install: %v", err)
			}
			return nil
		},
		"update":    func(*testing.T) error { return runUpdate(context.Background(), "", "", false) },
		"uninstall": func(*testing.T) error { return runUninstall(context.Background(), nil, true) },
		"import": func(t *testing.T) error {
			dir := t.TempDir()
			lf := &lockfile.File{Version: 1, Skills: map[string]*lockfile.Entry{
				"local": {Source: "../somewhere", SourceType: lockfile.SourceLocal, ComputedHash: "x"},
			}}
			if err := lockfile.Save(dir, lf); err != nil {
				t.Fatal(err)
			}
			return runImport(context.Background(), dir)
		},
		"agent": func(*testing.T) error { return runAgent(context.Background()) },
	}
	const hold = 300 * time.Millisecond
	for name, run := range cmds {
		t.Run(name, func(t *testing.T) {
			start := holdHomeLock(t, hold)
			_ = run(t)
			if waited := time.Since(start); waited < hold {
				t.Errorf("%s finished after %v, without waiting for the %v lock holder", name, waited, hold)
			}
		})
	}
}

// Read-only commands take no lock, so a long-running mutation never blocks them.
func TestReadOnlyCommandsIgnoreHomeLock(t *testing.T) {
	cmds := map[string]func() error{
		"list":  runList,
		"check": func() error { return runCheck(context.Background()) },
	}
	const hold = 5 * time.Second
	for name, run := range cmds {
		t.Run(name, func(t *testing.T) {
			start := holdHomeLock(t, hold)
			_ = run()
			if waited := time.Since(start); waited >= hold {
				t.Errorf("%s waited %v for the lock holder; read-only commands must not lock", name, waited)
			}
		})
	}
}
