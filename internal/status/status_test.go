package status

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)

func checked(at, next time.Time, current string) *File {
	f := New()
	f.CheckedAt, f.NextDueAt = at, next
	f.Self = &Self{Current: current, Method: "binary"}
	return f
}

// TestDue walks the scheduled-refresh decision with an injected clock.
func TestDue(t *testing.T) {
	day := 24 * time.Hour
	cases := []struct {
		name     string
		f        *File
		now      time.Time
		enabled  bool
		interval time.Duration
		current  string
		want     bool
	}{
		{"never refreshed", nil, t0, true, day, "0.4.0", true},
		{"never refreshed but disabled", nil, t0, false, day, "0.4.0", false},
		{"empty cache", New(), t0, true, day, "0.4.0", true},
		{"just checked", checked(t0, t0.Add(day), "0.4.0"), t0.Add(time.Minute), true, day, "0.4.0", false},
		{"one second before due", checked(t0, t0.Add(day), "0.4.0"), t0.Add(day - time.Second), true, day, "0.4.0", false},
		{"exactly due", checked(t0, t0.Add(day), "0.4.0"), t0.Add(day), true, day, "0.4.0", true},
		{"past due", checked(t0, t0.Add(day), "0.4.0"), t0.Add(3 * day), true, day, "0.4.0", true},
		{"past due but disabled", checked(t0, t0.Add(day), "0.4.0"), t0.Add(3 * day), false, day, "0.4.0", false},
		{"retry after a failed check", checked(t0, t0.Add(RetryAfterFailure), "0.4.0"), t0.Add(RetryAfterFailure), true, day, "0.4.0", true},
		{"interval shortened since", checked(t0, t0.Add(day), "0.4.0"), t0.Add(2 * time.Hour), true, time.Hour, "0.4.0", true},
		{"interval lengthened since", checked(t0, t0.Add(time.Hour), "0.4.0"), t0.Add(2 * time.Hour), true, day, "0.4.0", true},
		{"clock went back", checked(t0, t0.Add(day), "0.4.0"), t0.Add(-time.Hour), true, day, "0.4.0", true},
		{"skillm replaced since", checked(t0, t0.Add(day), "0.4.0"), t0.Add(time.Minute), true, day, "0.5.0", true},
		{"no next due recorded", checked(t0, time.Time{}, "0.4.0"), t0.Add(time.Hour), true, day, "0.4.0", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Due(c.f, c.now, c.enabled, c.interval, c.current); got != c.want {
				t.Fatalf("Due = %v, want %v", got, c.want)
			}
		})
	}
}

func TestDueAt(t *testing.T) {
	if got := DueAt(nil, time.Hour); !got.IsZero() {
		t.Fatalf("never checked: DueAt = %v, want zero", got)
	}
	f := checked(t0, t0.Add(24*time.Hour), "0.4.0")
	if got := DueAt(f, 24*time.Hour); !got.Equal(t0.Add(24 * time.Hour)) {
		t.Fatalf("DueAt = %v", got)
	}
	if got := DueAt(f, 2*time.Hour); !got.Equal(t0.Add(2 * time.Hour)) {
		t.Fatalf("shortened interval: DueAt = %v", got)
	}
}

func TestStale(t *testing.T) {
	f := checked(t0, t0.Add(24*time.Hour), "0.4.0")
	for _, c := range []struct {
		name string
		f    *File
		now  time.Time
		want bool
	}{
		{"never refreshed", nil, t0, true},
		{"fresh", f, t0.Add(23 * time.Hour), false},
		{"one interval old", f, t0.Add(24 * time.Hour), true},
		{"dated in the future", f, t0.Add(-time.Minute), true},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := Stale(c.f, c.now, 24*time.Hour); got != c.want {
				t.Fatalf("Stale = %v, want %v", got, c.want)
			}
		})
	}
}

// TestRecountBadge: the badge is on for a skill update or a newer skillm,
// never for an error, and errors list every failed lookup.
func TestRecountBadge(t *testing.T) {
	f := New()
	f.Skills = []Skill{
		{ID: "a", Status: SkillUpToDate},
		{ID: "b", Status: SkillError, Error: "offline"},
		{ID: "c", Status: SkillUntracked, Error: "gone"},
		{ID: "d", Status: SkillLocal},
	}
	f.Self = &Self{Current: "0.4.0", Method: "binary", Error: "no network"}
	f.Recount()
	if f.Badge || f.Updates != 0 {
		t.Fatalf("errors alone must not badge: %+v", f)
	}
	if len(f.Errors) != 3 || f.Errors[0].SkillID != "b" || f.Errors[1].Code != SkillUntracked ||
		f.Errors[2].Code != CodeSelfCheck || f.Errors[2].Message != "no network" {
		t.Fatalf("errors = %+v", f.Errors)
	}

	f.Skills = append(f.Skills, Skill{ID: "e", Status: SkillUpdateAvailable})
	f.Recount()
	if !f.Badge || f.Updates != 1 {
		t.Fatalf("a skill update must badge: %+v", f)
	}

	f.Skills = f.Skills[:1]
	f.Self = &Self{Current: "0.4.0", Latest: "0.5.0", Available: true, Method: "bundled"}
	f.Recount()
	if !f.Badge || f.Updates != 0 || len(f.Errors) != 0 {
		t.Fatalf("a newer skillm must badge: %+v", f)
	}
}

func TestLoadSave(t *testing.T) {
	home := t.TempDir()
	if f, err := Load(home); f != nil || err != nil {
		t.Fatalf("missing cache: Load = %v, %v; want nil, nil", f, err)
	}

	f := New()
	f.CheckedAt = time.Date(2026, 9, 23, 12, 0, 0, 999, time.FixedZone("CEST", 2*60*60))
	f.NextDueAt = f.CheckedAt.Add(24 * time.Hour)
	f.Skills = []Skill{{ID: "a", Status: SkillUpdateAvailable, InstalledRev: "1", UpstreamRev: "2"}}
	if err := Save(home, f); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(home, FileName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"checked_at": "2026-09-23T10:00:00Z"`) || !strings.HasSuffix(string(b), "}\n") {
		t.Fatalf("saved times must be UTC whole seconds:\n%s", b)
	}
	got, err := Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if !got.CheckedAt.Equal(f.CheckedAt) || got.Updates != 1 || !got.Badge || len(got.Skills) != 1 {
		t.Fatalf("round trip = %+v", got)
	}

	// A hand-edited count is recomputed on load.
	edited := strings.Replace(string(b), `"badge": true`, `"badge": false`, 1)
	if err := os.WriteFile(filepath.Join(home, FileName), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := Load(home); err != nil || !got.Badge {
		t.Fatalf("badge not recomputed on load: %+v, %v", got, err)
	}

	for name, body := range map[string]string{
		"corrupt":        "{not json",
		"unknown schema": `{"schema_version": 2}`,
	} {
		if err := os.WriteFile(filepath.Join(home, FileName), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if f, err := Load(home); err == nil {
			t.Errorf("%s cache: Load = %+v, want an error", name, f)
		}
	}
}
