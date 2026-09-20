package cmd

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ultrakorne/skillm/internal/skill"
	"github.com/ultrakorne/skillm/internal/source"
	"github.com/ultrakorne/skillm/internal/state"
)

// TestRegistryCollision verifies the per-skill collision decision the fetch
// pipeline makes for a chosen id: an unregistered id is fresh; an id already
// registered from the SAME source is fine (its content is re-fetched and
// re-installed); an id registered from a DIFFERENT source is a collision error.
func TestRegistryCollision(t *testing.T) {
	st := &state.State{}
	st.Upsert(state.SkillEntry{ID: "alpha", Kind: state.KindGit, Source: "https://example.com/x", Path: "alpha"})

	same := srcIdentity{kind: state.KindGit, source: "https://example.com/x", path: "alpha"}
	diffURL := srcIdentity{kind: state.KindGit, source: "https://example.com/OTHER", path: "alpha"}

	// Not registered → fresh, no error.
	if err := registryCollision(st, "absent", same); err != nil {
		t.Fatalf("absent id: err=%v, want nil", err)
	}
	// Registered from the same source → no error.
	if err := registryCollision(st, "alpha", same); err != nil {
		t.Fatalf("same source: err=%v, want nil", err)
	}
	// Registered from a different source → a collision error naming --as.
	err := registryCollision(st, "alpha", diffURL)
	if err == nil {
		t.Fatal("different source must error")
	}
}

// TestMergeEntry verifies that installing a fresh id records it as-is, while
// re-installing an already-registered id preserves its install markers
// (VendoredAt/Global) and original InstalledAt while refreshing the source and
// revision fields.
func TestMergeEntry(t *testing.T) {
	st := &state.State{}

	fresh := state.SkillEntry{ID: "new", Kind: state.KindGit, Source: "u", Path: "p", Ref: "main", Revision: "r1"}
	if got := mergeEntry(st, "new", fresh); !reflect.DeepEqual(got, fresh) {
		t.Fatalf("fresh id: mergeEntry = %+v, want %+v", got, fresh)
	}

	existing := state.SkillEntry{
		ID: "alpha", Kind: state.KindGit, Source: "u", Path: "p", Ref: "main", Revision: "old",
		VendoredAt: []string{"/proj"}, Global: true,
	}
	st.Upsert(existing)
	updated := state.SkillEntry{ID: "alpha", Kind: state.KindGit, Source: "u", Path: "p", Ref: "main", Revision: "new"}
	got := mergeEntry(st, "alpha", updated)
	if got.Revision != "new" {
		t.Errorf("revision not refreshed: %q", got.Revision)
	}
	if !got.Global || len(got.VendoredAt) != 1 || got.VendoredAt[0] != "/proj" {
		t.Errorf("install markers not preserved: %+v", got)
	}
}

// TestMergeEntryKeepsRecordedRemote pins that a same-source re-install typed
// another way does not repoint the recorded remote. The entry's spelling is a
// deliberate choice — an HTTPS remote so a keyless CI can update — and
// update/list clone from it, so a local `install git@...` must not flip it to
// SSH. Lenient matching (one repo, many spellings) is what makes this reachable.
func TestMergeEntryKeepsRecordedRemote(t *testing.T) {
	st := &state.State{}
	st.Upsert(state.SkillEntry{
		ID: "alpha", Kind: state.KindGit, Source: "https://github.com/o/r",
		Path: "p", Ref: "main", Revision: "old",
	})
	fresh := state.SkillEntry{
		ID: "alpha", Kind: state.KindGit, Source: "git@github.com:o/r.git",
		Path: "p", Ref: "main", Revision: "new",
	}
	got := mergeEntry(st, "alpha", fresh)
	if got.Source != "https://github.com/o/r" {
		t.Errorf("recorded remote repointed to %q, want the HTTPS spelling on record", got.Source)
	}
	if got.Revision != "new" {
		t.Errorf("revision not refreshed: %q", got.Revision)
	}
}

