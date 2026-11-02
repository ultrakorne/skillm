// Package skill models an on-disk Skill and parses its SKILL.md entry point.
//
// A Skill is a directory whose entry point is a SKILL.md file. SKILL.md is an
// optional YAML frontmatter block delimited by lines containing only "---",
// followed by a Markdown body:
//
//	---
//	name: grill-with-docs
//	description: Grill things, with documentation.
//	---
//	# body markdown...
//
// Files without a frontmatter block are tolerated: the whole file is treated as
// the body, and Name/Description are left empty (Name falling back to the
// directory id where applicable).
package skill

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/goccy/go-yaml"
)

// SkillFile is the conventional entry-point filename for a Skill directory.
const SkillFile = "SKILL.md"

// Skill is a parsed representation of a single skill on disk.
//
// Name and Description are pulled out of the YAML frontmatter for convenience;
// the complete frontmatter is preserved (in document order) in Frontmatter so
// callers can read any additional keys without re-parsing the file. Body holds
// the Markdown content following the frontmatter block.
type Skill struct {
	// ID is the skill's stable identifier — by convention the base name of its
	// directory. Empty when the skill was parsed from a bare file path with no
	// owning directory context.
	ID string

	// Dir is the absolute-or-relative directory the skill lives in (as supplied
	// to Load). Empty when parsed from a standalone SKILL.md path that is not a
	// <dir>/SKILL.md (ParseSKILLMD still records the file's directory).
	Dir string

	// Name and Description are read from the frontmatter keys of the same name.
	Name        string
	Description string

	// Frontmatter is the full, order-preserving set of frontmatter key/values.
	// It is nil when the file had no frontmatter block.
	Frontmatter yaml.MapSlice

	// Body is the Markdown content after the frontmatter block (or the whole
	// file when there is no frontmatter).
	Body string
}

// HasFrontmatter reports whether the parsed SKILL.md contained a frontmatter
// block (even an empty one between two "---" delimiters).
func (s *Skill) HasFrontmatter() bool {
	return s.Frontmatter != nil
}

// Load reads <dir>/SKILL.md and returns the parsed Skill. The skill's ID
// defaults to filepath.Base(dir) and Dir is set to dir. If the frontmatter has
// no name, Name falls back to the ID so callers always have a display name.
func Load(dir string) (*Skill, error) {
	path := filepath.Join(dir, SkillFile)
	s, err := ParseSKILLMD(path)
	if err != nil {
		return nil, err
	}
	s.Dir = dir
	s.ID = filepath.Base(dir)
	if s.Name == "" {
		s.Name = s.ID
	}
	return s, nil
}

// ParseSKILLMD reads and parses a SKILL.md file at path. It tolerates files
// without a frontmatter block (the whole file becomes the Body). Dir is set to
// the file's containing directory; Load overrides Dir/ID with the skill dir.
func ParseSKILLMD(path string) (*Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	s := &Skill{Dir: filepath.Dir(path)}

	front, body, ok := splitFrontmatter(data)
	if !ok {
		// No frontmatter block: the entire file is the body.
		s.Body = string(data)
		return s, nil
	}

	s.Body = string(body)

	// Always record that a frontmatter block was present, even if empty.
	s.Frontmatter = yaml.MapSlice{}
	if len(bytes.TrimSpace(front)) > 0 {
		var fm yaml.MapSlice
		if err := yaml.Unmarshal(front, &fm); err != nil {
			loose, ok := parseLooseFrontmatter(front)
			if !ok {
				// Without the YAML source excerpt the library adds by
				// default, so the error fits on one line.
				return nil, fmt.Errorf("parse frontmatter in %s: %s", path, yaml.FormatError(err, false, false))
			}
			fm = loose
		}
		s.Frontmatter = fm
	}

	s.Name = stringValue(s.Frontmatter, "name")
	s.Description = stringValue(s.Frontmatter, "description")

	return s, nil
}

// topLevelKey matches a frontmatter line that starts a top-level key: an
// unindented key followed by ":" and then a space or the end of the line.
var topLevelKey = regexp.MustCompile(`^([A-Za-z0-9_][A-Za-z0-9_.-]*)[ \t]*:(?:[ \t]|$)`)

