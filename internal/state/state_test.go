package state

import (
	"os"
	"reflect"
	"testing"
	"time"
)

func TestLoadAbsentReturnsEmpty(t *testing.T) {
	home := t.TempDir()

	s, err := Load(home)
	if err != nil {
		t.Fatalf("Load on empty home: %v", err)
	}
	if len(s.Skills) != 0 {
		t.Errorf("Load absent = %+v, want empty State", s)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	home := t.TempDir()

	want := &State{Skills: []SkillEntry{
		{
			ID:          "grill-with-docs",
			Kind:        KindGit,
			Source:      "https://github.com/ultrakorne/skill-collection",
			Path:        "grill-with-docs",
			Ref:         "refs/heads/master",
			Revision:    "d97bdddcddc6818bc7ae1a0ff501912739da6cf4",
			InstalledAt: time.Date(2026, 6, 13, 18, 0, 0, 0, time.UTC),
		},
		{
			ID:          "my-local-skill",
			Kind:        KindLocal,
			Source:      "/home/ultra/dev/my-skill",
			InstalledAt: time.Date(2026, 6, 13, 18, 5, 0, 0, time.UTC),
		},
	}}

	if err := Save(home, want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load(home)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if !got.Skills[0].InstalledAt.Equal(want.Skills[0].InstalledAt) {
		t.Errorf("InstalledAt mismatch: got %v want %v", got.Skills[0].InstalledAt, want.Skills[0].InstalledAt)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round-trip mismatch:\n got = %+v\nwant = %+v", got, want)
	}
}

func TestLocalEntryOmitsGitFields(t *testing.T) {
	home := t.TempDir()

	want := &State{Skills: []SkillEntry{{
		ID:          "my-local-skill",
		Kind:        KindLocal,
		Source:      "/home/ultra/dev/my-skill",
		InstalledAt: time.Date(2026, 6, 13, 18, 5, 0, 0, time.UTC),
	}}}
	if err := Save(home, want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	data, err := os.ReadFile(Path(home))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	got := string(data)
	for _, key := range []string{"path", "ref", "revision"} {
		if containsKey(got, key) {
			t.Errorf("local entry serialized empty %q key:\n%s", key, got)
		}
	}
}

// containsKey reports whether a TOML key assignment "key =" appears in s.
func containsKey(s, key string) bool {
	for i := 0; i+len(key)+2 <= len(s); i++ {
		if s[i:i+len(key)] == key && (s[i+len(key)] == ' ' || s[i+len(key)] == '=') {
			// ensure it's at a line/token boundary (preceded by newline or space)
			if i == 0 || s[i-1] == '\n' || s[i-1] == ' ' {
				return true
			}
		}
	}
	return false
}

func TestGet(t *testing.T) {
	s := &State{Skills: []SkillEntry{
		{ID: "a", Kind: KindGit},
		{ID: "b", Kind: KindLocal},
	}}

	if e, ok := s.Get("b"); !ok || e.Kind != KindLocal {
		t.Errorf("Get(b) = %+v, %v; want b/local entry, true", e, ok)
	}
	if _, ok := s.Get("missing"); ok {
		t.Error("Get(missing) ok = true, want false")
	}
}

func TestUpsertInserts(t *testing.T) {
	s := &State{}
	s.Upsert(SkillEntry{ID: "a", Kind: KindGit})
	if len(s.Skills) != 1 || s.Skills[0].ID != "a" {
		t.Fatalf("Upsert insert: %+v", s.Skills)
	}
}

func TestUpsertReplaces(t *testing.T) {
	s := &State{Skills: []SkillEntry{
		{ID: "a", Kind: KindGit, Revision: "old"},
		{ID: "b", Kind: KindLocal},
	}}
	s.Upsert(SkillEntry{ID: "a", Kind: KindGit, Revision: "new"})

	if len(s.Skills) != 2 {
		t.Fatalf("Upsert replace changed length: %+v", s.Skills)
	}
	if e, _ := s.Get("a"); e.Revision != "new" {
		t.Errorf("Upsert did not replace: revision = %q, want new", e.Revision)
	}
	// Order preserved: a still first.
	if s.Skills[0].ID != "a" || s.Skills[1].ID != "b" {
		t.Errorf("Upsert disturbed order: %+v", s.Skills)
	}
}

func TestRemove(t *testing.T) {
	s := &State{Skills: []SkillEntry{
		{ID: "a"}, {ID: "b"}, {ID: "c"},
	}}

	if !s.Remove("b") {
		t.Error("Remove(b) = false, want true")
	}
	if len(s.Skills) != 2 || s.Skills[0].ID != "a" || s.Skills[1].ID != "c" {
		t.Errorf("Remove(b) left %+v, want [a c]", s.Skills)
	}
	if s.Remove("missing") {
		t.Error("Remove(missing) = true, want false")
	}
}

func TestSaveNilErrors(t *testing.T) {
	if err := Save(t.TempDir(), nil); err == nil {
		t.Error("Save(nil) = nil error, want error")
	}
}
