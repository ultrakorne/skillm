// Package selfupdate upgrades the running skillm binary in place: it asks
// GitHub for the latest published release, and — when that release is newer
// than the running build — downloads the archive for this platform, verifies
// it against the release's checksums.txt, and swaps the new binary into the
// running executable's own path.
//
// Only release builds are upgraded. A binary whose version is not a clean
// release tag (the default "dev", a `go build` from a working tree, a `git
// describe` string) corresponds to no published release, so replacing it would
// silently discard the user's own build; IsReleaseVersion is the gate, and the
// caller reports the refusal.
//
// Like every other internal package, this one is presentation-free: it returns
// values and errors, and cmd/upgrade.go does the printing and prompting.
package selfupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

const (
	repoOwner = "ultrakorne"
	repoName  = "skillm"

	// binaryName is the file inside the release archive, and the name of the
	// executable being replaced. goreleaser appends .exe on Windows.
	binaryName = "skillm"

	// checksumsName is goreleaser's per-release checksum manifest.
	checksumsName = "checksums.txt"

	metadataTimeout = 15 * time.Second
	downloadTimeout = 5 * time.Minute
)

// baseURL is the GitHub API root. Tests point it at an httptest.Server; there
// is no user-visible flag.
var baseURL = "https://api.github.com"

// Release is the latest published release, resolved down to the two assets
// this platform needs: its archive and the release's checksum manifest.
type Release struct {
	// Tag is the release tag, always in canonical "vX.Y.Z" form.
	Tag string
	// ArchiveName is the asset file name, which is also the key the checksum
	// manifest lists it under.
	ArchiveName string
	// ArchiveURL and ChecksumsURL are direct download URLs.
	ArchiveURL   string
	ChecksumsURL string
}

// IsReleaseVersion reports whether v is a clean release tag (vX.Y.Z with no
// pre-release or build suffix). It is false for "dev", "", and `git describe`
// output such as "v0.1.0-3-gabc123-dirty", which is what keeps local builds
// out of the upgrade path entirely.
//
// Both "v0.2.0" and "0.2.0" are accepted: goreleaser's {{ .Version }} drops the
// leading "v", so a shipped binary's baked-in version has no prefix while the
// release tag it came from does.
func IsReleaseVersion(v string) bool {
	c := canonicalVersion(v)
	if !semver.IsValid(c) {
		return false
	}
	return semver.Prerelease(c) == "" && semver.Build(c) == ""
}

// canonicalVersion returns v with a leading "v" when that makes it valid
// semver. Anything else is returned unchanged so the caller's own validation
// rejects it.
func canonicalVersion(v string) string {
	if v == "" || strings.HasPrefix(v, "v") {
		return v
	}
	if semver.IsValid("v" + v) {
		return "v" + v
	}
	return v
}

// Display strips the leading "v" so versions read the same way in every
// message regardless of which form they arrived in.
func Display(v string) string { return strings.TrimPrefix(v, "v") }

// IsNewer reports whether the release tag is strictly newer than the running
// version. An invalid tag is never newer — we refuse to "upgrade" to something
// we cannot order.
func IsNewer(tag, version string) bool {
	tag = canonicalVersion(tag)
	if !semver.IsValid(tag) {
		return false
	}
	return semver.Compare(tag, canonicalVersion(version)) > 0
}

// Latest fetches the latest release and resolves the assets for the running
// GOOS/GOARCH. version is sent in the User-Agent only. A release that exists
// but ships no archive for this platform is an error naming the asset that was
// expected, since that is a packaging problem the user can report.
func Latest(ctx context.Context, version string) (*Release, error) {
	payload, err := fetchLatest(ctx, version)
	if err != nil {
		return nil, err
	}
	tag := canonicalVersion(payload.TagName)
	if !semver.IsValid(tag) {
		return nil, fmt.Errorf("latest release tag %q is not a version number", payload.TagName)
	}

	archiveName := archiveName(tag)
	rel := &Release{
		Tag:          tag,
		ArchiveName:  archiveName,
		ArchiveURL:   findAsset(payload.Assets, archiveName),
		ChecksumsURL: findAsset(payload.Assets, checksumsName),
	}
	if rel.ArchiveURL == "" {
		return nil, fmt.Errorf("release %s has no %s asset for %s/%s", Display(tag), archiveName, runtime.GOOS, runtime.GOARCH)
	}
	if rel.ChecksumsURL == "" {
		return nil, fmt.Errorf("release %s is missing %s, so the download cannot be verified", Display(tag), checksumsName)
	}
	return rel, nil
}

// archiveName mirrors .goreleaser.yaml's archive name_template:
// skillm_<version-without-v>_<os>_<arch>, tar.gz everywhere except Windows,
// which ships a zip. Keep the two in step — a rename there breaks upgrade.
func archiveName(tag string) string {
	ext := "tar.gz"
	if runtime.GOOS == "windows" {
		ext = "zip"
	}
	return fmt.Sprintf("%s_%s_%s_%s.%s", binaryName, Display(tag), runtime.GOOS, runtime.GOARCH, ext)
}

// archiveEntry is the binary's path inside the archive.
func archiveEntry() string {
	if runtime.GOOS == "windows" {
		return binaryName + ".exe"
	}
	return binaryName
}

type releaseAsset struct {
	Name        string `json:"name"`
	DownloadURL string `json:"browser_download_url"`
}

type releasePayload struct {
	TagName string         `json:"tag_name"`
	Assets  []releaseAsset `json:"assets"`
}

func fetchLatest(ctx context.Context, version string) (*releasePayload, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/releases/latest", baseURL, repoOwner, repoName)
	reqCtx, cancel := context.WithTimeout(ctx, metadataTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", userAgent(version))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reach github: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github returned %s for the latest release", resp.Status)
	}

	var payload releasePayload
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("read the release metadata: %w", err)
	}
	return &payload, nil
}

func findAsset(assets []releaseAsset, name string) string {
	for _, a := range assets {
		if a.Name == name {
			return a.DownloadURL
		}
	}
	return ""
}

func userAgent(version string) string {
	return "skillm/" + Display(fallback(version, "dev"))
}

func fallback(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
