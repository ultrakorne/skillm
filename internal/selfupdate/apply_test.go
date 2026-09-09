package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTarGz builds a gzip'd tarball at dst containing one regular file per
// entry in files (name → contents).
func writeTarGz(t *testing.T, dst string, files map[string]string) {
	t.Helper()
	f, err := os.Create(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name:     name,
			Mode:     0o755,
			Size:     int64(len(body)),
			Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeZip(t *testing.T, dst string, files map[string]string) {
	t.Helper()
	f, err := os.Create(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}

func sha256Of(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestExtractFromTarGz(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "skillm.tar.gz")
	writeTarGz(t, src, map[string]string{
		"README.md": "docs",
		"skillm":    "#!/bin/sh\necho new\n",
	})

	dst := filepath.Join(dir, "out")
	if err := extractBinary(src, "skillm", dst); err != nil {
		t.Fatalf("extractBinary: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "#!/bin/sh\necho new\n" {
		t.Errorf("extracted %q", got)
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("extracted binary is not executable: %v", info.Mode())
	}
}

// A "./skillm" entry is the same file as "skillm"; path.Clean is what makes
// both archive layouts work.
func TestExtractFromTarGzDotSlashEntry(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.tar.gz")
	writeTarGz(t, src, map[string]string{"./skillm": "body"})

	dst := filepath.Join(dir, "out")
	if err := extractBinary(src, "skillm", dst); err != nil {
		t.Fatalf("extractBinary: %v", err)
	}
	if got, _ := os.ReadFile(dst); string(got) != "body" {
		t.Errorf("extracted %q", got)
	}
}

func TestExtractFromZip(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "skillm.zip")
	writeZip(t, src, map[string]string{"LICENSE": "mit", "skillm.exe": "windows body"})

	dst := filepath.Join(dir, "out.exe")
	if err := extractBinary(src, "skillm.exe", dst); err != nil {
		t.Fatalf("extractBinary: %v", err)
	}
	if got, _ := os.ReadFile(dst); string(got) != "windows body" {
		t.Errorf("extracted %q", got)
	}
}

func TestExtractMissingEntry(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.tar.gz")
	writeTarGz(t, src, map[string]string{"README.md": "docs"})

	if err := extractBinary(src, "skillm", filepath.Join(dir, "out")); err == nil {
		t.Fatal("expected an error when the archive has no binary")
	}
}

func TestVerifyChecksum(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "skillm_1.0.0_linux_amd64.tar.gz")
	if err := os.WriteFile(archive, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256Of(t, archive)

	manifest := filepath.Join(dir, "checksums.txt")
	body := fmt.Sprintf("%s  other_asset.tar.gz\n%s  skillm_1.0.0_linux_amd64.tar.gz\n", strings.Repeat("0", 64), sum)
	if err := os.WriteFile(manifest, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := verifyChecksum(manifest, archive, "skillm_1.0.0_linux_amd64.tar.gz"); err != nil {
		t.Fatalf("verifyChecksum: %v", err)
	}
}

func TestVerifyChecksumMismatch(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "a.tar.gz")
	if err := os.WriteFile(archive, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(dir, "checksums.txt")
	if err := os.WriteFile(manifest, []byte(strings.Repeat("a", 64)+"  a.tar.gz\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := verifyChecksum(manifest, archive, "a.tar.gz"); err == nil {
		t.Fatal("expected a checksum mismatch error")
	}
}

func TestVerifyChecksumMissingEntry(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "a.tar.gz")
	if err := os.WriteFile(archive, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(dir, "checksums.txt")
	if err := os.WriteFile(manifest, []byte(strings.Repeat("a", 64)+"  b.tar.gz\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := verifyChecksum(manifest, archive, "a.tar.gz"); err == nil {
		t.Fatal("expected an error for an asset the manifest does not list")
	}
}

func TestReplaceExecutable(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "skillm")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(dir, "staged")
	if err := os.WriteFile(staged, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := replaceExecutable(target, staged); err != nil {
		t.Fatalf("replaceExecutable: %v", err)
	}
	if got, _ := os.ReadFile(target); string(got) != "new" {
		t.Errorf("target holds %q, want new", got)
	}
	// The swap must not leave scratch files beside the installed binary.
	for _, leftover := range []string{target + ".new", target + ".old"} {
		if _, err := os.Stat(leftover); err == nil {
			t.Errorf("%s was left behind", filepath.Base(leftover))
		}
	}
}

// Apply end-to-end against a stub release server: metadata, archive and
// manifest are all served locally, and the "running executable" is a file in a
// temp dir.
func TestApplyReplacesResolvedExecutable(t *testing.T) {
	dir := t.TempDir()
	tag := "v9.0.0"
	name := archiveName(tag)

	archive := filepath.Join(dir, name)
	if strings.HasSuffix(name, ".zip") {
		writeZip(t, archive, map[string]string{archiveEntry(): "new binary"})
	} else {
		writeTarGz(t, archive, map[string]string{archiveEntry(): "new binary"})
	}
	manifest := fmt.Sprintf("%s  %s\n", sha256Of(t, archive), name)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/" + name:
			http.ServeFile(w, r, archive)
		case "/checksums.txt":
			fmt.Fprint(w, manifest)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	target := filepath.Join(dir, "installed", archiveEntry())
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	prev := resolveExecutable
	resolveExecutable = func() (string, error) { return target, nil }
	defer func() { resolveExecutable = prev }()

	rel := &Release{
		Tag:          tag,
		ArchiveName:  name,
		ArchiveURL:   srv.URL + "/" + name,
		ChecksumsURL: srv.URL + "/checksums.txt",
	}
	got, err := Apply(context.Background(), rel, "v1.0.0")
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if got != target {
		t.Errorf("installed at %q, want %q", got, target)
	}
	if body, _ := os.ReadFile(target); string(body) != "new binary" {
		t.Errorf("target holds %q, want the new binary", body)
	}
}

// A download whose bytes do not match the published checksum must leave the
// installed binary exactly as it was.
func TestApplyLeavesBinaryIntactOnChecksumMismatch(t *testing.T) {
	dir := t.TempDir()
	tag := "v9.0.0"
	name := archiveName(tag)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/" + name:
			fmt.Fprint(w, "tampered archive")
		case "/checksums.txt":
			fmt.Fprintf(w, "%s  %s\n", strings.Repeat("b", 64), name)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	target := filepath.Join(dir, archiveEntry())
	if err := os.WriteFile(target, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	prev := resolveExecutable
	resolveExecutable = func() (string, error) { return target, nil }
	defer func() { resolveExecutable = prev }()

	rel := &Release{
		Tag:          tag,
		ArchiveName:  name,
		ArchiveURL:   srv.URL + "/" + name,
		ChecksumsURL: srv.URL + "/checksums.txt",
	}
	if _, err := Apply(context.Background(), rel, "v1.0.0"); err == nil {
		t.Fatal("expected Apply to refuse a mismatched download")
	}
	if body, _ := os.ReadFile(target); string(body) != "old binary" {
		t.Errorf("target was modified: %q", body)
	}
}
