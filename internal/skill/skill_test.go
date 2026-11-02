package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
)

func TestParseSKILLMD_WithFrontmatter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, SkillFile)
	content := "---\n" +
		"name: grill-with-docs\n" +
		"description: Grill things, with documentation.\n" +
		"license: MIT\n" +
		"---\n" +
		"# Grill\n\nBody text here.\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := ParseSKILLMD(path)
	if err != nil {
		t.Fatalf("ParseSKILLMD: %v", err)
	}
	if !s.HasFrontmatter() {
		t.Fatal("expected HasFrontmatter to be true")
	}
	if s.Name != "grill-with-docs" {
		t.Errorf("Name = %q, want %q", s.Name, "grill-with-docs")
	}
	if s.Description != "Grill things, with documentation." {
		t.Errorf("Description = %q", s.Description)
	}
	if got := stringValue(s.Frontmatter, "license"); got != "MIT" {
		t.Errorf("license = %q, want MIT (extra keys must remain accessible)", got)
	}
	if want := "# Grill\n\nBody text here.\n"; s.Body != want {
		t.Errorf("Body = %q, want %q", s.Body, want)
	}
}

func TestParseSKILLMD_NoFrontmatter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, SkillFile)
	content := "# Just a heading\n\nNo frontmatter at all.\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := ParseSKILLMD(path)
	if err != nil {
		t.Fatalf("ParseSKILLMD: %v", err)
	}
	if s.HasFrontmatter() {
		t.Error("expected HasFrontmatter to be false")
	}
	if s.Name != "" || s.Description != "" {
		t.Errorf("Name/Description should be empty without frontmatter, got %q / %q", s.Name, s.Description)
	}
	if s.Body != content {
		t.Errorf("Body = %q, want whole file %q", s.Body, content)
	}
}

func TestParseSKILLMD_EmptyFrontmatter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, SkillFile)
	content := "---\n---\nbody\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := ParseSKILLMD(path)
	if err != nil {
		t.Fatalf("ParseSKILLMD: %v", err)
	}
	if !s.HasFrontmatter() {
		t.Error("an empty --- / --- block still counts as frontmatter present")
	}
	if s.Body != "body\n" {
		t.Errorf("Body = %q", s.Body)
	}
}

func TestParseSKILLMD_OpenWithoutClose(t *testing.T) {
	// A leading "---" with no closing delimiter is not valid frontmatter; the
	// whole file is treated as body.
	dir := t.TempDir()
	path := filepath.Join(dir, SkillFile)
	content := "---\nname: dangling\nstill body\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := ParseSKILLMD(path)
	if err != nil {
		t.Fatalf("ParseSKILLMD: %v", err)
	}
	if s.HasFrontmatter() {
		t.Error("dangling open delimiter must not be treated as frontmatter")
	}
	if s.Body != content {
		t.Errorf("Body = %q, want whole file", s.Body)
	}
}

func TestLoad_DefaultsIDAndName(t *testing.T) {
	base := t.TempDir()
	skillDir := filepath.Join(base, "my-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// No name key in frontmatter: Name should fall back to the id.
	content := "---\ndescription: a thing\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(skillDir, SkillFile), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := Load(skillDir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.ID != "my-skill" {
		t.Errorf("ID = %q, want my-skill", s.ID)
	}
	if s.Dir != skillDir {
		t.Errorf("Dir = %q, want %q", s.Dir, skillDir)
	}
	if s.Name != "my-skill" {
		t.Errorf("Name = %q, want fallback to id my-skill", s.Name)
	}
	if s.Description != "a thing" {
		t.Errorf("Description = %q", s.Description)
	}
}

func TestLoad_NameFromFrontmatterWins(t *testing.T) {
	base := t.TempDir()
	skillDir := filepath.Join(base, "dir-id")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: Pretty Name\n---\n"
	if err := os.WriteFile(filepath.Join(skillDir, SkillFile), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := Load(skillDir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.ID != "dir-id" {
		t.Errorf("ID = %q, want dir-id", s.ID)
	}
	if s.Name != "Pretty Name" {
		t.Errorf("Name = %q, want frontmatter value", s.Name)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("expected error for missing SKILL.md")
	}
}

// writeSKILLMD writes content to a SKILL.md in a fresh temp dir and returns
// its path.
func writeSKILLMD(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), SkillFile)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestParseSKILLMD_LooseFrontmatter(t *testing.T) {
	// An unquoted value holding ": " is not valid YAML, but agents read it as
	// plain "key: value" lines. Valid keys around it keep their YAML types.
	path := writeSKILLMD(t, "---\n"+
		"name: software-factory\n"+
		"description: Shared language for designing a software factory: the tasks it runs\n"+
		"  and the services it plugs into.\n"+
		"disable-model-invocation: true\n"+
		"metadata:\n"+
		"  author: someone\n"+
		"---\n"+
		"body\n")

	s, err := ParseSKILLMD(path)
	if err != nil {
		t.Fatalf("ParseSKILLMD: %v", err)
	}
	if s.Name != "software-factory" {
		t.Errorf("Name = %q", s.Name)
	}
	if want := "Shared language for designing a software factory: the tasks it runs and the services it plugs into."; s.Description != want {
		t.Errorf("Description = %q, want %q", s.Description, want)
	}
	var keys []string
	for _, item := range s.Frontmatter {
		keys = append(keys, item.Key.(string))
		switch item.Key {
		case "disable-model-invocation":
			if item.Value != true {
				t.Errorf("disable-model-invocation = %#v, want bool true", item.Value)
			}
		case "metadata":
			if _, ok := item.Value.(map[string]any); !ok {
				if _, ok := item.Value.(yaml.MapSlice); !ok {
					t.Errorf("metadata = %#v, want a mapping", item.Value)
				}
			}
		}
	}
	if got, want := strings.Join(keys, ","), "name,description,disable-model-invocation,metadata"; got != want {
		t.Errorf("keys = %s, want %s (document order)", got, want)
	}
	if s.Body != "body\n" {
		t.Errorf("Body = %q", s.Body)
	}
}

func TestParseSKILLMD_UnparseableFrontmatter(t *testing.T) {
	// An unindented line that is not a key cannot be read even leniently.
	path := writeSKILLMD(t, "---\nname: x\njust some prose\n---\nbody\n")
	if _, err := ParseSKILLMD(path); err == nil || !strings.Contains(err.Error(), "parse frontmatter") {
		t.Fatalf("err = %v, want a parse frontmatter error", err)
	}
}
