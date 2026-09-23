package selfupdate

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestInBundle(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/Applications/skillm.app/Contents/Helpers/skillm", true},
		{"/Applications/skillm.app/Contents/MacOS/skillm", true},
		{"/Users/me/Applications/Skillm.APP/contents/Resources/bin/skillm", true}, // case-insensitive
		{`C:\odd\skillm.app\Contents\skillm.exe`, true},                           // either separator
		{"/usr/local/bin/skillm", false},
		{"/opt/homebrew/Cellar/skillm/0.2.0/bin/skillm", false},
		{"/Applications/skillm.app", false},                // the bundle itself, not inside it
		{"/Applications/skillm.app/skillm", false},         // no Contents/
		{"/Users/me/my.app.backup/Contents/skillm", false}, // not a .app directory
		{"/Users/me/.app/Contents/skillm", false},          // ".app" alone names no bundle
		{"/Users/me/projects/Contents/skillm.app", false},  // .app is the file, not a parent
		{"/home/me/go/bin/skillm", false},
		{"", false},
	}
	for _, c := range cases {
		if got := InBundle(c.path); got != c.want {
			t.Errorf("InBundle(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

// Apply must refuse a binary inside an app bundle before it downloads
// anything, and leave the binary untouched.
func TestApplyRefusesBundledBinary(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.NotFound(w, r)
	}))
	defer srv.Close()

	target := filepath.Join(t.TempDir(), "skillm.app", "Contents", "Helpers", archiveEntry())
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("bundled binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	prev := resolveExecutable
	resolveExecutable = func() (string, error) { return target, nil }
	defer func() { resolveExecutable = prev }()

	tag := "v9.0.0"
	name := archiveName(tag)
	rel := &Release{
		Tag:          tag,
		ArchiveName:  name,
		ArchiveURL:   srv.URL + "/" + name,
		ChecksumsURL: srv.URL + "/checksums.txt",
	}
	if _, err := Apply(context.Background(), rel, "v1.0.0"); !errors.Is(err, ErrBundled) {
		t.Fatalf("Apply inside a bundle = %v, want ErrBundled", err)
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("Apply made %d requests before refusing", n)
	}
	if body, _ := os.ReadFile(target); string(body) != "bundled binary" {
		t.Errorf("target was modified: %q", body)
	}
}
