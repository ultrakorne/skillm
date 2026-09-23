package core

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/ultrakorne/skillm/internal/selfupdate"
)

// stubSelf injects the executable path, the latest release tag and the
// installer for one test. The returned counters record the network lookups
// and installs made.
func stubSelf(t *testing.T, exe, latestTag string) (lookups, applies *int) {
	t.Helper()
	prevExe, prevLatest, prevApply := executablePath, latestRelease, applyRelease
	t.Cleanup(func() { executablePath, latestRelease, applyRelease = prevExe, prevLatest, prevApply })

	lookups, applies = new(int), new(int)
	executablePath = func() string { return exe }
	latestRelease = func(ctx context.Context, version string) (*selfupdate.Release, error) {
		*lookups++
		return &selfupdate.Release{Tag: latestTag}, nil
	}
	applyRelease = func(ctx context.Context, rel *selfupdate.Release, version string) (string, error) {
		*applies++
		return exe, nil
	}
	return lookups, applies
}

var (
	plainExe   = filepath.Join(string(filepath.Separator)+"usr", "local", "bin", "skillm")
	bundledExe = filepath.Join(string(filepath.Separator)+"Applications", "skillm.app", "Contents", "Helpers", "skillm")
)

func TestSelfMethod(t *testing.T) {
	cases := []struct {
		name, version, exe string
		want               UpgradeMethod
	}{
		{"release binary", "0.2.0", plainExe, MethodBinary},
		{"release inside the app", "0.2.0", bundledExe, MethodBundled},
		{"source build", "dev", plainExe, MethodDev},
		{"source build inside the app", "dev", bundledExe, MethodDev},
		{"git describe build", "v0.2.0-3-gabc123", plainExe, MethodDev},
		{"unknown path", "0.2.0", "", MethodBinary},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			stubSelf(t, c.exe, "v0.3.0")
			got, exe := SelfMethod(c.version)
			if got != c.want {
				t.Errorf("SelfMethod(%q) at %q = %s, want %s", c.version, c.exe, got, c.want)
			}
			if exe != c.exe {
				t.Errorf("SelfMethod judged %q, want %q", exe, c.exe)
			}
		})
	}
}

func TestCheckSelfBinary(t *testing.T) {
	lookups, _ := stubSelf(t, plainExe, "v0.3.0")
	st, err := CheckSelf(context.Background(), "0.2.0")
	if err != nil {
		t.Fatal(err)
	}
	want := SelfStatus{Current: "0.2.0", Latest: "0.3.0", Available: true, Eligible: true, Method: MethodBinary, Executable: plainExe}
	st.release = nil
	if st != want {
		t.Errorf("CheckSelf = %+v, want %+v", st, want)
	}
	if *lookups != 1 {
		t.Errorf("looked up the latest release %d times, want 1", *lookups)
	}
}

func TestCheckSelfBinaryUpToDate(t *testing.T) {
	stubSelf(t, plainExe, "v0.2.0")
	st, err := CheckSelf(context.Background(), "v0.2.0")
	if err != nil {
		t.Fatal(err)
	}
	if st.Available || st.Eligible || st.Current != "0.2.0" || st.Latest != "0.2.0" {
		t.Errorf("CheckSelf = %+v, want an up-to-date, ineligible status", st)
	}
	if _, err := UpgradeSelf(context.Background(), st); !errors.Is(err, ErrNoUpgrade) {
		t.Errorf("UpgradeSelf when up to date = %v, want ErrNoUpgrade", err)
	}
}

// A bundled skillm still reports an available release (the app shows it),
// but is not Eligible: the app upgrades it, never `skillm upgrade`.
func TestCheckSelfBundled(t *testing.T) {
	_, applies := stubSelf(t, bundledExe, "v0.3.0")
	st, err := CheckSelf(context.Background(), "0.2.0")
	if err != nil {
		t.Fatal(err)
	}
	if st.Method != MethodBundled || !st.Available || st.Eligible || st.Latest != "0.3.0" {
		t.Errorf("CheckSelf = %+v, want bundled, available, not eligible", st)
	}
	if _, err := UpgradeSelf(context.Background(), st); !errors.Is(err, ErrManagedByApp) {
		t.Errorf("UpgradeSelf inside the app = %v, want ErrManagedByApp", err)
	}
	if *applies != 0 {
		t.Error("UpgradeSelf tried to replace a bundled binary")
	}
}

// A source build looks nothing up and is never replaced.
func TestCheckSelfDev(t *testing.T) {
	lookups, applies := stubSelf(t, plainExe, "v0.3.0")
	st, err := CheckSelf(context.Background(), "dev")
	if err != nil {
		t.Fatal(err)
	}
	want := SelfStatus{Current: "dev", Method: MethodDev, Executable: plainExe}
	if st != want {
		t.Errorf("CheckSelf = %+v, want %+v", st, want)
	}
	if *lookups != 0 {
		t.Error("CheckSelf reached the network for a source build")
	}
	if _, err := UpgradeSelf(context.Background(), st); !errors.Is(err, ErrSourceBuild) {
		t.Errorf("UpgradeSelf on a source build = %v, want ErrSourceBuild", err)
	}
	if *applies != 0 {
		t.Error("UpgradeSelf tried to replace a source build")
	}
}

func TestUpgradeSelfInstalls(t *testing.T) {
	_, applies := stubSelf(t, plainExe, "v0.3.0")
	st, err := CheckSelf(context.Background(), "0.2.0")
	if err != nil {
		t.Fatal(err)
	}
	path, err := UpgradeSelf(context.Background(), st)
	if err != nil {
		t.Fatal(err)
	}
	if path != plainExe || *applies != 1 {
		t.Errorf("UpgradeSelf installed at %q (%d applies), want %q once", path, *applies, plainExe)
	}
}

// The installer's own bundle guard (the path resolved into a bundle after
// CheckSelf judged it) maps to the same refusal.
func TestUpgradeSelfMapsInstallerBundleGuard(t *testing.T) {
	stubSelf(t, plainExe, "v0.3.0")
	applyRelease = func(ctx context.Context, rel *selfupdate.Release, version string) (string, error) {
		return "", selfupdate.ErrBundled
	}
	st, err := CheckSelf(context.Background(), "0.2.0")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := UpgradeSelf(context.Background(), st); !errors.Is(err, ErrManagedByApp) {
		t.Errorf("UpgradeSelf = %v, want ErrManagedByApp", err)
	}
}

func TestCheckSelfWrapsLookupError(t *testing.T) {
	stubSelf(t, plainExe, "v0.3.0")
	boom := errors.New("offline")
	latestRelease = func(ctx context.Context, version string) (*selfupdate.Release, error) { return nil, boom }
	_, err := CheckSelf(context.Background(), "0.2.0")
	if !errors.Is(err, boom) || err.Error() != "check for a newer skillm: offline" {
		t.Errorf("CheckSelf = %v, want the wrapped lookup error", err)
	}
}
