package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ultrakorne/skillm/internal/state"
	"github.com/ultrakorne/skillm/internal/store"
)

// TestCollisionCheck verifies the per-skill add/reuse/collision decision the
// shared pipeline makes for a chosen id, in both add mode (any in-Home id is a
// hard error) and install source mode (same Source reuses, different Source
// errors).
func TestCollisionCheck(t *testing.T) {
	home := t.TempDir()
	const id = "alpha"
	if err := os.MkdirAll(store.SkillDir(home, id), 0o755); err != nil {
		t.Fatalf("mkdir skill: %v", err)
	}
	st := &state.State{}
	st.Upsert(state.SkillEntry{ID: id, Kind: state.KindGit, Source: "https://example.com/x", Path: "alpha"})

	same := srcIdentity{kind: state.KindGit, source: "https://example.com/x", path: "alpha"}
	diffURL := srcIdentity{kind: state.KindGit, source: "https://example.com/OTHER", path: "alpha"}

	// Not in Home → fresh add, regardless of mode.
	if reuse, err := collisionCheck(st, home, "absent", same, true); err != nil || reuse {
		t.Fatalf("absent id: reuse=%v err=%v, want false,nil", reuse, err)
	}
	if reuse, err := collisionCheck(st, home, "absent", same, false); err != nil || reuse {
		t.Fatalf("absent id (add mode): reuse=%v err=%v, want false,nil", reuse, err)
	}

	// In Home, add mode → a hard collision error.
	if _, err := collisionCheck(st, home, id, same, false); err == nil {
		t.Fatal("add-mode collision must error")
	}

	// In Home from the same Source, install source mode → reuse.
	if reuse, err := collisionCheck(st, home, id, same, true); err != nil || !reuse {
		t.Fatalf("same source: reuse=%v err=%v, want true,nil", reuse, err)
	}

	// In Home from a different Source, install source mode → collision error.
	if reuse, err := collisionCheck(st, home, id, diffURL, true); err == nil || reuse {
		t.Fatalf("different source: reuse=%v err=%v, want false,error", reuse, err)
	}
}

// TestSrcIdentityMatches checks the Source-identity comparison used to decide
// same-vs-different Source: git compares URL and subpath; local compares the
// directory by absolute, cleaned path; a kind mismatch never matches.
func TestSrcIdentityMatches(t *testing.T) {
	gitE := state.SkillEntry{Kind: state.KindGit, Source: "u", Path: "p"}
	if !(srcIdentity{kind: state.KindGit, source: "u", path: "p"}).matches(gitE) {
		t.Error("git same url+path should match")
	}
	if (srcIdentity{kind: state.KindGit, source: "u", path: "q"}).matches(gitE) {
		t.Error("git different subpath must not match")
	}
	if (srcIdentity{kind: state.KindGit, source: "v", path: "p"}).matches(gitE) {
		t.Error("git different url must not match")
	}
	if (srcIdentity{kind: state.KindLocal, source: "u"}).matches(gitE) {
		t.Error("kind mismatch must not match")
	}

	dir := t.TempDir()
	localE := state.SkillEntry{Kind: state.KindLocal, Source: dir}
	if !(srcIdentity{kind: state.KindLocal, source: dir}).matches(localE) {
		t.Error("local same dir should match")
	}
	// A path that cleans to the same directory still matches.
	if !(srcIdentity{kind: state.KindLocal, source: filepath.Join(dir, ".")}).matches(localE) {
		t.Error("local cleaned-equal dir should match")
	}
	if (srcIdentity{kind: state.KindLocal, source: filepath.Join(dir, "elsewhere")}).matches(localE) {
		t.Error("local different dir must not match")
	}
}
