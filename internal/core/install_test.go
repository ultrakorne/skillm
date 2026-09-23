package core

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ultrakorne/skillm/internal/agentdir"
	"github.com/ultrakorne/skillm/internal/state"
)

// writeSkill writes a SKILL.md with frontmatter for skill id under dir/id and
// returns that directory.
func writeSkill(t *testing.T, dir, id, body string) string {
	t.Helper()
	sd := filepath.Join(dir, id)
	if err := os.MkdirAll(sd, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + id + "\ndescription: " + id + " skill\n---\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(sd, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return sd
}

// installSetup is localTestSetup plus a local Source holding skill "demo",
// inspected with the project base as the working directory.
func installSetup(t *testing.T) (opts Options, base string, insp *Inspection) {
	t.Helper()
	home, base, _, _ := localTestSetup(t)
	srcRoot := t.TempDir()
	writeSkill(t, srcRoot, "demo", "demo body")
	opts = Options{Home: home, Cwd: base}
	insp, err := Inspect(context.Background(), opts, filepath.Join(srcRoot, "demo"), "")
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	t.Cleanup(func() { insp.Close() })
	return opts, base, insp
}

// TestInstallYesDoesNotTakeOverAgentLinks: Yes only answers the canonical-slot
// question, so an install with Yes (but not Force) must leave a hand-made
// skill at an agent's link path alone; only Force takes it over.
func TestInstallYesDoesNotTakeOverAgentLinks(t *testing.T) {
	opts, base, insp := installSetup(t)
	if err := os.MkdirAll(claudeLink(base), 0o755); err != nil {
		t.Fatal(err)
	}
	notes := filepath.Join(claudeLink(base), "NOTES.md")
	if err := os.WriteFile(notes, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	req := InstallRequest{Inspection: insp, IDs: []string{"demo"}, Scope: agentdir.Local, Base: base}

	yes := opts
	yes.Yes = true
	if _, err := InstallSkills(context.Background(), yes, nil, req); err != nil {
		t.Fatalf("InstallSkills with Yes: %v", err)
	}
	if _, err := os.Stat(notes); err != nil {
		t.Fatalf("Yes must not take over the agent link path: %v", err)
	}

	force := opts
	force.Force = true
	if _, err := InstallSkills(context.Background(), force, nil, req); err != nil {
		t.Fatalf("InstallSkills with Force: %v", err)
	}
	if fi, err := os.Lstat(claudeLink(base)); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("Force must take over the agent link path (err=%v)", err)
	}
}

// TestInstallForeignFiles: a foreign directory at the canonical slot stops the
// install with a *ForeignFilesError before anything is written; SkipForeign
// skips that skill with an install_blocked event; Yes overwrites it.
func TestInstallForeignFiles(t *testing.T) {
	opts, base, insp := installSetup(t)
	foreign := filepath.Join(demoSlot(base), "MINE.md")
	if err := os.MkdirAll(demoSlot(base), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(foreign, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	req := InstallRequest{Inspection: insp, IDs: []string{"demo"}, Scope: agentdir.Local, Base: base}

	_, err := InstallSkills(context.Background(), opts, nil, req)
	var ff *ForeignFilesError
	if !errors.As(err, &ff) || len(ff.Paths) != 1 || ff.Paths[0] != demoSlot(base) {
		t.Fatalf("err = %v, want a ForeignFilesError naming %s", err, demoSlot(base))
	}
	if _, err := os.Lstat(claudeLink(base)); !os.IsNotExist(err) {
		t.Fatalf("nothing may be written before the question is answered (claude link: %v)", err)
	}
	if _, err := os.Stat(filepath.Join(opts.Home, "state.toml")); !os.IsNotExist(err) {
		t.Fatalf("the registry must not be written: %v", err)
	}

	skip := req
	skip.SkipForeign = true
	rep := &recorder{}
	res, err := InstallSkills(context.Background(), opts, rep, skip)
	if err != nil {
		t.Fatalf("SkipForeign: %v", err)
	}
	if res.InstalledAny() || len(eventsWith(rep, CodeInstallBlocked)) != 1 {
		t.Fatalf("SkipForeign must skip demo with install_blocked: res=%+v events=%+v", res, rep.events)
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatalf("a skipped skill's foreign files must stay: %v", err)
	}

	yes := opts
	yes.Yes = true
	res, err = InstallSkills(context.Background(), yes, nil, req)
	if err != nil || len(res.Skills) != 1 || res.Skills[0].Action != VendorAdopted {
		t.Fatalf("Yes: res=%+v err=%v, want demo adopted", res, err)
	}
}

// TestInspectRelativeLocalSource: a relative local Source is resolved against
// Options.Cwd, never the process's working directory, and the install records
// the absolute path.
func TestInspectRelativeLocalSource(t *testing.T) {
	home, base, _, _ := localTestSetup(t)
	project := t.TempDir()
	writeSkill(t, filepath.Join(project, "skills"), "rel", "rel body")
	opts := Options{Home: home, Cwd: project}

	insp, err := Inspect(context.Background(), opts, "./skills/rel", "")
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	defer insp.Close()
	want := filepath.Join(project, "skills", "rel")
	if insp.Kind != state.KindLocal || insp.Source != want || len(insp.Skills) != 1 || insp.Skills[0].Path != want {
		t.Fatalf("inspection = %+v, want local source %s", insp, want)
	}
	if insp.Skills[0].Name != "rel" || insp.Skills[0].Description != "rel skill" {
		t.Fatalf("skill metadata = %+v", insp.Skills[0])
	}

	if _, err := InstallSkills(context.Background(), opts, nil, InstallRequest{Inspection: insp, IDs: []string{"rel"}, Scope: agentdir.Local, Base: base}); err != nil {
		t.Fatalf("InstallSkills: %v", err)
	}
	st, err := state.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	if e, ok := st.Get("rel"); !ok || e.Source != want {
		t.Fatalf("recorded source = %q, want %q", e.Source, want)
	}

	if _, err := Inspect(context.Background(), Options{Home: home}, "./skills/rel", ""); err == nil {
		t.Fatal("a relative local source with no Cwd must be refused")
	}
}

// TestValidateSourceSelection: the selection errors are typed, so the caller
// words them (the CLI names --as).
func TestValidateSourceSelection(t *testing.T) {
	home, _, _, _ := localTestSetup(t)
	opts := Options{Home: home, Cwd: t.TempDir()}
	catalog := t.TempDir()
	writeSkill(t, catalog, "alpha", "a")
	writeSkill(t, catalog, "beta", "b")
	insp, err := Inspect(context.Background(), opts, catalog, "")
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	defer insp.Close()

	if err := ValidateSourceSelection(opts, insp, []string{"alpha", "beta"}, "renamed"); !errors.Is(err, ErrAsMultiple) {
		t.Fatalf("As on two skills: err = %v, want ErrAsMultiple", err)
	}
	if err := ValidateSourceSelection(opts, insp, []string{"nope"}, ""); err == nil || !strings.Contains(err.Error(), "not found in source: nope") {
		t.Fatalf("unknown id: err = %v", err)
	}

	st := &state.State{}
	st.Upsert(state.SkillEntry{ID: "alpha", Kind: state.KindLocal, Source: "/elsewhere/alpha"})
	if err := state.Save(home, st); err != nil {
		t.Fatal(err)
	}
	var collision *SourceCollisionError
	if err := ValidateSourceSelection(opts, insp, []string{"alpha"}, ""); !errors.As(err, &collision) || collision.ID != "alpha" {
		t.Fatalf("different source: err = %v, want a SourceCollisionError for alpha", err)
	}
	if strings.Contains(collision.Error(), "--as") {
		t.Fatalf("core's collision text must not name a CLI flag: %q", collision.Error())
	}
	if err := ValidateSourceSelection(opts, insp, []string{"alpha"}, "alpha2"); err != nil {
		t.Fatalf("As resolves the collision: %v", err)
	}
}

// TestInstallUsesInspectedCommit: an install from an Inspection copies the
// commit that was inspected, even when the branch has moved on since.
func TestInstallUsesInspectedCommit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	home, _, _, _ := localTestSetup(t)
	repo := t.TempDir()
	writeSkill(t, repo, "alpha", "alpha v1")
	git := func(args ...string) string {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = repo
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main")
	git("config", "user.email", "test@example.com")
	git("config", "user.name", "test")
	git("add", "-A")
	git("commit", "-q", "-m", "v1")
	v1 := git("rev-parse", "HEAD")

	opts := Options{Home: home, Cwd: t.TempDir()}
	insp, err := Inspect(context.Background(), opts, "file://"+filepath.ToSlash(repo), "")
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	defer insp.Close()
	if insp.Kind != state.KindGit || insp.Ref != "main" || insp.Commit != v1 {
		t.Fatalf("inspection = %+v, want git main@%s", insp, v1)
	}
	if len(insp.Skills) != 1 || insp.Skills[0].ID != "alpha" || insp.Skills[0].Path != "alpha" {
		t.Fatalf("skills = %+v", insp.Skills)
	}

	// Move the branch on after inspecting.
	writeSkill(t, repo, "alpha", "alpha v2")
	git("commit", "-q", "-am", "v2")

	if _, err := InstallSkills(context.Background(), opts, nil, InstallRequest{Inspection: insp, IDs: []string{"alpha"}, Scope: agentdir.Global}); err != nil {
		t.Fatalf("InstallSkills: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(agentdir.CanonicalSkillDirAt(agentdir.Global, "", "alpha"), "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "alpha v1") {
		t.Fatalf("installed content is not the inspected commit's:\n%s", body)
	}
	st, err := state.Load(home)
	if err != nil {
		t.Fatal(err)
	}
	e, _ := st.Get("alpha")
	if e.Ref != "main" || e.Revision != git("rev-parse", v1+":alpha") {
		t.Fatalf("recorded ref/revision = %q/%q, want main and the v1 tree", e.Ref, e.Revision)
	}
}

// TestSrcIdentityResolvesRelativeAgainstBase: a legacy relative local Source
// is resolved against SrcIdentity.Base, never the process's working directory.
func TestSrcIdentityResolvesRelativeAgainstBase(t *testing.T) {
	legacy := state.SkillEntry{Kind: state.KindLocal, Source: "./skills/foo"}
	base := filepath.Join(string(filepath.Separator)+"proj", "x")
	abs := filepath.Join(base, "skills", "foo")
	if !(SrcIdentity{Kind: state.KindLocal, Source: abs, Base: base}).Matches(legacy) {
		t.Error("a relative recorded source must resolve against Base")
	}
	if (SrcIdentity{Kind: state.KindLocal, Source: abs}).Matches(legacy) {
		t.Error("with no Base a relative source must not match an absolute one")
	}
}
