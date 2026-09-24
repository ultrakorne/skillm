package core

import (
	"path/filepath"
	"reflect"
	"testing"

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

	same := SrcIdentity{Kind: state.KindGit, Source: "https://example.com/x", Path: "alpha"}
	diffURL := SrcIdentity{Kind: state.KindGit, Source: "https://example.com/OTHER", Path: "alpha"}

	// Not registered → fresh, no error.
	if err := RegistryCollision(st, "absent", same); err != nil {
		t.Fatalf("absent id: err=%v, want nil", err)
	}
	// Registered from the same source → no error.
	if err := RegistryCollision(st, "alpha", same); err != nil {
		t.Fatalf("same source: err=%v, want nil", err)
	}
	// Registered from a different source → a collision error naming --as.
	err := RegistryCollision(st, "alpha", diffURL)
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
	if got := MergeEntry(st, "new", fresh); !reflect.DeepEqual(got, fresh) {
		t.Fatalf("fresh id: MergeEntry = %+v, want %+v", got, fresh)
	}

	existing := state.SkillEntry{
		ID: "alpha", Kind: state.KindGit, Source: "u", Path: "p", Ref: "main", Revision: "old",
		VendoredAt: []string{"/proj"}, Global: true,
	}
	st.Upsert(existing)
	updated := state.SkillEntry{ID: "alpha", Kind: state.KindGit, Source: "u", Path: "p", Ref: "main", Revision: "new"}
	got := MergeEntry(st, "alpha", updated)
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
	got := MergeEntry(st, "alpha", fresh)
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
	if got := MergeEntry(st, "alpha", fresh); got.Source != "/abs/foo/" {
		t.Errorf("local source = %q, want the path just fetched", got.Source)
	}
}

// TestSrcIdentityMatches checks the Source-identity comparison used to decide
// same-vs-different Source: git compares URL and subpath; local compares the
// directory by absolute, cleaned path; a kind mismatch never matches.
func TestSrcIdentityMatches(t *testing.T) {
	gitE := state.SkillEntry{Kind: state.KindGit, Source: "u", Path: "p"}
	if !(SrcIdentity{Kind: state.KindGit, Source: "u", Path: "p"}).Matches(gitE) {
		t.Error("git same url+path should match")
	}
	if (SrcIdentity{Kind: state.KindGit, Source: "u", Path: "q"}).Matches(gitE) {
		t.Error("git different subpath must not match")
	}
	if (SrcIdentity{Kind: state.KindGit, Source: "v", Path: "p"}).Matches(gitE) {
		t.Error("git different url must not match")
	}
	if (SrcIdentity{Kind: state.KindLocal, Source: "u"}).Matches(gitE) {
		t.Error("kind mismatch must not match")
	}

	dir := t.TempDir()
	localE := state.SkillEntry{Kind: state.KindLocal, Source: dir}
	if !(SrcIdentity{Kind: state.KindLocal, Source: dir}).Matches(localE) {
		t.Error("local same dir should match")
	}
	// A path that cleans to the same directory still matches.
	if !(SrcIdentity{Kind: state.KindLocal, Source: filepath.Join(dir, ".")}).Matches(localE) {
		t.Error("local cleaned-equal dir should match")
	}
	if (SrcIdentity{Kind: state.KindLocal, Source: filepath.Join(dir, "elsewhere")}).Matches(localE) {
		t.Error("local different dir must not match")
	}
}

// TestSrcIdentityMatchesRemoteSpellings pins the rule that one repo typed
// several ways is still one source. A spelling difference used to read as a
// different Source and refuse the install as a collision, whose suggested
// `--as` would then install a duplicate of the same repo under a second id.
// The recorded entry deliberately carries a trailing slash: entries written
// before CanonicalRemote hold the URL exactly as typed, so both sides normalize.
func TestSrcIdentityMatchesRemoteSpellings(t *testing.T) {
	e := state.SkillEntry{Kind: state.KindGit, Source: "https://github.com/o/r/", Path: "p"}
	spellings := []string{
		"https://github.com/o/r/",    // exactly as recorded
		"https://github.com/o/r",     // the address-bar URL
		"https://github.com/o/r.git", // the clone-button URL
		"git@github.com:o/r.git",     // the scp-like form
	}
	for _, s := range spellings {
		if !(SrcIdentity{Kind: state.KindGit, Source: s, Path: "p"}).Matches(e) {
			t.Errorf("%q is the same repo and should match", s)
		}
	}
	if (SrcIdentity{Kind: state.KindGit, Source: "https://github.com/o/other", Path: "p"}).Matches(e) {
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
	if (SrcIdentity{Kind: state.KindGit, Source: "https://git.example.com/team/Widget", Path: "p"}).Matches(selfManaged) {
		t.Error("path case is significant here: /team/Widget is a different repo from /team/widget")
	}
	if !(SrcIdentity{Kind: state.KindGit, Source: "https://GIT.EXAMPLE.COM/team/widget", Path: "p"}).Matches(selfManaged) {
		t.Error("the host is case-insensitive and should match")
	}

	// GitHub paths are case-insensitive, so a case variant is the same repo —
	// erroring here would be the spurious collision this all set out to fix.
	gh := state.SkillEntry{Kind: state.KindGit, Source: "https://github.com/owner/repo", Path: "p"}
	if !(SrcIdentity{Kind: state.KindGit, Source: "https://github.com/Owner/Repo.git", Path: "p"}).Matches(gh) {
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
		if got := CanonicalRemote(tc.in); got != tc.want {
			t.Errorf("CanonicalRemote(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestSourceSpellingsAreOneSource pins the seam between the two halves of the
// source-identity fix: shorthand resolution (source.GitRemote, which expands a
// GitHub "owner/repo") and remote normalization (CanonicalRemote on the way in,
// NormalizeRemote on compare). They were written apart and each is tested apart,
// but only together do they make every way of naming one repo reach one entry.
// Walk them in the order cmd's fetchToStage does, so a change to either side that
// breaks the pair fails here.
func TestSourceSpellingsAreOneSource(t *testing.T) {
	recorded := state.SkillEntry{Kind: state.KindGit, Source: "https://github.com/o/r", Path: "p"}
	spellings := []string{
		"o/r",                        // the shorthand `skills add` takes
		"o/r.git",                    // shorthand, clone-button suffix
		"https://github.com/o/r",     // the address-bar URL
		"https://github.com/o/r/",    // pasted with a trailing slash
		"https://github.com/o/r.git", // the clone-button URL
		"git@github.com:o/r.git",     // the scp-like form
		"https://github.com/O/R",     // GitHub folds path case
	}
	for _, s := range spellings {
		resolved := CanonicalRemote(source.GitRemote(s))
		if !(SrcIdentity{Kind: state.KindGit, Source: resolved, Path: "p"}).Matches(recorded) {
			t.Errorf("%q resolved to %q, which did not match the recorded source", s, resolved)
		}
	}
	other := CanonicalRemote(source.GitRemote("o/other"))
	if (SrcIdentity{Kind: state.KindGit, Source: other, Path: "p"}).Matches(recorded) {
		t.Error("a genuinely different repo must still be a different source")
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
		if got := RepoRelSubpath(tc.repo, tc.dir); got != tc.want {
			t.Errorf("RepoRelSubpath(%q,%q) = %q, want %q", tc.repo, tc.dir, got, tc.want)
		}
	}
}
