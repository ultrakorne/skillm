package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Apply downloads rel's archive and checksum manifest, verifies the archive's
// SHA256 against the manifest, extracts the binary, and swaps it into the
// running executable's path. Everything happens in a temp directory until the
// checksum matches, so a failed or tampered download never reaches the
// installed binary. version is used for the User-Agent only.
//
// It returns the path the new binary was installed at, which is not always the
// path the user typed: a symlinked install (e.g. a package manager's
// /usr/local/bin/skillm -> ../Cellar/…) is resolved to its real target, so the
// report names the file that actually changed.
//
// A binary inside a macOS app bundle is never replaced: Apply returns
// ErrBundled before downloading anything (see InBundle).
func Apply(ctx context.Context, rel *Release, version string) (string, error) {
	target, err := resolveExecutable()
	if err != nil {
		return "", fmt.Errorf("locate the running skillm binary: %w", err)
	}
	if InBundle(target) {
		return "", ErrBundled
	}

	tmp, err := os.MkdirTemp("", "skillm-upgrade-")
	if err != nil {
		return "", fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmp)

	archivePath := filepath.Join(tmp, rel.ArchiveName)
	if err := download(ctx, rel.ArchiveURL, archivePath, version); err != nil {
		return "", fmt.Errorf("download %s: %w", rel.ArchiveName, err)
	}
	checksumsPath := filepath.Join(tmp, checksumsName)
	if err := download(ctx, rel.ChecksumsURL, checksumsPath, version); err != nil {
		return "", fmt.Errorf("download %s: %w", checksumsName, err)
	}
	if err := verifyChecksum(checksumsPath, archivePath, rel.ArchiveName); err != nil {
		return "", err
	}

	staged := filepath.Join(tmp, archiveEntry())
	if err := extractBinary(archivePath, archiveEntry(), staged); err != nil {
		return "", err
	}
	if err := replaceExecutable(target, staged); err != nil {
		return "", err
	}
	return target, nil
}

// resolveExecutable is a package var so tests can stub it. Symlink resolution
// failures are fatal rather than falling back to the unresolved path: renaming
// a symlink would report success while leaving the real binary stale.
var resolveExecutable = func() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

func download(ctx context.Context, url, dst, version string) error {
	reqCtx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent(version))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned %s", resp.Status)
	}

	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// verifyChecksum compares the archive's SHA256 against the entry the release's
// checksums.txt publishes for it. A mismatch, or a manifest with no row for
// this archive, aborts the upgrade — an unverified binary is never installed.
func verifyChecksum(checksumsPath, archivePath, archiveName string) error {
	want, err := readChecksum(checksumsPath, archiveName)
	if err != nil {
		return err
	}

	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("checksum mismatch for %s: the download does not match the published release (got %s, want %s)", archiveName, got, want)
	}
	return nil
}

// readChecksum parses goreleaser's manifest format — one "<sha256>  <name>"
// row per asset — and returns the digest recorded for name.
func readChecksum(path, name string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 {
			continue
		}
		// Some checksum writers mark binary mode with a leading "*".
		if strings.TrimPrefix(fields[1], "*") == name {
			return fields[0], nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("%s has no entry for %s", checksumsName, name)
}

// extractBinary pulls the single entry named entry out of the release archive
// at src and writes it to dst with mode 0755. tar.gz and zip are both handled
// because goreleaser ships zip on Windows and tar.gz everywhere else.
func extractBinary(src, entry, dst string) error {
	if strings.HasSuffix(src, ".zip") {
		return extractFromZip(src, entry, dst)
	}
	return extractFromTarGz(src, entry, dst)
}

func extractFromTarGz(src, entry, dst string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gunzip %s: %w", filepath.Base(src), err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read %s: %w", filepath.Base(src), err)
		}
		// Archive paths are slash-separated; compare cleaned so a "./skillm"
		// entry matches and a "../" traversal attempt never does.
		if path.Clean(hdr.Name) != entry || hdr.Typeflag != tar.TypeReg {
			continue
		}
		return writeBinary(dst, tr)
	}
	return fmt.Errorf("%s does not contain a %s binary", filepath.Base(src), entry)
}

func extractFromZip(src, entry, dst string) error {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", filepath.Base(src), err)
	}
	defer zr.Close()

	for _, f := range zr.File {
		if path.Clean(f.Name) != entry || f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		defer rc.Close()
		return writeBinary(dst, rc)
	}
	return fmt.Errorf("%s does not contain a %s binary", filepath.Base(src), entry)
}

func writeBinary(dst string, r io.Reader) error {
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// replaceExecutable swaps the file at target for the freshly extracted binary
// at staged. The staged copy is first written next to target, so the final
// rename is within one filesystem (the temp dir often is not), then the
// running binary is renamed aside and the new one renamed into its place —
// the POSIX self-replacement dance, which Windows also permits for a running
// image. If the second rename fails the original is restored, so an
// interrupted upgrade never leaves the user without a skillm.
func replaceExecutable(target, staged string) error {
	next := target + ".new"
	old := target + ".old"

	if err := copyFile(staged, next, 0o755); err != nil {
		return fmt.Errorf("write the new binary next to %s: %w (is the directory writable? re-run with elevated permissions if skillm is installed system-wide)", target, err)
	}
	cleanup := next
	defer func() {
		if cleanup != "" {
			os.Remove(cleanup)
		}
	}()

	if err := os.Rename(target, old); err != nil {
		return fmt.Errorf("move the current binary aside: %w", err)
	}
	if err := os.Rename(next, target); err != nil {
		if rbErr := os.Rename(old, target); rbErr != nil {
			return fmt.Errorf("install the new binary: %w (the previous binary is at %s and could not be restored: %v)", err, old, rbErr)
		}
		return fmt.Errorf("install the new binary: %w (the previous one was restored)", err)
	}
	cleanup = "" // renamed into place; nothing left to remove

	// Best-effort: on Windows the aside copy can stay locked while the old
	// image is still mapped, and a leftover .old is harmless.
	_ = os.Remove(old)
	return nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