// parseLooseFrontmatter reads frontmatter that is not valid YAML the way agents
// such as Claude Code do: as top-level "key: value" lines. Hand-written
// SKILL.md files often hold an unquoted value with a ": " in it
// ("description: Do X: then Y"), which strict YAML rejects.
//
// Each top-level key, with its indented continuation lines, is parsed as YAML
// on its own, so keys that are valid keep their types (booleans, lists, maps).
// A key that still fails becomes the raw text after its first ":", with
// continuation lines folded in by single spaces. ok is false when some
// unindented line does not start a key, as the block is then not a simple
// mapping and guessing its meaning would be worse than reporting the error.
func parseLooseFrontmatter(front []byte) (fm yaml.MapSlice, ok bool) {
	type chunk struct {
		key   string
		lines []string
	}
	var chunks []chunk
	for _, line := range strings.Split(string(front), "\n") {
		line = strings.TrimRight(line, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if m := topLevelKey.FindStringSubmatch(line); m != nil {
			chunks = append(chunks, chunk{key: m[1], lines: []string{line}})
			continue
		}
		if len(chunks) == 0 || line == trimmed {
			return nil, false // unindented, not a key: not a plain mapping
		}
		last := &chunks[len(chunks)-1]
		last.lines = append(last.lines, line)
	}
	if len(chunks) == 0 {
		return nil, false
	}

	fm = make(yaml.MapSlice, 0, len(chunks))
	for _, c := range chunks {
		var one yaml.MapSlice
		if err := yaml.Unmarshal([]byte(strings.Join(c.lines, "\n")), &one); err == nil && len(one) == 1 {
			fm = append(fm, one[0])
			continue
		}
		parts := []string{strings.TrimSpace(c.lines[0][strings.Index(c.lines[0], ":")+1:])}
		for _, l := range c.lines[1:] {
			parts = append(parts, strings.TrimSpace(l))
		}
		fm = append(fm, yaml.MapItem{Key: c.key, Value: strings.TrimSpace(strings.Join(parts, " "))})
	}
	return fm, true
}

// splitFrontmatter separates a YAML frontmatter block from the Markdown body.
//
// A frontmatter block exists only when the very first line of the file (after
// an optional UTF-8 BOM and ignoring trailing whitespace) is exactly "---" and
// a later line is exactly "---". front is the bytes between the two delimiters;
// body is everything after the closing delimiter. ok is false when there is no
// well-formed opening/closing pair, in which case the caller treats the entire
// file as the body.
func splitFrontmatter(data []byte) (front, body []byte, ok bool) {
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF}) // strip UTF-8 BOM

	// Normalise the leading delimiter line. The file must start with "---"
	// followed by a newline (allowing trailing spaces/CR on that line).
	rest, found := chompDelimiter(data)
	if !found {
		return nil, nil, false
	}

	// Find the closing delimiter line within the remainder.
	lineStart := 0
	for lineStart <= len(rest) {
		nl := bytes.IndexByte(rest[lineStart:], '\n')
		var line []byte
		var next int
		if nl < 0 {
			line = rest[lineStart:]
			next = len(rest)
		} else {
			line = rest[lineStart : lineStart+nl]
			next = lineStart + nl + 1
		}
		if isDelimiter(line) {
			return rest[:lineStart], rest[next:], true
		}
		if nl < 0 {
			break
		}
		lineStart = next
	}

	// Opening delimiter with no closing delimiter: not valid frontmatter.
	return nil, nil, false
}

// chompDelimiter returns the bytes following an opening "---" delimiter line at
// the start of data, and whether such a line was present.
func chompDelimiter(data []byte) (rest []byte, ok bool) {
	nl := bytes.IndexByte(data, '\n')
	var first []byte
	if nl < 0 {
		first = data
	} else {
		first = data[:nl]
	}
	if !isDelimiter(first) {
		return nil, false
	}
	if nl < 0 {
		return nil, true
	}
	return data[nl+1:], true
}

// isDelimiter reports whether a line (without its trailing newline) is a
// frontmatter delimiter — exactly "---" once carriage returns and surrounding
// whitespace are trimmed.
func isDelimiter(line []byte) bool {
	return bytes.Equal(bytes.TrimSpace(line), []byte("---"))
}

// stringValue returns the string value for key in an ordered frontmatter map,
// or "" if the key is absent or not a string-coercible scalar.
func stringValue(fm yaml.MapSlice, key string) string {
	for _, item := range fm {
		k, ok := item.Key.(string)
		if !ok || k != key {
			continue
		}
		switch v := item.Value.(type) {
		case string:
			return v
		case nil:
			return ""
		default:
			return fmt.Sprintf("%v", v)
		}
	}
	return ""
}
