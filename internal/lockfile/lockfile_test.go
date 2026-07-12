package lockfile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The expected values in this file were generated with a verbatim JS port of
// vercel-labs/skills' computeSkillFolderHash and writeLocalLock running on
// Node 22 (see docs/vercel-skills-comparison.md). They pin byte-for-byte
// compatibility: if these tests pass, `npx skills` derives the same
// computedHash and produces the same lockfile bytes for the same content.

// writeFixtureSkill builds the reference skill tree the vectors were
// generated from, including entries the hash must SKIP (.git, node_modules,
// a symlink).
func writeFixtureSkill(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"SKILL.md":              "# My Skill\nbody text\n",
		"references/palette.md": "palette data",
		"Scripts/run_all.sh":    "#!/bin/sh\necho hi\n",
		"a-b.md":                "dash",
		"a_b.md":                "under",
		"a.b.md":                "dot",
		"A1.md":                 "num",
		".git/config":           "ignored",
		"node_modules/x.js":     "ignored",
	}
	for rel, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("SKILL.md", filepath.Join(dir, "link.md")); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestComputeDirHashMatchesVercel(t *testing.T) {
	dir := writeFixtureSkill(t)
	got, err := ComputeDirHash(dir)
	if err != nil {
		t.Fatal(err)
	}
	const want = "7a7a631c221a7fffa3707e40a509908edc1f2e1b69818ef046b34db12aafa345"
	if got != want {
		t.Errorf("hash mismatch with vercel's algorithm:\n got %s\nwant %s", got, want)
	}
}

func TestCollateMatchesNodeLocaleCompare(t *testing.T) {
	// Expected order produced by Node 22's default localeCompare for the
	// fixture paths (punctuation < digits < letters, case-folded primaries,
	// lowercase-first tiebreak, full-string primary pass before case).
	want := []string{
		"a_b.md", "a-b.md", "a.b.md", "A1.md",
		"references/palette.md", "Scripts/run_all.sh", "SKILL.md",
	}
	got := append([]string(nil), want...)
	sort.Sort(sort.Reverse(sort.StringSlice(got))) // scramble deterministically
	sort.Slice(got, func(i, j int) bool { return collateLess(got[i], got[j]) })
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("order mismatch:\n got %v\nwant %v", got, want)
	}

	// Primary difference after an earlier case difference must win:
	// "SKILL0" < "skill1" under ICU even though 's' < 'S' at the case level.
	if !collateLess("SKILL0", "skill1") {
		t.Error("full-string primary pass must precede the case pass")
	}
	if !collateLess("skill.md", "Skill.md") || !collateLess("Skill.md", "SKILL.md") {
		t.Error("lowercase must sort before uppercase on primary ties")
	}
}

func TestSaveMatchesVercelBytes(t *testing.T) {
	root := t.TempDir()
	f := &File{
		Version: 1,
		Skills: map[string]*Entry{
			"zeta": {
				Source:       "owner/repo",
				Ref:          "main",
				SourceType:   SourceGitHub,
				SkillPath:    "skills/zeta/SKILL.md",
				ComputedHash: strings.Repeat("aa", 32),
			},
			"alpha": {
				Source:       "https://gitlab.com/g/r.git",
				SourceURL:    "https://gitlab.com/g/r.git",
				SourceType:   SourceGitLab,
				SkillPath:    "SKILL.md",
				ComputedHash: strings.Repeat("bb", 32),
				Extra:        map[string]json.RawMessage{"subagents": json.RawMessage(`["","sub1"]`)},
			},
		},
	}
	if err := Save(root, f); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(Path(root))
	if err != nil {
		t.Fatal(err)
	}
	// Exact bytes JSON.stringify(sorted, null, 2)+"\n" produced for the same data.
	want := `{
  "version": 1,
  "skills": {
    "alpha": {
      "source": "https://gitlab.com/g/r.git",
      "sourceUrl": "https://gitlab.com/g/r.git",
      "sourceType": "gitlab",
      "skillPath": "SKILL.md",
      "computedHash": "` + strings.Repeat("bb", 32) + `",
      "subagents": [
        "",
        "sub1"
      ]
    },
    "zeta": {
      "source": "owner/repo",
      "ref": "main",
      "sourceType": "github",
      "skillPath": "skills/zeta/SKILL.md",
      "computedHash": "` + strings.Repeat("aa", 32) + `"
    }
  }
}
`
	if string(got) != want {
		t.Errorf("lockfile bytes differ from vercel's writer:\n got:\n%s\nwant:\n%s", got, want)
	}
}

