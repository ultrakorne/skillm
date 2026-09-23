package core

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ultrakorne/skillm/internal/agentdir"
	"github.com/ultrakorne/skillm/internal/config"
	"github.com/ultrakorne/skillm/internal/lockfile"
	"github.com/ultrakorne/skillm/internal/state"
	"github.com/ultrakorne/skillm/internal/store"
)

// localTestSetup builds a temp Home plus a source directory holding skill
// "demo"'s content, and returns the home, a fresh project base distinct from
// HOME (so each agent's local folder is real, not aliased to its global one),
// the source dir VendorOne copies from, and the default agents (claude+agents).
func localTestSetup(t *testing.T) (home, base, src string, agents []agentdir.Agent) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())

	home = t.TempDir()
	if err := store.EnsureHome(home); err != nil {
		t.Fatalf("EnsureHome: %v", err)
	}
	src = t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("demo body\n"), 0o644); err != nil {
		t.Fatalf("write src SKILL.md: %v", err)
	}
	return home, t.TempDir(), src, config.Default().AllAgents()
}

func demoSlot(base string) string   { return agentdir.CanonicalSkillDir(base, "demo") }
func claudeLink(base string) string { return filepath.Join(base, ".claude", "skills", "demo") }

// TestLocalInstallWritesCopyAndLinks: an absent slot gets the canonical copy,
// the canonical "agents" entry gets no link (the copy serves it), and claude
// gets a relative link resolving to the copy.
func TestLocalInstallWritesCopyAndLinks(t *testing.T) {
	home, base, src, agents := localTestSetup(t)

	action, err := VendorOne(nil, home, "demo", src, agents, agentdir.Local, base, false, false, false, "local")
	if err != nil {
		t.Fatalf("VendorOne: %v", err)
	}
	if action != VendorWrote {
		t.Fatalf("action = %v, want VendorWrote", action)
	}

	// The canonical slot is a real directory with the skill's content.
	b, err := os.ReadFile(filepath.Join(demoSlot(base), "SKILL.md"))
	if err != nil || string(b) != "demo body\n" {
		t.Fatalf("canonical copy content = %q err=%v", b, err)
	}
	fi, _ := os.Lstat(demoSlot(base))
	if fi == nil || !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("canonical slot should be a real dir")
	}

	// claude got a relative link resolving to the copy.
	raw, err := os.Readlink(claudeLink(base))
	if err != nil {
		t.Fatalf("claude link missing: %v", err)
	}
	if filepath.IsAbs(raw) {
		t.Fatalf("claude link is absolute (%q), want relative", raw)
	}
	resolved := filepath.Clean(filepath.Join(filepath.Dir(claudeLink(base)), raw))
	if resolved != filepath.Clean(demoSlot(base)) {
		t.Fatalf("claude link resolves to %s, want canonical copy", resolved)
	}

	// servedAgents sees both the canonical agent and the linked one.
	names := servedAgents(home, "demo", agents, agentdir.Local, base)
	if strings.Join(names, ",") != "agents,claude" && strings.Join(names, ",") != "claude,agents" {
		t.Fatalf("served agents = %v, want agents+claude", names)
	}
}

// TestLocalInstallConvertsLegacyHomeSymlink: a pre-refactor absolute symlink
// into Home at the canonical slot is converted to a real copy without force.
func TestLocalInstallConvertsLegacyHomeSymlink(t *testing.T) {
	home, base, src, agents := localTestSetup(t)

	if err := os.MkdirAll(filepath.Dir(demoSlot(base)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, "skills", "demo"), demoSlot(base)); err != nil {
		t.Fatal(err)
	}

	action, err := VendorOne(nil, home, "demo", src, agents, agentdir.Local, base, false, false, false, "local")
	if err != nil {
		t.Fatalf("VendorOne: %v", err)
	}
	if action != VendorConverted {
		t.Fatalf("action = %v, want VendorConverted", action)
	}
	fi, err := os.Lstat(demoSlot(base))
	if err != nil || fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
		t.Fatalf("converted slot should be a real dir; mode=%v err=%v", fi.Mode(), err)
	}
}

