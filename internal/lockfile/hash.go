package lockfile

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// ComputeDirHash returns the vercel-compatible content hash of a skill
// directory: SHA-256 over every regular file, sorted by ICU-collated relative
// path (see collate.go), hashing each file's forward-slash relative path
// followed by its raw content. Directories named ".git" or "node_modules" are
// skipped, as are symlinks and other irregular entries — all exactly matching
// computeSkillFolderHash in vercel-labs/skills, so both tools derive the same
// computedHash for the same tree.
func ComputeDirHash(dir string) (string, error) {
	var paths []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil // symlinks and irregular entries are not hashed
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("lockfile: hash %s: %w", dir, err)
	}

	sort.Slice(paths, func(i, j int) bool { return collateLess(paths[i], paths[j]) })

	h := sha256.New()
	for _, rel := range paths {
		io.WriteString(h, rel)
		f, err := os.Open(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			return "", fmt.Errorf("lockfile: hash %s: %w", dir, err)
		}
		_, cerr := io.Copy(h, f)
		f.Close()
		if cerr != nil {
			return "", fmt.Errorf("lockfile: hash %s: %w", dir, cerr)
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
