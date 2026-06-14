// Package gitx shells out to the system git binary to perform the narrow set
// of operations skillm needs: treeless clones, default-branch resolution,
// per-skill subtree SHA lookup (the "Revision"), and materializing a single
// subdirectory so the store can consume it.
//
// Every exported function takes a context.Context so callers can cancel
// long-running git invocations (fang wires an interrupt-aware context down to
// each command). Errors wrap git's stderr so failures are actionable.
package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// runGit invokes `git <args...>` with dir as the working directory (dir may be
// empty to use the process cwd) and returns trimmed stdout. On failure it
// returns an error that includes the git command, exit status, and stderr.
func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// Prefer the context error so callers can detect cancellation.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), ctxErr)
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg == "" {
			return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// looksLikeSHA reports whether ref is a full or abbreviated hex object name.
// A SHA cannot be passed to `git clone --branch`, so we treat it specially.
func looksLikeSHA(ref string) bool {
	if len(ref) < 7 || len(ref) > 40 {
		return false
	}
	for _, r := range ref {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		case r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}

// TreelessClone clones url into destDir with history and blobs filtered out
// (`--filter=tree:0`), fetching trees and blobs lazily on demand. This keeps
// the clone cheap even for large catalog repos.
//
// If ref is empty, the repository's default branch is cloned (shallow). If ref
// names a branch or tag it is cloned directly via --branch. If ref looks like a
// commit SHA, the default branch is cloned first and then the specific commit
// is fetched and checked out (since --branch does not accept raw SHAs).
//
// destDir must not already exist (or must be empty); git creates it.
func TreelessClone(ctx context.Context, url, ref, destDir string) error {
	if url == "" {
		return errors.New("gitx: clone url is empty")
	}
	if destDir == "" {
		return errors.New("gitx: clone destination is empty")
	}

	if ref != "" && !looksLikeSHA(ref) {
		// Branch or tag: clone that ref directly, shallow + treeless.
		args := []string{
			"clone",
			"--filter=tree:0",
			"--depth", "1",
			"--branch", ref,
			"--single-branch",
			"--", url, destDir,
		}
		if _, err := runGit(ctx, "", args...); err != nil {
			return fmt.Errorf("gitx: clone %s (ref %q): %w", url, ref, err)
		}
		return nil
	}

	// No ref, or an arbitrary SHA: clone the default branch first.
	args := []string{
		"clone",
		"--filter=tree:0",
		"--depth", "1",
		"--", url, destDir,
	}
	if _, err := runGit(ctx, "", args...); err != nil {
		return fmt.Errorf("gitx: clone %s: %w", url, err)
	}

	if ref == "" {
		return nil
	}

	// Arbitrary SHA: fetch just that commit, then check it out. The commit may
	// not be reachable from the shallow default-branch tip, so fetch it
	// explicitly (still treeless).
	if _, err := runGit(ctx, destDir,
		"fetch", "--filter=tree:0", "--depth", "1", "origin", ref,
	); err != nil {
		return fmt.Errorf("gitx: fetch commit %q from %s: %w", ref, url, err)
	}
	if _, err := runGit(ctx, destDir, "checkout", "--detach", ref); err != nil {
		return fmt.Errorf("gitx: checkout commit %q: %w", ref, err)
	}
	return nil
}

// DefaultRef resolves the default branch name of the repository at repoDir
// (e.g. "main" or "master"). It first consults the remote HEAD recorded by the
// clone, falling back to the currently checked-out branch.
func DefaultRef(ctx context.Context, repoDir string) (string, error) {
	if repoDir == "" {
		return "", errors.New("gitx: repoDir is empty")
	}

	// origin/HEAD points at refs/remotes/origin/<default>; symbolic-ref reads it
	// without a network round-trip when the clone recorded it.
	if out, err := runGit(ctx, repoDir, "symbolic-ref", "--short", "refs/remotes/origin/HEAD"); err == nil {
		// Returns e.g. "origin/main"; strip the remote prefix.
		if _, branch, ok := strings.Cut(out, "/"); ok && branch != "" {
			return branch, nil
		}
		if out != "" {
			return out, nil
		}
	}

	// Fall back to the currently checked-out branch.
	if out, err := runGit(ctx, repoDir, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		if out != "" && out != "HEAD" {
			return out, nil
		}
	}

	return "", fmt.Errorf("gitx: could not determine default branch for %s", repoDir)
}

// SubtreeSHA returns the git tree object SHA of subpath within the repository
// at repoDir, resolved at ref. This is a skill's Revision: it changes only when
// the content of that specific subdirectory changes, so a commit touching a
// different skill never alters it.
//
// subpath is the directory path relative to the repository root (e.g.
// "grill-with-docs"). An empty subpath resolves to the root tree of ref.
func SubtreeSHA(ctx context.Context, repoDir, ref, subpath string) (string, error) {
	if repoDir == "" {
		return "", errors.New("gitx: repoDir is empty")
	}
	if ref == "" {
		ref = "HEAD"
	}

	clean := cleanSubpath(subpath)
	if clean == "" {
		// Root tree: resolve ref^{tree}.
		out, err := runGit(ctx, repoDir, "rev-parse", "--verify", ref+"^{tree}")
		if err != nil {
			return "", fmt.Errorf("gitx: resolve root tree of %q: %w", ref, err)
		}
		return out, nil
	}

	// `git ls-tree <ref> <path>` lists the single entry for subpath. We use the
	// porcelain-stable -z output and parse the object id, asserting it is a tree.
	out, err := runGit(ctx, repoDir, "ls-tree", "-z", ref, clean)
	if err != nil {
		return "", fmt.Errorf("gitx: ls-tree %q at %q: %w", clean, ref, err)
	}
	if out == "" {
		return "", fmt.Errorf("gitx: %q not found at %q", clean, ref)
	}

	// Each NUL-terminated record is: "<mode> <type> <object>\t<path>".
	for _, record := range strings.Split(out, "\x00") {
		if record == "" {
			continue
		}
		meta, _, found := strings.Cut(record, "\t")
		if !found {
			continue
		}
		fields := strings.Fields(meta)
		if len(fields) < 3 {
			continue
		}
		objType, objID := fields[1], fields[2]
		if objType != "tree" {
			return "", fmt.Errorf("gitx: %q at %q is a %s, not a directory", clean, ref, objType)
		}
		return objID, nil
	}

	return "", fmt.Errorf("gitx: %q not found at %q", clean, ref)
}

// MaterializeSubdir extracts the files under subpath (resolved at the currently
// checked-out ref of repoDir) into destDir, so the store can consume them as a
// skill directory. The contents of the subdir are placed directly into destDir
// (i.e. destDir becomes the skill root, not destDir/<subpath>).
//
// destDir is created if it does not exist. Because the source repo is a
// treeless clone, the relevant trees and blobs are fetched lazily by the
// underlying git read-tree/checkout. An empty subpath materializes the whole
// repository worktree (excluding the .git directory).
func MaterializeSubdir(ctx context.Context, repoDir, subpath, destDir string) error {
	if repoDir == "" {
		return errors.New("gitx: repoDir is empty")
	}
	if destDir == "" {
		return errors.New("gitx: destination is empty")
	}

	clean := cleanSubpath(subpath)

	// Ensure the requested subpath exists in the working tree and is a
	// directory before copying. This also realizes lazily-fetched blobs.
	srcDir := repoDir
	if clean != "" {
		srcDir = filepath.Join(repoDir, filepath.FromSlash(clean))
	}
	info, err := os.Stat(srcDir)
	if err != nil {
		// The blobs may not be present yet in a treeless clone; force a
		// checkout of the path to materialize them, then retry the stat.
		if checkoutErr := checkoutPath(ctx, repoDir, clean); checkoutErr != nil {
			return fmt.Errorf("gitx: materialize %q: %w", subpath, err)
		}
		info, err = os.Stat(srcDir)
		if err != nil {
			return fmt.Errorf("gitx: materialize %q: %w", subpath, err)
		}
	}
	if !info.IsDir() {
		return fmt.Errorf("gitx: %q is not a directory", subpath)
	}

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("gitx: create destination %s: %w", destDir, err)
	}

	if err := copyTree(srcDir, destDir); err != nil {
		return fmt.Errorf("gitx: copy %q into %s: %w", subpath, destDir, err)
	}
	return nil
}

// checkoutPath forces git to write the given path (relative to the repo root)
// into the working tree, fetching lazily-filtered blobs as needed. An empty
// path checks out everything.
func checkoutPath(ctx context.Context, repoDir, clean string) error {
	args := []string{"checkout", "HEAD", "--"}
	if clean == "" {
		args = append(args, ".")
	} else {
		args = append(args, filepath.FromSlash(clean))
	}
	if _, err := runGit(ctx, repoDir, args...); err != nil {
		return err
	}
	return nil
}

// cleanSubpath normalizes a repo-relative subpath: forward slashes, no leading
// "./" or "/", and "." / "" collapse to "" (meaning the repo root).
func cleanSubpath(subpath string) string {
	p := strings.TrimSpace(subpath)
	p = filepath.ToSlash(p)
	p = strings.TrimPrefix(p, "./")
	p = strings.Trim(p, "/")
	if p == "." {
		return ""
	}
	return p
}

// copyTree recursively copies the contents of src into dst, skipping any nested
// ".git" directory. Regular files, directories, and symlinks are reproduced;
// file modes are preserved.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		// Never copy a nested git directory into the materialized skill.
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		target := filepath.Join(dst, rel)

		info, err := d.Info()
		if err != nil {
			return err
		}

		switch {
		case d.IsDir():
			return os.MkdirAll(target, dirPerm(info.Mode()))
		case info.Mode()&os.ModeSymlink != 0:
			linkDest, err := os.Readlink(path)
			if err != nil {
				return err
			}
			// Replace any pre-existing entry to keep the copy idempotent.
			_ = os.Remove(target)
			return os.Symlink(linkDest, target)
		default:
			return copyFile(path, target, info.Mode().Perm())
		}
	})
}

// dirPerm derives a directory permission from a source mode, defaulting to
// 0755 when the source carries no useful permission bits.
func dirPerm(mode os.FileMode) os.FileMode {
	perm := mode.Perm()
	if perm == 0 {
		return 0o755
	}
	return perm
}

// copyFile copies the file at src to dst, creating parent directories and
// applying perm to the destination.
func copyFile(src, dst string, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}

	if _, err := out.ReadFrom(in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