// TestMergeEntryLocalSourceFollowsFetch pins that the preservation above is
// scoped to git remotes: a local skill's Source is a directory, matched by
// absolute path (see sameLocalPath), and re-installing records the path just
// used.
func TestMergeEntryLocalSourceFollowsFetch(t *testing.T) {
	st := &state.State{}
	st.Upsert(state.SkillEntry{ID: "alpha", Kind: state.KindLocal, Source: "/abs/foo", Revision: "old"})
	fresh := state.SkillEntry{ID: "alpha", Kind: state.KindLocal, Source: "/abs/foo/", Revision: "new"}
	if got := mergeEntry(st, "alpha", fresh); got.Source != "/abs/foo/" {
		t.Errorf("local source = %q, want the path just fetched", got.Source)
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

func TestSelectFound(t *testing.T) {
	mk := func(id string) source.Found {
		return source.Found{Id: id, Dir: "/tmp/" + id, Skill: &skill.Skill{ID: id, Name: id}}
	}
	multi := []source.Found{mk("alpha"), mk("beta"), mk("gamma")}
	single := []source.Found{mk("solo")}

	cases := []struct {
		name       string
		found      []source.Found
		selectArgs []string
		all        bool
		wantIDs    []string
		wantErr    bool
	}{
		{"single auto-selects without prompt", single, nil, false, []string{"solo"}, false},
		{"explicit id selects that one", multi, []string{"beta"}, false, []string{"beta"}, false},
		{"multiple ids select those, in discovery order", multi, []string{"gamma", "alpha"}, false, []string{"alpha", "gamma"}, false},
		{"--all selects everything", multi, nil, true, []string{"alpha", "beta", "gamma"}, false},
		{"unknown id errors", multi, []string{"nope"}, false, nil, true},
		{"one unknown among known errors (atomic)", multi, []string{"alpha", "nope"}, false, nil, true},
		{"single with matching id", single, []string{"solo"}, false, []string{"solo"}, false},
		{"single with mismatched id errors", single, []string{"other"}, false, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := selectFound(tc.found, tc.selectArgs, tc.all)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %v", ids(got))
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			gotIDs := ids(got)
			if len(gotIDs) != len(tc.wantIDs) {
				t.Fatalf("ids = %v, want %v", gotIDs, tc.wantIDs)
			}
			for i := range gotIDs {
				if gotIDs[i] != tc.wantIDs[i] {
					t.Fatalf("ids = %v, want %v", gotIDs, tc.wantIDs)
				}
			}
		})
	}
}

// TestSrcIdentityMatchesRemoteSpellings pins the rule that one repo typed
// several ways is still one source. A spelling difference used to read as a
// different Source and refuse the install as a collision, whose suggested
// `--as` would then install a duplicate of the same repo under a second id.
// The recorded entry deliberately carries a trailing slash: entries written
// before canonicalRemote hold the URL exactly as typed, so both sides normalize.
func TestSrcIdentityMatchesRemoteSpellings(t *testing.T) {
	e := state.SkillEntry{Kind: state.KindGit, Source: "https://github.com/o/r/", Path: "p"}
	spellings := []string{
		"https://github.com/o/r/",    // exactly as recorded
		"https://github.com/o/r",     // the address-bar URL
		"https://github.com/o/r.git", // the clone-button URL
		"git@github.com:o/r.git",     // the scp-like form
	}
	for _, s := range spellings {
		if !(srcIdentity{kind: state.KindGit, source: s, path: "p"}).matches(e) {
			t.Errorf("%q is the same repo and should match", s)
		}
	}
	if (srcIdentity{kind: state.KindGit, source: "https://github.com/o/other", path: "p"}).matches(e) {
		t.Error("a genuinely different repo must not match")
	}
}

// TestSrcIdentityMatchesRepoPathCase pins where case-folding stops. The host is
// always folded (DNS is case-insensitive). The repo path is folded only on hosts
// that treat it that way: on a self-managed GitLab/Gitea/git-over-ssh host two
// paths differing in case are two repos, and folding them would bypass the --as
// guard and let one skill's content replace the other's.
func TestSrcIdentityMatchesRepoPathCase(t *testing.T) {
	selfManaged := state.SkillEntry{Kind: state.KindGit, Source: "https://git.example.com/team/widget", Path: "p"}
	if (srcIdentity{kind: state.KindGit, source: "https://git.example.com/team/Widget", path: "p"}).matches(selfManaged) {
		t.Error("path case is significant here: /team/Widget is a different repo from /team/widget")
	}
	if !(srcIdentity{kind: state.KindGit, source: "https://GIT.EXAMPLE.COM/team/widget", path: "p"}).matches(selfManaged) {
		t.Error("the host is case-insensitive and should match")
	}

	// GitHub paths are case-insensitive, so a case variant is the same repo —
	// erroring here would be the spurious collision this all set out to fix.
	gh := state.SkillEntry{Kind: state.KindGit, Source: "https://github.com/owner/repo", Path: "p"}
	if !(srcIdentity{kind: state.KindGit, source: "https://github.com/Owner/Repo.git", path: "p"}).matches(gh) {
		t.Error("github.com/Owner/Repo is the same repo as github.com/owner/repo")
	}
}

// TestCanonicalRemote checks the form a git remote is recorded in: trailing
// slashes go, since they are not part of a repo's identity, while the scheme and
// any ".git" suffix stay because the value is handed back to git.
func TestCanonicalRemote(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"https://github.com/o/r", "https://github.com/o/r"},
		{"https://github.com/o/r/", "https://github.com/o/r"},
		{"https://github.com/o/r///", "https://github.com/o/r"},
		{"https://github.com/o/r.git/", "https://github.com/o/r.git"},
	}
	for _, tc := range cases {
		if got := canonicalRemote(tc.in); got != tc.want {
			t.Errorf("canonicalRemote(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestRepoRelSubpath(t *testing.T) {
	cases := []struct {
		repo string
		dir  string
		want string
	}{
		{"/tmp/repo", "/tmp/repo", ""},
		{"/tmp/repo", "/tmp/repo/skills/foo", "skills/foo"},
		{"/tmp/repo", "/tmp/repo/foo", "foo"},
	}
	for _, tc := range cases {
		if got := repoRelSubpath(tc.repo, tc.dir); got != tc.want {
			t.Errorf("repoRelSubpath(%q,%q) = %q, want %q", tc.repo, tc.dir, got, tc.want)
		}
	}
}

func ids(found []source.Found) []string {
	out := make([]string, 0, len(found))
	for _, f := range found {
		out = append(out, f.Id)
	}
	return out
}
