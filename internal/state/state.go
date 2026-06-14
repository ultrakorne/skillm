// Package state loads and saves skillm's machine-managed registry,
// ~/.skillm/state.toml. Unlike config, skillm owns this file and writes it
// freely. It records, per added skill, what cannot be re-derived: the Source,
// kind (git/local), the Revision recorded at add time, and the install
// timestamp. Links are deliberately not stored here — they are read live from
// disk (see PLAN §2).
package state

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"

	toml "github.com/pelletier/go-toml/v2"
)

// FileName is the base name of the registry file within Home.
const FileName = "state.toml"

// Kind enumerates how a skill was sourced.
const (
	// KindGit marks a skill fetched from a git repository (Revision-tracked).
	KindGit = "git"
	// KindLocal marks a skill copied from a local path (not Revision-tracked).
	KindLocal = "local"
)

// SkillEntry is one record in the registry. The TOML keys are snake_case to
// match state.toml (PLAN §2). For local skills, Path/Ref/Revision are empty and
// omitted on write.
type SkillEntry struct {
	// ID is the Skill ID — the directory name under ~/.skillm/skills/.
	ID string `toml:"id"`
	// Kind is "git" or "local" (see KindGit / KindLocal).
	Kind string `toml:"kind"`
	// Source is the git URL (git skills) or original local path (local skills).
	Source string `toml:"source"`
	// Path is the subpath of the skill within the repo (git skills only).
	Path string `toml:"path,omitempty"`
	// Ref is the branch/tag/sha pinned at add time (git skills only).
	Ref string `toml:"ref,omitempty"`
	// Revision is the skill subdir's git tree SHA at add time (git skills only).
	Revision string `toml:"revision,omitempty"`
	// InstalledAt is when the skill was added, serialized as RFC3339.
	InstalledAt time.Time `toml:"installed_at"`
}

// State is the in-memory form of the whole registry.
type State struct {
	// Skills is one entry per added skill. The TOML key "skills" produces the
	// [[skills]] array-of-tables in state.toml.
	Skills []SkillEntry `toml:"skills"`
	// LocalRoots is the set of project directories (absolute paths) skillm has
	// linked skills into at local scope. It is NOT link state — link existence
	// is still read live from disk — only the set of directories worth
	// scanning, so `list` and `remove` can find local links outside the current
	// directory. Roots that hold none of skillm's links are pruned by the
	// commands that touch them.
	LocalRoots []string `toml:"local_roots,omitempty"`
}

// Path returns the absolute path to the registry file inside homeDir.
func Path(homeDir string) string {
	return filepath.Join(homeDir, FileName)
}

// Load reads the registry from homeDir. An absent file yields an empty State
// and a nil error (a fresh Home has no skills yet). Other I/O or parse errors
// are returned.
func Load(homeDir string) (*State, error) {
	path := Path(homeDir)

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &State{}, nil
		}
		return nil, fmt.Errorf("state: read %s: %w", path, err)
	}

	s := &State{}
	if err := toml.Unmarshal(data, s); err != nil {
		return nil, fmt.Errorf("state: parse %s: %w", path, err)
	}
	return s, nil
}

// Save writes s to the registry file in homeDir, creating homeDir if necessary.
// skillm owns this file, so overwriting it wholesale is expected.
func Save(homeDir string, s *State) error {
	if s == nil {
		return errors.New("state: cannot save nil state")
	}

	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		return fmt.Errorf("state: create home %s: %w", homeDir, err)
	}

	data, err := toml.Marshal(s)
	if err != nil {
		return fmt.Errorf("state: marshal: %w", err)
	}

	path := Path(homeDir)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("state: write %s: %w", path, err)
	}
	return nil
}

// Get returns the entry with the given Skill ID and true, or a zero entry and
// false if no such skill is registered.
func (s *State) Get(id string) (SkillEntry, bool) {
	for _, e := range s.Skills {
		if e.ID == id {
			return e, true
		}
	}
	return SkillEntry{}, false
}

// Upsert inserts e, or replaces the existing entry with the same ID. The caller
// is responsible for persisting the State afterwards via Save.
func (s *State) Upsert(e SkillEntry) {
	for i := range s.Skills {
		if s.Skills[i].ID == e.ID {
			s.Skills[i] = e
			return
		}
	}
	s.Skills = append(s.Skills, e)
}

// Remove deletes the entry with the given Skill ID, preserving the order of the
// remaining entries. It returns true if an entry was removed, false if no such
// ID was present.
func (s *State) Remove(id string) bool {
	for i := range s.Skills {
		if s.Skills[i].ID == id {
			s.Skills = append(s.Skills[:i], s.Skills[i+1:]...)
			return true
		}
	}
	return false
}

// AddLocalRoot records dir as a directory skillm has linked a skill into at
// local scope. LocalRoots is a string set, so adding a dir already present is a
// no-op. It returns true if dir was added (the caller should then Save).
// Callers pass an absolute, cleaned path.
func (s *State) AddLocalRoot(dir string) bool {
	if slices.Contains(s.LocalRoots, dir) {
		return false
	}
	s.LocalRoots = append(s.LocalRoots, dir)
	return true
}

// RemoveLocalRoot drops dir from the tracked local roots, preserving order. It
// returns true if dir was present (the caller should then Save).
func (s *State) RemoveLocalRoot(dir string) bool {
	for i, r := range s.LocalRoots {
		if r == dir {
			s.LocalRoots = append(s.LocalRoots[:i], s.LocalRoots[i+1:]...)
			return true
		}
	}
	return false
}
