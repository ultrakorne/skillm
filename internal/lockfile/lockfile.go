// Package lockfile reads and writes skills-lock.json — the project-scope skill
// lockfile pioneered by vercel-labs/skills and adopted by skillm as its local
// install interop format. The file lives at a project root, is meant to be
// committed, and records per skill where it came from (source/sourceType/
// sourceUrl/ref/skillPath) and a content hash of the installed folder
// (computedHash). skillm writes entries any `npx skills` user can consume and
// vice versa; state.toml remains skillm's own source of truth — the lockfile is
// the shared, committable surface.
//
// Compatibility rules honoured here (verified against vercel's local-lock.ts):
//   - version 1; skills sorted alphabetically; 2-space indent; trailing newline;
//   - entry keys in vercel's insertion order (source, sourceUrl, ref,
//     sourceType, skillPath, computedHash) so rewrites diff cleanly;
//   - unknown keys — both per-entry (e.g. "subagents") and top-level — are
//     preserved verbatim across a read-modify-write, never dropped.
package lockfile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// FileName is the lockfile's base name at a project root.
const FileName = "skills-lock.json"

// Version is the schema version skillm reads and writes. A file with a higher
// version is left untouched (see Load).
const Version = 1

// Source types shared with vercel's CLI. Only the ones skillm produces or
// imports are named; others (node_modules, well-known, …) pass through as
// opaque strings.
const (
	SourceGitHub = "github"
	SourceGitLab = "gitlab"
	SourceGit    = "git"
	SourceLocal  = "local"
)

// Entry is one skill's lock record. Known fields are typed; every key this
// package does not model is kept in Extra and round-tripped verbatim.
type Entry struct {
	// Source identifies where the skill came from: "owner/repo" for GitHub
	// HTTPS sources, the verbatim URL for SSH/non-GitHub git sources, or a
	// local path.
	Source string
	// SourceURL is the original remote URL when Source was normalized;
	// vercel sets it only for git/gitlab source types.
	SourceURL string
	// Ref is the branch or tag pinned at install time (empty = default branch).
	Ref string
	// SourceType is the provider kind: github, gitlab, git, local, ….
	SourceType string
	// SkillPath is the path of the skill's SKILL.md within the source repo,
	// forward-slashed (e.g. "skills/pdf/SKILL.md").
	SkillPath string
	// ComputedHash is the SHA-256 content hash of the installed skill folder
	// (see ComputeDirHash).
	ComputedHash string
	// Extra holds every entry key this package does not model (e.g.
	// "subagents"), preserved for round-tripping.
	Extra map[string]json.RawMessage
}

// File is a parsed skills-lock.json.
type File struct {
	// Version is the schema version found in the file.
	Version int
	// Skills maps skill name to its entry.
	Skills map[string]*Entry
	// Extra holds unknown top-level keys, preserved for round-tripping.
	Extra map[string]json.RawMessage
}

// SubdirOf returns the skill's directory within its source repo — SkillPath
// with the trailing SKILL.md stripped — in the forward-slash, ""-means-root
// form skillm's state entries use. ok is false when SkillPath is absent or not
// a SKILL.md path.
func (e *Entry) SubdirOf() (subdir string, ok bool) {
	const manifest = "SKILL.md"
	switch {
	case e.SkillPath == manifest:
		return "", true
	case strings.HasSuffix(e.SkillPath, "/"+manifest):
		return strings.TrimSuffix(e.SkillPath, "/"+manifest), true
	default:
		return "", false
	}
}

// Path returns the lockfile path for a project root.
func Path(root string) string {
	return filepath.Join(root, FileName)
}

// Load reads the lockfile at root. An absent file yields an empty, writable
// File (Version 1, no skills) and nil error. A file with a version above
// Version is returned as-is — callers must treat it as read-only (see
// Editable). Parse errors are returned, not swallowed: a corrupt committed
// lockfile is worth surfacing, not silently resetting.
func Load(root string) (*File, error) {
	data, err := os.ReadFile(Path(root))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &File{Version: Version, Skills: map[string]*Entry{}}, nil
		}
		return nil, fmt.Errorf("lockfile: read %s: %w", Path(root), err)
	}

	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return nil, fmt.Errorf("lockfile: parse %s: %w", Path(root), err)
	}

	f := &File{Version: Version, Skills: map[string]*Entry{}}
	if raw, ok := top["version"]; ok {
		if err := json.Unmarshal(raw, &f.Version); err != nil {
			return nil, fmt.Errorf("lockfile: parse %s: bad version: %w", Path(root), err)
		}
		delete(top, "version")
	}
	if raw, ok := top["skills"]; ok {
		var skills map[string]json.RawMessage
		if err := json.Unmarshal(raw, &skills); err != nil {
			return nil, fmt.Errorf("lockfile: parse %s: bad skills: %w", Path(root), err)
		}
		for name, rawEntry := range skills {
			e, err := parseEntry(rawEntry)
			if err != nil {
				return nil, fmt.Errorf("lockfile: parse %s: skill %q: %w", Path(root), name, err)
			}
			f.Skills[name] = e
		}
		delete(top, "skills")
	}
	if len(top) > 0 {
		f.Extra = top
	}
	return f, nil
}

