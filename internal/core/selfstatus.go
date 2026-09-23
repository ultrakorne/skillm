package core

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/ultrakorne/skillm/internal/selfupdate"
)

// UpgradeMethod says how the running skillm binary gets upgraded.
type UpgradeMethod string

const (
	// MethodBinary: a release build installed on its own; `skillm upgrade`
	// replaces it in place.
	MethodBinary UpgradeMethod = "binary"
	// MethodBundled: a release build inside a macOS app bundle. The app
	// upgrades the whole bundle (Sparkle), so skillm never replaces it.
	MethodBundled UpgradeMethod = "bundled"
	// MethodDev: a build whose version is not a release tag (built from
	// source). No release corresponds to it, so there is nothing to upgrade
	// to and no release is looked up. This wins over MethodBundled: a debug
	// app that bundles a source build is dev.
	MethodDev UpgradeMethod = "dev"
)

// SelfStatus is the running skillm's version against the latest release.
type SelfStatus struct {
	// Current is the running version, without a leading "v" ("dev" for an
	// unstamped source build).
	Current string
	// Latest is the latest release's version, without a leading "v". It is
	// empty for MethodDev, which looks nothing up.
	Latest string
	// Available reports that Latest is newer than Current.
	Available bool
	// Eligible reports that `skillm upgrade` can install Latest: it is
	// Available and Method is MethodBinary. A bundled skillm is upgraded by
	// its app instead.
	Eligible bool
	// Method says how this binary is upgraded.
	Method UpgradeMethod
	// Executable is the running binary's path (symlinks resolved when
	// possible), the one Method was judged from. It may be empty when the
	// path cannot be determined.
	Executable string

	// release is the resolved latest release, which UpgradeSelf installs.
	release *selfupdate.Release
}

// ErrManagedByApp means the running skillm is inside the skillm app's bundle,
// which the app upgrades as a whole; replacing the binary alone would break
// the bundle's code signature.
var ErrManagedByApp = errors.New("skillm is managed by the skillm app; use Upgrade in the menu")

// ErrSourceBuild means the running skillm is not a release build, so there
// is no release to upgrade it to.
var ErrSourceBuild = errors.New("skillm is built from source, so there is no release to upgrade to")

// ErrNoUpgrade means the running skillm is already the latest release.
var ErrNoUpgrade = errors.New("skillm is already the latest release")

// Test hooks: the running executable's path and the release lookup.
var (
	executablePath = func() string {
		if exe, err := selfupdate.Executable(); err == nil {
			return exe
		}
		exe, _ := os.Executable()
		return exe
	}
	latestRelease = selfupdate.Latest
	applyRelease  = selfupdate.Apply
)

// SelfMethod says how the running skillm (of the given version) is upgraded,
// and the executable path that was judged. It works offline, so a caller can
// refuse an upgrade before reaching the network.
func SelfMethod(version string) (UpgradeMethod, string) {
	exe := executablePath()
	switch {
	case !selfupdate.IsReleaseVersion(version):
		return MethodDev, exe
	case exe != "" && selfupdate.InBundle(exe):
		return MethodBundled, exe
	default:
		return MethodBinary, exe
	}
}

// CheckSelf reports the running skillm (of the given version) against the
// latest published release. A dev build returns without any network request.
// It changes nothing.
func CheckSelf(ctx context.Context, version string) (SelfStatus, error) {
	method, exe := SelfMethod(version)
	st := SelfStatus{
		Current:    selfupdate.Display(version),
		Method:     method,
		Executable: exe,
	}
	if method == MethodDev {
		return st, nil
	}
	rel, err := latestRelease(ctx, version)
	if err != nil {
		return st, fmt.Errorf("check for a newer skillm: %w", err)
	}
	st.Latest = selfupdate.Display(rel.Tag)
	st.Available = selfupdate.IsNewer(rel.Tag, version)
	st.Eligible = st.Available && method == MethodBinary
	st.release = rel
	return st, nil
}

// UpgradeSelf installs the release st (from CheckSelf) found, replacing the
// running binary, and returns the path it installed at. It refuses a bundled
// skillm (ErrManagedByApp), a source build (ErrSourceBuild) and an up-to-date
// one (ErrNoUpgrade) without writing anything.
func UpgradeSelf(ctx context.Context, st SelfStatus) (string, error) {
	switch {
	case st.Method == MethodBundled:
		return "", ErrManagedByApp
	case st.Method == MethodDev:
		return "", ErrSourceBuild
	case !st.Eligible || st.release == nil:
		return "", ErrNoUpgrade
	}
	path, err := applyRelease(ctx, st.release, st.Current)
	if errors.Is(err, selfupdate.ErrBundled) {
		// The executable moved into a bundle (or resolved into one) after
		// CheckSelf judged it.
		return "", ErrManagedByApp
	}
	if err != nil {
		return "", fmt.Errorf("upgrade to %s: %w", st.Latest, err)
	}
	return path, nil
}
