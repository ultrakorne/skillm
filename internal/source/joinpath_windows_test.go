package source

import "testing"

// TestJoinPathDriveRooted: on Windows a path rooted at a separator with no
// drive names the base's drive root, as filepath.Abs would resolve it, not a
// directory under the base.
func TestJoinPathDriveRooted(t *testing.T) {
	for _, tc := range []struct{ base, p, want string }{
		{`C:\proj`, `\skills\foo`, `C:\skills\foo`},
		{`C:\proj`, `/skills/foo`, `C:\skills\foo`},
		{`\\server\share\proj`, `\skills\foo`, `\\server\share\skills\foo`},
		{`C:\proj`, `skills\foo`, `C:\proj\skills\foo`},
	} {
		if got := JoinPath(tc.base, tc.p); got != tc.want {
			t.Errorf("JoinPath(%q, %q) = %q, want %q", tc.base, tc.p, got, tc.want)
		}
	}
}
