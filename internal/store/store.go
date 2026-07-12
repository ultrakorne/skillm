// Package store manages skillm's Home directory — resolving its location and
// bootstrapping its layout — and holds the directory-copy primitives skillm
// uses to write a skill's canonical install copy.
//
// Home layout:
//
//	<home>/
//	├── config.toml
//	└── state.toml
//
// Home holds only skillm's config and registry; a skill's files live solely in
// its canonical install stores (~/.agents/skills at Global scope,
// <project>/.agents/skills at Local scope). There is no longer a Home skills/
// library — the installs are the only copies. Reading/writing config.toml and
// state.toml belongs to the config and state packages respectively.
package store

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// dirName is the conventional Home directory name under the user's home.
const dirName = ".skillm"

// Home resolves the Home directory, in precedence order:
//
//  1. override (the --home flag value), when non-empty;
//  2. the $SKILLM_HOME environment variable, when set and non-empty;
//  3. the default ~/.skillm.
//
// The returned path is cleaned but not guaranteed to exist — call EnsureHome to
// create the layout.
func Home(override string) (string, error) {
	if override != "" {
		return filepath.Clean(override), nil
	}
	if env := os.Getenv("SKILLM_HOME"); env != "" {
		return filepath.Clean(env), nil
	}
	hd, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("determine user home directory: %w", err)
	}
	return filepath.Join(hd, dirName), nil
}

// EnsureHome creates the Home directory if it does not already exist. Home holds
// only config.toml and state.toml, so this is just a MkdirAll of the home dir
// itself. It is idempotent.
func EnsureHome(home string) error {
	if home == "" {
		return errors.New("home directory path is empty")
	}
	if err := os.MkdirAll(home, 0o755); err != nil {
		return fmt.Errorf("create home directory at %s: %w", home, err)
	}
	return nil
}

// CopyDir recursively copies the skill directory tree at srcDir into dstDir,
// reproducing regular files, subdirectories, and symlinks. dstDir and its parent
// are created if missing; existing files are overwritten. It does NOT first
// clear dstDir — callers that need a clean replacement use ReplaceDir. This is
// the primitive used to materialize a canonical install copy from a staged or
// fetched source directory.
func CopyDir(srcDir, dstDir string) error {
	info, err := os.Stat(srcDir)
	if err != nil {
		return fmt.Errorf("read source directory %s: %w", srcDir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("source path %s is not a directory", srcDir)
	}
	return copyTree(srcDir, dstDir)
}

// ReplaceDir replaces dstDir with a fresh copy of srcDir. It copies srcDir into
// a temporary sibling first, then removes any existing dstDir and renames the
// staging copy into place. This is not atomic — between the remove and the
// rename there is a brief window where dstDir does not exist — but it does
// guarantee the destination is never left half-written: the copy is fully
// staged before the old dstDir is touched, so a failure mid-copy leaves the
// existing dstDir intact. The parent of dstDir is created if missing. This is
// how a Vendored copy is written and refreshed.
func ReplaceDir(srcDir, dstDir string) error {
	if _, err := os.Stat(srcDir); err != nil {
		return fmt.Errorf("read source directory %s: %w", srcDir, err)
	}
	if err := os.MkdirAll(filepath.Dir(dstDir), 0o755); err != nil {
		return fmt.Errorf("create parent of %s: %w", dstDir, err)
	}

	stage := dstDir + ".skillm-tmp"
	_ = os.RemoveAll(stage)
	if err := CopyDir(srcDir, stage); err != nil {
		_ = os.RemoveAll(stage)
		return fmt.Errorf("stage copy of %s: %w", srcDir, err)
	}

	if err := os.RemoveAll(dstDir); err != nil {
		_ = os.RemoveAll(stage)
		return fmt.Errorf("clear destination %s: %w", dstDir, err)
	}
	if err := os.Rename(stage, dstDir); err != nil {
		_ = os.RemoveAll(stage)
		return fmt.Errorf("install copy into %s: %w", dstDir, err)
	}
	return nil
}

// DirContentEqual reports whether the directory trees at a and b have identical
// content: the same set of relative paths, each of the same type, with regular
// files byte-for-byte equal and symlinks pointing at the same target. It is
// best-effort — any read error (or a missing tree) yields false, so a caller
// treats "can't prove equal" as "refresh needed". It lets `update` skip
// rewriting an install copy whose content already matches its source (no git
// churn).
func DirContentEqual(a, b string) bool {
	am, err := contentMap(a)
	if err != nil {
		return false
	}
	bm, err := contentMap(b)
	if err != nil {
		return false
	}
	if len(am) != len(bm) {
		return false
	}
	for rel, ea := range am {
		eb, ok := bm[rel]
		if !ok || ea != eb {
			return false
		}
	}
	return true
}

// entrySig is a content signature for one tree entry: its type and either a
// file's content hash, a symlink's target, or "" for a directory.
type entrySig struct {
	kind fs.FileMode // ModeDir, ModeSymlink, or 0 for a regular file
	data string      // file content (regular) or link target (symlink)
}

// contentMap walks root and returns a map of each entry's path (relative to
// root) to its content signature. Irregular entries (devices, sockets) are
// skipped, matching copyTree.
func contentMap(root string) (map[string]entrySig, error) {
	out := make(map[string]entrySig)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		switch {
		case d.IsDir():
			out[rel] = entrySig{kind: fs.ModeDir}
		case d.Type()&fs.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			out[rel] = entrySig{kind: fs.ModeSymlink, data: link}
		case d.Type().IsRegular():
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			out[rel] = entrySig{data: string(content)}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// copyTree recursively copies the directory tree rooted at src to dst. It
// reproduces regular files (preserving their permission bits), subdirectories,
// and symlinks (copied as symlinks, not followed). Other file types (devices,
// sockets, named pipes) are skipped — they have no place in a skill directory.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)

		switch {
		case d.IsDir():
			info, err := d.Info()
			if err != nil {
				return err
			}
			return os.MkdirAll(target, info.Mode().Perm())
		case d.Type()&fs.ModeSymlink != 0:
			return copySymlink(path, target)
		case d.Type().IsRegular():
			info, err := d.Info()
			if err != nil {
				return err
			}
			return copyFile(path, target, info.Mode().Perm())
		default:
			// Skip irregular entries (devices, sockets, fifos).
			return nil
		}
	})
}

// copyFile copies a regular file from src to dst with the given permission
// bits. The destination's parent directory is assumed to already exist.
func copyFile(src, dst string, perm fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}

	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// copySymlink recreates the symlink at src (by its link target) at dst. The
// link is copied verbatim and not dereferenced.
func copySymlink(src, dst string) error {
	link, err := os.Readlink(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.Symlink(link, dst)
}