// TestLocalInstallForeignDirBlockedThenForced: an unrecorded foreign directory
// is a conflict — left untouched without force, adopted with it, refreshed
// when recorded.
func TestLocalInstallForeignDirBlockedThenForced(t *testing.T) {
	home, base, src, agents := localTestSetup(t)

	foreign := demoSlot(base)
	if err := os.MkdirAll(foreign, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(foreign, "MINE.txt"), []byte("hand\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if c := VendorConflict(home, "demo", agentdir.Local, base, false); c != foreign {
		t.Fatalf("VendorConflict = %q, want %q", c, foreign)
	}
	if c := VendorConflict(home, "demo", agentdir.Local, base, true); c != "" {
		t.Fatalf("recorded dir must not be a conflict, got %q", c)
	}

	// Not forced → blocked, nothing written, no links created.
	action, err := VendorOne(nil, home, "demo", src, agents, agentdir.Local, base, false, false, false, "local")
	if err != nil {
		t.Fatalf("VendorOne (no force): %v", err)
	}
	if action != VendorBlocked {
		t.Fatalf("action = %v, want VendorBlocked", action)
	}
	if _, err := os.Stat(filepath.Join(foreign, "MINE.txt")); err != nil {
		t.Fatalf("blocked foreign dir must survive: %v", err)
	}
	if _, err := os.Lstat(claudeLink(base)); !os.IsNotExist(err) {
		t.Fatalf("no link may be created for a blocked install; lstat err = %v", err)
	}

	// Recorded → skillm's own copy → refreshed (overwritten).
	action, err = VendorOne(nil, home, "demo", src, agents, agentdir.Local, base, true, false, false, "local")
	if err != nil {
		t.Fatalf("VendorOne (recorded): %v", err)
	}
	if action != VendorRefreshed {
		t.Fatalf("action = %v, want VendorRefreshed", action)
	}
	if _, err := os.Stat(filepath.Join(foreign, "MINE.txt")); !os.IsNotExist(err) {
		t.Fatalf("refresh should overwrite the dir; MINE.txt err = %v", err)
	}
}

// TestVendorOneForceLinksIsSeparateFromCanonicalForce: force (consent to
// overwrite a canonical-slot conflict, which may come from an interactive
// "yes" to a prompt listing only canonical-slot paths) must not by itself
// authorize deleting a foreign entry at an agent's link path — a separate
// path the user was never shown. Only forceLinks, driven solely by the
// explicit --force flag, does that.
func TestVendorOneForceLinksIsSeparateFromCanonicalForce(t *testing.T) {
	home, base, src, agents := localTestSetup(t)

	// A foreign file at claude's link path; the canonical slot is untouched.
	if err := os.MkdirAll(filepath.Dir(claudeLink(base)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claudeLink(base), []byte("hand-copied\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// force=true but forceLinks=false: the canonical copy is written, but
	// claude's foreign file must survive untouched.
	action, err := VendorOne(nil, home, "demo", src, agents, agentdir.Local, base, false, true, false, "local")
	if err != nil {
		t.Fatalf("VendorOne: %v", err)
	}
	if action != VendorWrote {
		t.Fatalf("action = %v, want VendorWrote", action)
	}
	b, err := os.ReadFile(claudeLink(base))
	if err != nil || string(b) != "hand-copied\n" {
		t.Fatalf("claude's foreign file must survive without forceLinks; content=%q err=%v", b, err)
	}

	// forceLinks=true takes it over.
	action, err = VendorOne(nil, home, "demo", src, agents, agentdir.Local, base, true, true, true, "local")
	if err != nil {
		t.Fatalf("VendorOne: %v", err)
	}
	if action != VendorRefreshed {
		t.Fatalf("action = %v, want VendorRefreshed", action)
	}
	fi, err := os.Lstat(claudeLink(base))
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("claude link must be taken over with forceLinks=true (err=%v)", err)
	}
}

// TestLocalRemove removes the agent links and the canonical copy, and is
// idempotent.
func TestLocalRemove(t *testing.T) {
	home, base, src, agents := localTestSetup(t)
	if _, err := VendorOne(nil, home, "demo", src, agents, agentdir.Local, base, false, false, false, "local"); err != nil {
		t.Fatalf("seed install: %v", err)
	}

	removed, err := VendorRemove(nil, home, "demo", agents, agentdir.Local, base, true, "local")
	if err != nil {
		t.Fatalf("VendorRemove: %v", err)
	}
	if !removed {
		t.Fatal("VendorRemove should report the copy removed")
	}
	if _, err := os.Lstat(demoSlot(base)); !os.IsNotExist(err) {
		t.Fatalf("canonical copy should be gone; err = %v", err)
	}
	if _, err := os.Lstat(claudeLink(base)); !os.IsNotExist(err) {
		t.Fatalf("claude link should be gone; err = %v", err)
	}
	// Idempotent.
	if again, _ := VendorRemove(nil, home, "demo", agents, agentdir.Local, base, true, "local"); again {
		t.Fatal("second VendorRemove removed something")
	}
}

// TestLockEntrySync: installing writes a vercel-compatible lock entry; a
// re-upsert preserves foreign keys; removal drops the entry and deletes an
// emptied lockfile.
func TestLockEntrySync(t *testing.T) {
	home, base, src, agents := localTestSetup(t)
	if _, err := VendorOne(nil, home, "demo", src, agents, agentdir.Local, base, false, false, false, "local"); err != nil {
		t.Fatalf("seed install: %v", err)
	}

	entry := state.SkillEntry{
		ID:       "demo",
		Kind:     state.KindGit,
		Source:   "https://github.com/owner/repo",
		Path:     "skills/demo",
		Ref:      "main",
		Revision: "abc",
	}
	if err := UpsertLockEntry(nil, entry, base); err != nil {
		t.Fatalf("UpsertLockEntry: %v", err)
	}

	lf, err := lockfile.Load(base)
	if err != nil {
		t.Fatal(err)
	}
	e := lf.Skills["demo"]
	if e == nil {
		t.Fatal("lock entry missing")
	}
	if e.Source != "owner/repo" || e.SourceType != "github" || e.Ref != "main" ||
		e.SkillPath != "skills/demo/SKILL.md" || len(e.ComputedHash) != 64 {
		t.Fatalf("lock entry mis-built: %+v", e)
	}

	// A foreign key on the entry survives a re-upsert.
	raw, _ := os.ReadFile(lockfile.Path(base))
	patched := strings.Replace(string(raw), "\"computedHash\"", "\"subagents\": [\"x\"],\n      \"computedHash\"", 1)
	if err := os.WriteFile(lockfile.Path(base), []byte(patched), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := UpsertLockEntry(nil, entry, base); err != nil {
		t.Fatalf("UpsertLockEntry: %v", err)
	}
	raw, _ = os.ReadFile(lockfile.Path(base))
	if !strings.Contains(string(raw), "subagents") {
		t.Fatalf("re-upsert dropped foreign key:\n%s", raw)
	}

	if err := RemoveLockEntry(nil, "demo", base); err != nil {
		t.Fatalf("RemoveLockEntry: %v", err)
	}
	if _, err := os.Stat(lockfile.Path(base)); !os.IsNotExist(err) {
		t.Fatalf("emptied lockfile should be deleted; err = %v", err)
	}
}

// eventsWith returns the recorded EventLogs carrying code.
func eventsWith(r *recorder, code string) []Event {
	var out []Event
	for _, ev := range r.events {
		if ev.Type == EventLog && ev.Code == code {
			out = append(out, ev)
		}
	}
	return out
}

// TestVendorOneReportsLinks: the per-link lines the CLI used to print are now
// EventLogs with a stable code, the skill id and the unchanged sentence.
func TestVendorOneReportsLinks(t *testing.T) {
	home, base, src, agents := localTestSetup(t)
	rep := &recorder{}
	if _, err := VendorOne(rep, home, "demo", src, agents, agentdir.Local, base, false, false, false, "local"); err != nil {
		t.Fatalf("VendorOne: %v", err)
	}
	linked := eventsWith(rep, CodeLinked)
	if len(linked) != 1 {
		t.Fatalf("linked events = %+v, want one (claude)", rep.events)
	}
	ev := linked[0]
	if ev.Level != LevelSuccess || ev.Skill != "demo" || ev.Text != "linked demo for claude (local)" {
		t.Fatalf("linked event = %+v", ev)
	}

	// A foreign file at claude's link path is refused with a pointer to --force.
	if _, err := VendorRemove(nil, home, "demo", agents, agentdir.Local, base, true, "local"); err != nil {
		t.Fatalf("VendorRemove: %v", err)
	}
	if err := os.WriteFile(claudeLink(base), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep = &recorder{}
	if _, err := VendorOne(rep, home, "demo", src, agents, agentdir.Local, base, false, false, false, "local"); err != nil {
		t.Fatalf("VendorOne: %v", err)
	}
	refused := eventsWith(rep, CodeLinkRefused)
	if len(refused) != 1 || refused[0].Level != LevelWarn || !strings.HasSuffix(refused[0].Text, "(pass --force to take it over)") {
		t.Fatalf("refused events = %+v", rep.events)
	}
}

// TestLinkAndUnlinkFailuresAreNotRefusals: link_refused / unlink_refused are
// only for an entry skillm does not own (which --force could take over); an
// I/O failure — here, claude's skill folder cannot exist because .claude is a
// file — is link_failed / unlink_failed, with the linker's text unchanged.
func TestLinkAndUnlinkFailuresAreNotRefusals(t *testing.T) {
	home, base, src, agents := localTestSetup(t)
	if err := os.WriteFile(filepath.Join(base, ".claude"), []byte("not a dir\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rep := &recorder{}
	if _, err := VendorOne(rep, home, "demo", src, agents, agentdir.Local, base, false, false, false, "local"); err != nil {
		t.Fatalf("VendorOne: %v", err)
	}
	if refused := eventsWith(rep, CodeLinkRefused); len(refused) != 0 {
		t.Fatalf("an I/O failure was reported as a refusal: %+v", refused)
	}
	failed := eventsWith(rep, CodeLinkFailed)
	if len(failed) != 1 || failed[0].Level != LevelWarn || failed[0].Skill != "demo" ||
		!strings.Contains(failed[0].Text, claudeLink(base)) || strings.Contains(failed[0].Text, "--force") {
		t.Fatalf("link_failed events = %+v", rep.events)
	}

	if runtime.GOOS == "windows" {
		// Windows reports a path under a file as not found, which Unlink
		// treats as an absent link rather than a failure.
		return
	}
	rep = &recorder{}
	if _, err := VendorRemove(rep, home, "demo", agents, agentdir.Local, base, true, "local"); err != nil {
		t.Fatalf("VendorRemove: %v", err)
	}
	if refused := eventsWith(rep, CodeUnlinkRefused); len(refused) != 0 {
		t.Fatalf("an I/O failure was reported as a refusal: %+v", refused)
	}
	if failed := eventsWith(rep, CodeUnlinkFailed); len(failed) != 1 || !strings.Contains(failed[0].Text, claudeLink(base)) {
		t.Fatalf("unlink_failed events = %+v", rep.events)
	}
}

// TestUnlinkRefusalIsReported: a file skillm did not create at an agent's
// link path is left alone and reported as unlink_refused with the linker's
// sentence.
func TestUnlinkRefusalIsReported(t *testing.T) {
	home, base, src, agents := localTestSetup(t)
	if _, err := VendorOne(nil, home, "demo", src, agents, agentdir.Local, base, false, false, false, "local"); err != nil {
		t.Fatalf("VendorOne: %v", err)
	}
	link := claudeLink(base)
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(link, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rep := &recorder{}
	if _, err := VendorRemove(rep, home, "demo", agents, agentdir.Local, base, true, "local"); err != nil {
		t.Fatalf("VendorRemove: %v", err)
	}
	refused := eventsWith(rep, CodeUnlinkRefused)
	want := "refusing to remove " + link + ": it is a file, not a skillm-managed link"
	if len(refused) != 1 || refused[0].Level != LevelWarn || refused[0].Text != want {
		t.Fatalf("unlink_refused events = %+v, want one with %q", rep.events, want)
	}
	if failed := eventsWith(rep, CodeUnlinkFailed); len(failed) != 0 {
		t.Fatalf("a refusal was reported as a failure: %+v", failed)
	}
	if _, err := os.Stat(link); err != nil {
		t.Fatalf("user file wrongly removed: %v", err)
	}
}

// TestLockEntryFailuresAreReturned: a lockfile with a newer schema is left
// alone, and the failure is both reported (as before) and returned.
func TestLockEntryFailuresAreReturned(t *testing.T) {
	home, base, src, agents := localTestSetup(t)
	if _, err := VendorOne(nil, home, "demo", src, agents, agentdir.Local, base, false, false, false, "local"); err != nil {
		t.Fatalf("seed install: %v", err)
	}
	newer := "{\n  \"version\": 99,\n  \"skills\": {\n    \"demo\": {\"source\": \"x\", \"sourceType\": \"local\", \"computedHash\": \"h\"}\n  }\n}\n"
	if err := os.WriteFile(lockfile.Path(base), []byte(newer), 0o644); err != nil {
		t.Fatal(err)
	}
	entry := state.SkillEntry{ID: "demo", Kind: state.KindLocal, Source: src}

	rep := &recorder{}
	err := UpsertLockEntry(rep, entry, base)
	if err == nil || !strings.Contains(err.Error(), "newer schema") {
		t.Fatalf("UpsertLockEntry err = %v, want the newer-schema failure", err)
	}
	if got := eventsWith(rep, CodeLockfileFailed); len(got) != 1 || got[0].Text != err.Error() || got[0].Level != LevelWarn {
		t.Fatalf("events = %+v, want one warning carrying the error", rep.events)
	}

	rep = &recorder{}
	err = RemoveLockEntry(rep, "demo", base)
	if err == nil || !strings.Contains(err.Error(), "not removed") {
		t.Fatalf("RemoveLockEntry err = %v, want the newer-schema failure", err)
	}
	if got := eventsWith(rep, CodeLockfileFailed); len(got) != 1 {
		t.Fatalf("events = %+v, want one warning", rep.events)
	}
	if b, _ := os.ReadFile(lockfile.Path(base)); string(b) != newer {
		t.Fatalf("newer-schema lockfile was rewritten:\n%s", b)
	}
}

// TestRefreshCopy covers when a copy is rewritten and that a failed write is
// reported and returned.
func TestRefreshCopy(t *testing.T) {
	_, base, src, _ := localTestSetup(t)
	target := demoSlot(base)

	// Nothing to refresh from.
	if ok, err := RefreshCopy(nil, "", "demo", target, "p", true, true); ok || err != nil {
		t.Fatalf("empty src: ok=%v err=%v", ok, err)
	}

	// Drift (the target is missing) → synced.
	rep := &recorder{}
	ok, err := RefreshCopy(rep, src, "demo", target, "p", false, false)
	if !ok || err != nil {
		t.Fatalf("drift: ok=%v err=%v", ok, err)
	}
	if got := eventsWith(rep, CodeCopySynced); len(got) != 1 || got[0].Text != "synced copy of demo (p)" {
		t.Fatalf("events = %+v", rep.events)
	}

	// Equal content and not updated → untouched.
	if ok, err := RefreshCopy(nil, src, "demo", target, "p", false, false); ok || err != nil {
		t.Fatalf("equal: ok=%v err=%v", ok, err)
	}

	// A just-updated git skill is always rewritten.
	rep = &recorder{}
	if ok, err := RefreshCopy(rep, src, "demo", target, "p", true, true); !ok || err != nil {
		t.Fatalf("updated: ok=%v err=%v", ok, err)
	}
	if got := eventsWith(rep, CodeCopyRefreshed); len(got) != 1 || got[0].Text != "refreshed copy of demo (p)" {
		t.Fatalf("events = %+v", rep.events)
	}

	// A failed write is reported and returned.
	rep = &recorder{}
	ok, err = RefreshCopy(rep, filepath.Join(src, "missing"), "demo", target, "p", true, true)
	if ok || err == nil || !strings.HasPrefix(err.Error(), "refresh copy "+target+": ") {
		t.Fatalf("failed write: ok=%v err=%v", ok, err)
	}
	if got := eventsWith(rep, CodeCopyFailed); len(got) != 1 || got[0].Text != err.Error() {
		t.Fatalf("events = %+v", rep.events)
	}
}
