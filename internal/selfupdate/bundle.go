package selfupdate

import (
	"errors"
	"path/filepath"
	"strings"
)

// ErrBundled means the running skillm lives inside a macOS app bundle. The app
// ships a version-matched skillm and upgrades the whole bundle at once
// (Sparkle); swapping the binary alone would break the bundle's code
// signature, so Apply refuses before downloading anything.
var ErrBundled = errors.New("the running skillm is inside an app bundle, which upgrades it")

// Executable returns the running binary's path with symlinks resolved — the
// file Apply would replace. A symlink such as /usr/local/bin/skillm pointing
// into an app bundle therefore resolves into the bundle.
func Executable() (string, error) { return resolveExecutable() }

// InBundle reports whether path lies inside a macOS app bundle: some
// directory on it is named "<something>.app" and is followed by "Contents"
// (e.g. /Applications/skillm.app/Contents/Helpers/skillm). It only looks at
// the path, so it can be tested with any path on any platform; both
// separators are accepted, and the names are matched case-insensitively
// because the default macOS filesystem is.
func InBundle(path string) bool {
	parts := strings.FieldsFunc(filepath.ToSlash(path), func(r rune) bool {
		return r == '/' || r == '\\'
	})
	for i := 0; i+1 < len(parts); i++ {
		name := parts[i]
		if len(name) > len(".app") &&
			strings.EqualFold(name[len(name)-len(".app"):], ".app") &&
			strings.EqualFold(parts[i+1], "Contents") {
			return true
		}
	}
	return false
}
