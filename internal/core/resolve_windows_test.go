package core

import "testing"

// TestResolvePathDriveRooted: a Windows path rooted at a separator with no
// drive resolves to the base's drive root, as filepath.Abs did, not to a
// directory under the base.
func TestResolvePathDriveRooted(t *testing.T) {
	if got, want := ResolvePath(`\skills\foo`, `C:\proj`), `C:\skills\foo`; got != want {
		t.Errorf("ResolvePath = %q, want %q", got, want)
	}
	if got, want := ResolvePath(`skills\foo`, `C:\proj`), `C:\proj\skills\foo`; got != want {
		t.Errorf("ResolvePath = %q, want %q", got, want)
	}
}
