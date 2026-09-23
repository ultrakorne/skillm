package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// buildReleaseBinary builds skillm stamped as a release version into path.
func buildReleaseBinary(t *testing.T, path, version string) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-o", path,
		"-ldflags", "-X github.com/ultrakorne/skillm/cmd.version="+version, ".")
	build.Dir = ".."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build skillm: %v\n%s", err, out)
	}
}

// runOffline runs the binary with every HTTP(S) request routed to a dead
// proxy, so a network lookup fails fast and visibly instead of reaching
// GitHub.
func runOffline(t *testing.T, bin string, args ...string) (string, error) {
	t.Helper()
	c := exec.Command(bin, args...)
	c.Env = append(os.Environ(),
		"HTTPS_PROXY=http://127.0.0.1:1", "HTTP_PROXY=http://127.0.0.1:1", "NO_PROXY=",
		"HOME="+t.TempDir(), "SKILLM_HOME="+t.TempDir())
	out, err := c.CombinedOutput()
	// fang styles errors (capitalized, wrapped to the terminal width), so
	// compare lowercased with whitespace runs collapsed.
	return strings.ToLower(strings.Join(strings.Fields(string(out)), " ")), err
}

// A release skillm inside an app bundle refuses to upgrade itself, before
// reaching the network, and leaves the binary alone; `--check` still reports.
// The same binary outside a bundle goes on to look up the latest release.
func TestUpgradeRefusesInsideAppBundle(t *testing.T) {
	name := "skillm"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	dir := t.TempDir()
	bundled := filepath.Join(dir, "skillm.app", "Contents", "Helpers", name)
	buildReleaseBinary(t, bundled, "0.2.0")
	before, err := os.ReadFile(bundled)
	if err != nil {
		t.Fatal(err)
	}

	out, err := runOffline(t, bundled, "upgrade", "--yes")
	if err == nil {
		t.Fatalf("upgrade inside a bundle succeeded:\n%s", out)
	}
	if !strings.Contains(out, "managed by the skillm app; use upgrade in the menu") {
		t.Errorf("upgrade inside a bundle did not name the app:\n%s", out)
	}
	if strings.Contains(out, "check for a newer skillm") {
		t.Errorf("upgrade inside a bundle reached the network before refusing:\n%s", out)
	}
	if after, _ := os.ReadFile(bundled); string(after) != string(before) {
		t.Error("the bundled binary was modified")
	}

	// --check is a read: it looks the release up (and fails offline here)
	// rather than refusing.
	if out, _ := runOffline(t, bundled, "upgrade", "--check"); !strings.Contains(out, "check for a newer skillm") {
		t.Errorf("upgrade --check inside a bundle did not look up the release:\n%s", out)
	}

	// A symlink into the bundle (the app's "install command-line tool")
	// resolves to the bundled binary, so it refuses too.
	if runtime.GOOS != "windows" {
		link := filepath.Join(dir, "bin", name)
		if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(bundled, link); err != nil {
			t.Fatal(err)
		}
		out, err := runOffline(t, link, "upgrade", "--yes")
		if err == nil || !strings.Contains(out, "managed by the skillm app") {
			t.Errorf("upgrade through a symlink into the bundle = %v:\n%s", err, out)
		}
	}

	// Outside a bundle the same build is an ordinary binary install.
	plain := filepath.Join(dir, "plain", name)
	buildReleaseBinary(t, plain, "0.2.0")
	out, _ = runOffline(t, plain, "upgrade", "--yes")
	if strings.Contains(out, "managed by the skillm app") || !strings.Contains(out, "check for a newer skillm") {
		t.Errorf("upgrade outside a bundle did not look up the release:\n%s", out)
	}
}
