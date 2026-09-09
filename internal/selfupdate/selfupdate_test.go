package selfupdate

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
)

func TestIsReleaseVersion(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"v0.1.0", true},
		{"0.1.0", true}, // goreleaser bakes in the un-prefixed form
		{"v1.2.3", true},
		{"dev", false},                    // the default, un-stamped build
		{"", false},                       // ldflags omitted entirely
		{"v0.1.0-3-gabc123", false},       // `git describe` after a tag
		{"v0.1.0-3-gabc123-dirty", false}, // …with local edits
		{"v0.1.0-rc1", false},             // pre-release
		{"v0.1.0+meta", false},            // build metadata
		{"v0.1", true},                    // Go's semver treats a short tag as v0.1.0
		{"nonsense", false},
	}
	for _, c := range cases {
		if got := IsReleaseVersion(c.in); got != c.want {
			t.Errorf("IsReleaseVersion(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestIsNewer(t *testing.T) {
	cases := []struct {
		tag, version string
		want         bool
	}{
		{"v0.2.0", "v0.1.0", true},
		{"v0.2.0", "0.1.0", true}, // mixed prefixing must still order correctly
		{"v0.1.0", "v0.1.0", false},
		{"v0.1.0", "v0.2.0", false}, // a rolled-back release never downgrades us
		{"v0.10.0", "v0.9.0", true}, // numeric, not lexicographic
		{"latest", "v0.1.0", false}, // unorderable tags are never "newer"
		{"", "v0.1.0", false},
	}
	for _, c := range cases {
		if got := IsNewer(c.tag, c.version); got != c.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", c.tag, c.version, got, c.want)
		}
	}
}

func TestDisplayStripsPrefix(t *testing.T) {
	if got := Display("v1.2.3"); got != "1.2.3" {
		t.Errorf("Display(v1.2.3) = %q, want 1.2.3", got)
	}
	if got := Display("1.2.3"); got != "1.2.3" {
		t.Errorf("Display(1.2.3) = %q, want 1.2.3", got)
	}
}

// TestArchiveNameMatchesGoreleaser pins the asset name to the template in
// .goreleaser.yaml. If that template changes, upgrade stops finding its own
// release assets, so this test is the tripwire.
func TestArchiveNameMatchesGoreleaser(t *testing.T) {
	ext := "tar.gz"
	if runtime.GOOS == "windows" {
		ext = "zip"
	}
	want := fmt.Sprintf("skillm_1.2.3_%s_%s.%s", runtime.GOOS, runtime.GOARCH, ext)
	if got := archiveName("v1.2.3"); got != want {
		t.Errorf("archiveName = %q, want %q", got, want)
	}
}

// releaseServer stands in for the GitHub API, serving one /releases/latest
// payload built from the asset names given.
func releaseServer(t *testing.T, tag string, assets ...string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/ultrakorne/skillm/releases/latest" {
			http.NotFound(w, r)
			return
		}
		body := fmt.Sprintf(`{"tag_name":%q,"assets":[`, tag)
		for i, a := range assets {
			if i > 0 {
				body += ","
			}
			body += fmt.Sprintf(`{"name":%q,"browser_download_url":"https://example.test/%s"}`, a, a)
		}
		body += "]}"
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)

	prev := baseURL
	baseURL = srv.URL
	t.Cleanup(func() { baseURL = prev })
	return srv
}

func TestLatestResolvesPlatformAssets(t *testing.T) {
	asset := archiveName("v9.0.0")
	releaseServer(t, "v9.0.0", asset, "checksums.txt", "skillm_9.0.0_plan9_mips.tar.gz")

	rel, err := Latest(context.Background(), "v1.0.0")
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if rel.Tag != "v9.0.0" {
		t.Errorf("Tag = %q, want v9.0.0", rel.Tag)
	}
	if rel.ArchiveName != asset {
		t.Errorf("ArchiveName = %q, want %q", rel.ArchiveName, asset)
	}
	if rel.ArchiveURL != "https://example.test/"+asset {
		t.Errorf("ArchiveURL = %q", rel.ArchiveURL)
	}
	if rel.ChecksumsURL != "https://example.test/checksums.txt" {
		t.Errorf("ChecksumsURL = %q", rel.ChecksumsURL)
	}
}

// A release tag goreleaser normalises without the "v" must still resolve: the
// tag is canonicalised before it is compared or used to build asset names.
func TestLatestAcceptsUnprefixedTag(t *testing.T) {
	releaseServer(t, "9.0.0", archiveName("v9.0.0"), "checksums.txt")

	rel, err := Latest(context.Background(), "v1.0.0")
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if rel.Tag != "v9.0.0" {
		t.Errorf("Tag = %q, want v9.0.0", rel.Tag)
	}
}

func TestLatestErrorsWhenPlatformAssetMissing(t *testing.T) {
	releaseServer(t, "v9.0.0", "checksums.txt")

	if _, err := Latest(context.Background(), "v1.0.0"); err == nil {
		t.Fatal("expected an error when this platform's archive is absent")
	}
}

func TestLatestErrorsWithoutChecksums(t *testing.T) {
	releaseServer(t, "v9.0.0", archiveName("v9.0.0"))

	// Without checksums.txt the download cannot be verified, so refusing is the
	// only safe outcome — never fall back to installing an unverified binary.
	if _, err := Latest(context.Background(), "v1.0.0"); err == nil {
		t.Fatal("expected an error when checksums.txt is absent")
	}
}

func TestLatestErrorsOnNonVersionTag(t *testing.T) {
	releaseServer(t, "nightly", "checksums.txt")

	if _, err := Latest(context.Background(), "v1.0.0"); err == nil {
		t.Fatal("expected an error for a non-semver tag")
	}
}

func TestLatestErrorsOnHTTPFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "rate limited", http.StatusForbidden)
	}))
	defer srv.Close()
	prev := baseURL
	baseURL = srv.URL
	defer func() { baseURL = prev }()

	if _, err := Latest(context.Background(), "v1.0.0"); err == nil {
		t.Fatal("expected an error for a non-200 response")
	}
}
