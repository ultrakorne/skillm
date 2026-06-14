// Package store manages skillm's Home directory: resolving its location,
// bootstrapping its layout, and adding/removing skill directories within it.
//
// Home layout (see docs/PLAN.md §2):
//
//	<home>/
//	├── config.toml
//	├── state.toml
//	└── skills/
//	    └── <skill-id>/
//	        └── SKILL.md
//
// This package owns the skills/ subtree; reading/writing config.toml and
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

// skillsSubdir is the directory under Home that holds per-skill directories.
const skillsSubdir = "skills"

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

// EnsureHome creates the Home directory and its skills/ subdirectory if they do
// not already exist. It is idempotent.
func EnsureHome(home string) error {
	if home == "" {
		return errors.New("home directory path is empty")
	}
	if err := os.MkdirAll(SkillsDir(home), 0o755); err != nil {
		return fmt.Errorf("create home layout at %s: %w", home, err)
	}
	return nil
}

// SkillsDir returns the directory holding per-skill directories: <home>/skills.
func SkillsDir(home string) string {
	return filepath.Join(home, skillsSubdir)
}

// SkillDir returns the directory for a single skill: <home>/skills/<id>.
func SkillDir(home, id string) string {
	return filepath.Join(SkillsDir(home), id)
}

// Exists reports whether a skill with the given id is present in Home (i.e. its
// directory exists). Any stat error other than not-exist is treated as absent.
func Exists(home, id string) bool {
	info, err := os.Stat(SkillDir(home, id))
	if err != nil {
		return false
	}
	return info.IsDir()
}

// AddSkillDir copies the skill directory at srcDir recursively into Home at
// SkillDir(home, id). It errors if a skill with that id already exists in Home
// (the caller resolves collisions with --as, per PLAN §3). The skills/ parent
// is created if missing.
//
// On any failure mid-copy, the partially written destination is removed so Home
// is not left with a half-copied skill.
func AddSkillDir(home, id, srcDir string) error {
	src, err := os.Stat(srcDir)
	if err != nil {
		return fmt.Errorf("read source skill directory %s: %w", srcDir, err)
	}
	if !src.IsDir() {
		return fmt.Errorf("source skill path %s is not a directory", srcDir)
	}

	if Exists(home, id) {
		return fmt.Errorf("skill %q already exists in Home; use `skillm update` to refresh it or `--as <name>` to add it under a different id", id)
	}

	if err := os.MkdirAll(SkillsDir(home), 0o755); err != nil {
		return fmt.Errorf("create skills directory: %w", err)
	}

	dst := SkillDir(home, id)
	if err := copyTree(srcDir, dst); err != nil {
		// Best-effort cleanup of a partially copied skill.
		_ = os.RemoveAll(dst)
		return fmt.Errorf("copy skill %q into Home: %w", id, err)
	}
	return nil
}

// RemoveSkillDir deletes a skill's directory from Home. It is not an error if
// the skill is already absent (idempotent), matching Remove's "no dangling
// state" guarantee.
func RemoveSkillDir(home, id string) error {
	if err := os.RemoveAll(SkillDir(home, id)); err != nil {
		return fmt.Errorf("remove skill %q from Home: %w", id, err)
	}
	return nil
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