func TestLoadRoundTripPreservesUnknownKeys(t *testing.T) {
	root := t.TempDir()
	original := `{
  "version": 1,
  "skills": {
    "pdf": {
      "source": "anthropics/skills",
      "ref": "main",
      "sourceType": "github",
      "skillPath": "document-skills/pdf/SKILL.md",
      "computedHash": "abc",
      "subagents": ["", "researcher"],
      "futureField": {"nested": true}
    }
  },
  "futureTopLevel": [1, 2]
}
`
	if err := os.WriteFile(Path(root), []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	f, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	e := f.Skills["pdf"]
	if e == nil {
		t.Fatal("pdf entry missing")
	}
	if e.Source != "anthropics/skills" || e.Ref != "main" || e.SourceType != "github" ||
		e.SkillPath != "document-skills/pdf/SKILL.md" || e.ComputedHash != "abc" {
		t.Errorf("known fields mis-parsed: %+v", e)
	}
	if sub, ok := f.Skills["pdf"].SubdirOf(); !ok || sub != "document-skills/pdf" {
		t.Errorf("SubdirOf = %q, %v", sub, ok)
	}

	// Mutate a known field and rewrite; unknown keys must survive.
	e.ComputedHash = "def"
	if err := Save(root, f); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(Path(root))
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{`"subagents"`, `"researcher"`, `"futureField"`, `"nested": true`, `"futureTopLevel"`, `"def"`} {
		if !strings.Contains(string(data), needle) {
			t.Errorf("rewrite lost %s:\n%s", needle, data)
		}
	}
}

func TestLoadAbsentAndNewerVersion(t *testing.T) {
	root := t.TempDir()
	f, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if f.Version != Version || len(f.Skills) != 0 || !f.Editable() {
		t.Errorf("absent lockfile should load empty+editable, got %+v", f)
	}

	if err := os.WriteFile(Path(root), []byte(`{"version": 99, "skills": {}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err = Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if f.Editable() {
		t.Error("a newer schema version must not be editable")
	}
	if err := Save(root, f); err == nil {
		t.Error("Save must refuse a newer schema version")
	}
}

func TestSaveRemovesEmptyLockfile(t *testing.T) {
	root := t.TempDir()
	f := &File{Version: 1, Skills: map[string]*Entry{"x": {Source: "o/r", SourceType: "github", ComputedHash: "h"}}}
	if err := Save(root, f); err != nil {
		t.Fatal(err)
	}
	delete(f.Skills, "x")
	if err := Save(root, f); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(Path(root)); !os.IsNotExist(err) {
		t.Error("empty lockfile should be removed")
	}
}

func TestGitSourceFields(t *testing.T) {
	cases := []struct {
		url                    string
		source, sourceURL, typ string
	}{
		{"https://github.com/owner/repo", "owner/repo", "", "github"},
		{"https://github.com/owner/repo.git", "owner/repo", "", "github"},
		{"git@github.com:owner/repo.git", "git@github.com:owner/repo.git", "", "github"},
		{"https://gitlab.com/group/proj.git", "https://gitlab.com/group/proj.git", "https://gitlab.com/group/proj.git", "gitlab"},
		{"https://git.sr.ht/~me/repo", "https://git.sr.ht/~me/repo", "https://git.sr.ht/~me/repo", "git"},
	}
	for _, c := range cases {
		s, u, ty := GitSourceFields(c.url)
		if s != c.source || u != c.sourceURL || ty != c.typ {
			t.Errorf("GitSourceFields(%q) = (%q, %q, %q), want (%q, %q, %q)",
				c.url, s, u, ty, c.source, c.sourceURL, c.typ)
		}
	}
}

func TestCloneURL(t *testing.T) {
	cases := []struct {
		entry Entry
		want  string
		ok    bool
	}{
		{Entry{Source: "owner/repo", SourceType: "github"}, "https://github.com/owner/repo.git", true},
		{Entry{Source: "git@github.com:owner/repo.git", SourceType: "github"}, "git@github.com:owner/repo.git", true},
		{Entry{Source: "https://gitlab.com/g/r.git", SourceURL: "https://gitlab.com/g/r.git", SourceType: "gitlab"}, "https://gitlab.com/g/r.git", true},
		{Entry{Source: "g/r", SourceType: "gitlab"}, "", false}, // legacy ambiguous shorthand
		{Entry{Source: "../somewhere", SourceType: "local"}, "", false},
		{Entry{Source: "pdf", SourceType: "well-known"}, "", false},
	}
	for _, c := range cases {
		got, err := c.entry.CloneURL()
		if c.ok && (err != nil || got != c.want) {
			t.Errorf("CloneURL(%+v) = %q, %v; want %q", c.entry, got, err, c.want)
		}
		if !c.ok && err == nil {
			t.Errorf("CloneURL(%+v) should error, got %q", c.entry, got)
		}
	}
}
