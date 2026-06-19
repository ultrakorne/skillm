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

func TestLocalRoots(t *testing.T) {
	s := &State{}

	if !s.AddLocalRoot("/projA") {
		t.Error("AddLocalRoot(/projA) = false, want true (new)")
	}
	if s.AddLocalRoot("/projA") {
		t.Error("AddLocalRoot(/projA) again = true, want false (duplicate)")
	}
	if !s.AddLocalRoot("/projB") {
		t.Error("AddLocalRoot(/projB) = false, want true")
	}
	if !reflect.DeepEqual(s.LocalRoots, []string{"/projA", "/projB"}) {
		t.Fatalf("LocalRoots = %v, want [/projA /projB]", s.LocalRoots)
	}

	if !s.RemoveLocalRoot("/projA") {
		t.Error("RemoveLocalRoot(/projA) = false, want true")
	}
	if s.RemoveLocalRoot("/projA") {
		t.Error("RemoveLocalRoot(/projA) again = true, want false (absent)")
	}
	if !reflect.DeepEqual(s.LocalRoots, []string{"/projB"}) {
		t.Fatalf("LocalRoots after remove = %v, want [/projB]", s.LocalRoots)
	}
}

func TestLocalRootsRoundTrip(t *testing.T) {
	home := t.TempDir()

	want := &State{LocalRoots: []string{"/home/me/projA", "/home/me/projB"}}
	if err := Save(home, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(home)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(got.LocalRoots, want.LocalRoots) {
		t.Errorf("round-trip LocalRoots = %v, want %v", got.LocalRoots, want.LocalRoots)
	}
}

func TestVendoredRoots(t *testing.T) {
	s := &State{Skills: []SkillEntry{{ID: "a", Kind: KindGit}, {ID: "b", Kind: KindLocal}}}

	// Add to a known skill.
	if !s.AddVendoredRoot("a", "/projA") {
		t.Fatal("AddVendoredRoot(a, /projA) = false, want true")
	}
	if s.AddVendoredRoot("a", "/projA") {
		t.Error("AddVendoredRoot(a, /projA) again = true, want false (already present)")
	}
	if !s.AddVendoredRoot("a", "/projB") {
		t.Error("AddVendoredRoot(a, /projB) = false, want true")
	}
	if !reflect.DeepEqual(s.VendoredRoots("a"), []string{"/projA", "/projB"}) {
		t.Fatalf("VendoredRoots(a) = %v, want [/projA /projB]", s.VendoredRoots("a"))
	}

	// Unknown skill: no-op, not a panic.
	if s.AddVendoredRoot("missing", "/x") {
		t.Error("AddVendoredRoot on unknown skill = true, want false")
	}
	if s.VendoredRoots("missing") != nil {
		t.Error("VendoredRoots(missing) should be nil")
	}

	// Remove.
	if !s.RemoveVendoredRoot("a", "/projA") {
		t.Error("RemoveVendoredRoot(a, /projA) = false, want true")
	}
	if s.RemoveVendoredRoot("a", "/projA") {
		t.Error("RemoveVendoredRoot(a, /projA) again = true, want false (absent)")
	}
	if !reflect.DeepEqual(s.VendoredRoots("a"), []string{"/projB"}) {
		t.Fatalf("VendoredRoots(a) after remove = %v, want [/projB]", s.VendoredRoots("a"))
	}
	// Other skills are unaffected.
	if len(s.VendoredRoots("b")) != 0 {
		t.Errorf("VendoredRoots(b) = %v, want empty", s.VendoredRoots("b"))
	}
}

func TestVendoredAtRoundTripAndOmitted(t *testing.T) {
	home := t.TempDir()
	want := &State{Skills: []SkillEntry{
		{ID: "a", Kind: KindGit, VendoredAt: []string{"/home/me/projA"}},
		{ID: "b", Kind: KindLocal}, // no vendored copies
	}}
	if err := Save(home, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(home)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(got.VendoredRoots("a"), []string{"/home/me/projA"}) {
		t.Errorf("round-trip VendoredAt(a) = %v, want [/home/me/projA]", got.VendoredRoots("a"))
	}
	// A skill with no vendored copies must not serialize the key.
	data, err := os.ReadFile(Path(home))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	lines := 0
	for _, line := range splitLines(string(data)) {
		if containsKey(line, "vendored_at") {
			lines++
		}
	}
	if lines != 1 {
		t.Errorf("vendored_at should appear exactly once (only skill a), found %d:\n%s", lines, data)
	}
}

// splitLines splits s on newlines for a per-line key check.
func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

func TestLocalRootsOmittedWhenEmpty(t *testing.T) {
	home := t.TempDir()
	if err := Save(home, &State{Skills: []SkillEntry{{ID: "x", Kind: KindLocal}}}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, err := os.ReadFile(Path(home))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if containsKey(string(data), "local_roots") {
		t.Errorf("empty LocalRoots serialized a local_roots key:\n%s", data)
	}
}
