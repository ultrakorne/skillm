package cmd

import (
	"context"
	"testing"
)

// TestRunUpgradeRefusesSourceBuild pins the safety gate: a binary whose version
// is not a release tag corresponds to no published release, so upgrade must
// report that and return without touching the network or the binary. The
// default `version` is "dev", so no stubbing is needed.
func TestRunUpgradeRefusesSourceBuild(t *testing.T) {
	if Version() != "dev" {
		t.Skipf("test binary was version-stamped as %q", Version())
	}
	// Reaching GitHub would hang or fail here; returning nil proves the gate
	// short-circuited before any request.
	if err := runUpgrade(context.Background(), false); err != nil {
		t.Fatalf("runUpgrade on a source build = %v, want nil", err)
	}
}

// TestUpgradeSkipsGitCheck guards the annotation wiring: upgrade replaces
// skillm's own binary and must run on a machine without git, unlike every
// command that fetches or syncs skills. version is the other exception: a GUI
// runs `version --json` before it can report that git is missing, and status
// only reads the refresh cache.
func TestUpgradeSkipsGitCheck(t *testing.T) {
	c := newUpgradeCmd()
	if c.Annotations[annotationSkipGitCheck] != "true" {
		t.Error("upgrade must carry the skip-git-check annotation")
	}

	// The rest of the command tree must not: they all need git at runtime.
	for _, sub := range Root().Commands() {
		if sub.Name() == "upgrade" || sub.Name() == "version" || sub.Name() == "status" {
			continue
		}
		if sub.Annotations[annotationSkipGitCheck] == "true" {
			t.Errorf("%s unexpectedly skips the git check", sub.Name())
		}
	}
}

// TestUpgradeRegistered ensures the command is attached to the root, since it
// registers itself from an init() that is easy to lose in a refactor.
func TestUpgradeRegistered(t *testing.T) {
	for _, sub := range Root().Commands() {
		if sub.Name() == "upgrade" {
			return
		}
	}
	t.Fatal("upgrade is not registered on the root command")
}