// Editable reports whether skillm may rewrite this file: true for the schema
// version it understands. A newer version is another tool's future format —
// callers skip writing and warn instead of clobbering it.
func (f *File) Editable() bool { return f.Version <= Version }

// Save writes f to the lockfile at root in vercel-compatible layout. When f
// has no skills and no unknown top-level keys, the file is removed instead —
// an empty lockfile is noise in a repo. Callers must not Save a file for which
// Editable is false.
func Save(root string, f *File) error {
	if !f.Editable() {
		return fmt.Errorf("lockfile: refusing to rewrite %s: schema version %d is newer than supported (%d)", Path(root), f.Version, Version)
	}
	if len(f.Skills) == 0 && len(f.Extra) == 0 {
		if err := os.Remove(Path(root)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("lockfile: remove empty %s: %w", Path(root), err)
		}
		return nil
	}

	data, err := marshal(f)
	if err != nil {
		return err
	}
	if err := os.WriteFile(Path(root), data, 0o644); err != nil {
		return fmt.Errorf("lockfile: write %s: %w", Path(root), err)
	}
	return nil
}

// parseEntry decodes one skill entry, splitting known keys from Extra.
func parseEntry(raw json.RawMessage) (*Entry, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	e := &Entry{}
	str := func(key string, dst *string) error {
		r, ok := m[key]
		if !ok {
			return nil
		}
		if err := json.Unmarshal(r, dst); err != nil {
			return fmt.Errorf("bad %s: %w", key, err)
		}
		delete(m, key)
		return nil
	}
	for key, dst := range map[string]*string{
		"source":       &e.Source,
		"sourceUrl":    &e.SourceURL,
		"ref":          &e.Ref,
		"sourceType":   &e.SourceType,
		"skillPath":    &e.SkillPath,
		"computedHash": &e.ComputedHash,
	} {
		if err := str(key, dst); err != nil {
			return nil, err
		}
	}
	if len(m) > 0 {
		e.Extra = m
	}
	return e, nil
}

// marshal renders f with deterministic key order: version first, then skills
// (names byte-sorted, matching vercel's Object.keys().sort()), then any
// preserved top-level extras (byte-sorted). 2-space indent, trailing newline.
func marshal(f *File) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString("{\n")
	fmt.Fprintf(&b, "  \"version\": %d,\n", f.Version)
	b.WriteString("  \"skills\": {")

	names := make([]string, 0, len(f.Skills))
	for name := range f.Skills {
		names = append(names, name)
	}
	sort.Strings(names)
	for i, name := range names {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString("\n    ")
		writeJSONString(&b, name)
		b.WriteString(": ")
		if err := writeEntry(&b, f.Skills[name]); err != nil {
			return nil, err
		}
	}
	if len(names) > 0 {
		b.WriteString("\n  ")
	}
	b.WriteString("}")

	extraKeys := make([]string, 0, len(f.Extra))
	for k := range f.Extra {
		extraKeys = append(extraKeys, k)
	}
	sort.Strings(extraKeys)
	for _, k := range extraKeys {
		b.WriteString(",\n  ")
		writeJSONString(&b, k)
		b.WriteString(": ")
		if err := writeIndentedRaw(&b, f.Extra[k], "  "); err != nil {
			return nil, err
		}
	}

	b.WriteString("\n}\n")
	return b.Bytes(), nil
}

// writeEntry renders one entry object at the 4-space depth, keys in vercel's
// insertion order, omitting empty optionals, extras last (byte-sorted).
func writeEntry(b *bytes.Buffer, e *Entry) error {
	type kv struct {
		key string
		val string
	}
	var fields []kv
	add := func(key, val string) {
		if val != "" {
			fields = append(fields, kv{key, val})
		}
	}
	add("source", e.Source)
	add("sourceUrl", e.SourceURL)
	add("ref", e.Ref)
	add("sourceType", e.SourceType)
	add("skillPath", e.SkillPath)
	// computedHash is always written, even when somehow empty: vercel's schema
	// treats it as required.
	fields = append(fields, kv{"computedHash", e.ComputedHash})

	b.WriteString("{")
	for i, f := range fields {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString("\n      ")
		writeJSONString(b, f.key)
		b.WriteString(": ")
		writeJSONString(b, f.val)
	}

	extraKeys := make([]string, 0, len(e.Extra))
	for k := range e.Extra {
		extraKeys = append(extraKeys, k)
	}
	sort.Strings(extraKeys)
	for _, k := range extraKeys {
		b.WriteString(",\n      ")
		writeJSONString(b, k)
		b.WriteString(": ")
		if err := writeIndentedRaw(b, e.Extra[k], "      "); err != nil {
			return err
		}
	}

	b.WriteString("\n    }")
	return nil
}

// writeJSONString writes s as a JSON string literal.
func writeJSONString(b *bytes.Buffer, s string) {
	enc, _ := json.Marshal(s)
	b.Write(enc)
}

// writeIndentedRaw re-indents a preserved raw value so nested objects/arrays
// keep the file's 2-space style at the given base indent.
func writeIndentedRaw(b *bytes.Buffer, raw json.RawMessage, indent string) error {
	var tmp bytes.Buffer
	if err := json.Indent(&tmp, raw, indent, "  "); err != nil {
		return fmt.Errorf("lockfile: re-encode preserved value: %w", err)
	}
	b.Write(tmp.Bytes())
	return nil
}
