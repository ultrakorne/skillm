package skill

import (
	"os"
	"path/filepath"
	"testing"
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
