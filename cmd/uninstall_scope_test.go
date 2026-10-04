package cmd

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ultrakorne/skillm/internal/protocol"
)

func TestUninstallScopeFlags(t *testing.T) {
	e := env{home: t.TempDir(), userDir: t.TempDir(), bin: skillmBinary(t)}
	src := filepath.Join(t.TempDir(), "demo")
	writeSkillMD(t, src, "demo", "selected source")
	project, other := t.TempDir(), t.TempDir()
	e.run(t, "install", src, "--global")
	e.runIn(t, project, "install", "demo", "--local")
	e.runIn(t, other, "install", "demo", "--local")

	if out, err := e.tryRun(t, "uninstall", "demo", "--global", "--project", project, "--yes"); err == nil {
		t.Fatalf("conflicting scopes accepted: %s", out)
	}
	var un protocol.UninstallData
	e.jsonOK(t, project, &un, "uninstall", "demo", "--local", "--json", "--yes", "--confirmed-root", project)
	if len(un.Skills) != 1 || !slices.Equal(un.Skills[0].RemovedCopies, []string{project}) {
		t.Fatalf("local result: %+v", un)
	}
	assertNoLink(t, filepath.Join(project, ".agents", "skills", "demo"), "local copy must be removed")
	assertNoLink(t, filepath.Join(project, ".claude", "skills", "demo"), "local link must be removed")
	if !loadState(t, e).IsGlobal("demo") {
		t.Fatal("local uninstall removed global tracking")
	}
	// --all refers to every skill in this target, rather than every scope.
	parent := filepath.Dir(other)
	e.runIn(t, parent, "uninstall", "--all", "--project", filepath.Base(other), "--yes")
	assertNoLink(t, filepath.Join(other, ".agents", "skills", "demo"), "project copy must be removed")
	if _, err := os.Stat(filepath.Join(agentsGlobalCopy(e, "demo"), "SKILL.md")); err != nil {
		t.Fatalf("project uninstall removed the global copy: %v", err)
	}
	e.jsonOK(t, e.userDir, &un, "uninstall", "--all", "--global", "--json", "--yes", "--confirmed-root=")
	if len(un.Skills) != 1 || !slices.Equal(un.Skills[0].RemovedCopies, []string{"global"}) {
		t.Fatalf("global result: %+v", un)
	}
	if _, ok := loadState(t, e).Get("demo"); ok {
		t.Fatal("last install removal must drop the registry entry")
	}
}

func TestUninstallScopeTracksLegacyProject(t *testing.T) {
	e := env{home: t.TempDir(), userDir: t.TempDir(), bin: skillmBinary(t)}
	src := filepath.Join(t.TempDir(), "demo")
	writeSkillMD(t, src, "demo", "selected source")
	e.run(t, "install", src, "--global")
	project := t.TempDir()
	legacy := filepath.Join(e.home, "skills", "demo")
	writeSkillMD(t, legacy, "demo", "legacy copy")
	link := filepath.Join(project, ".claude", "skills", "demo")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(legacy, link); err != nil {
		t.Fatal(err)
	}
	// The project is discovered through cwd, despite having no copy marker
	// or tracked root. Removing Global must keep its legacy install usable.
	e.runIn(t, project, "uninstall", "demo", "--global", "--yes")
	st := loadState(t, e)
	if _, ok := st.Get("demo"); !ok || !slices.Contains(st.LocalRoots, project) {
		t.Fatalf("remaining project must be tracked: %+v", st)
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("global uninstall removed the local link: %v", err)
	}
	// A foreign canonical directory at the legacy project's slot is not
	// recorded, so scoped removal must leave it alone.
	foreign := filepath.Join(project, ".agents", "skills", "demo")
	writeSkillMD(t, foreign, "demo", "foreign directory")
	e.run(t, "uninstall", "--all", "--project", project, "--yes")
	assertNoLink(t, link, "--all must select and remove a legacy-only local install")
	if _, ok := loadState(t, e).Get("demo"); ok {
		t.Fatal("last legacy install's registry entry remains")
	}
	if _, err := os.Stat(filepath.Join(foreign, "SKILL.md")); err != nil {
		t.Fatalf("unrecorded canonical directory was removed: %v", err)
	}
}

func TestInstallForeignAgentDirectory(t *testing.T) {
	e := env{home: t.TempDir(), userDir: t.TempDir(), bin: skillmBinary(t)}
	src := filepath.Join(t.TempDir(), "demo")
	writeSkillMD(t, src, "demo", "selected source")
	foreign := claudeGlobalLink(e, "demo")
	writeSkillMD(t, foreign, "demo", "old installer")

	out := e.run(t, "install", src, "--global")
	if !strings.Contains(out, foreign) || !strings.Contains(out, "this agent keeps its existing skill; retry with --force") {
		t.Fatalf("install must explain the partial result and how to take over: %s", out)
	}
	if _, err := os.Stat(filepath.Join(agentsGlobalCopy(e, "demo"), "SKILL.md")); err != nil {
		t.Fatalf("canonical install must succeed despite the refused link: %v", err)
	}
	stdout, stderr, ok := e.runJSON(t, e.userDir, nil, "install", src, "demo", "--global", "--json", "--yes")
	if !ok || stderr != "" {
		t.Fatalf("JSON install must retain its warning contract: ok=%v stderr=%s stdout=%s", ok, stderr, stdout)
	}
	doc := decodeDoc(t, stdout)
	if doc.Error != nil || len(doc.Warnings) != 1 || doc.Warnings[0].Code != "link_refused" {
		t.Fatalf("JSON must report link_refused instead of a foreign-files error: %s", stdout)
	}
	b, err := os.ReadFile(filepath.Join(foreign, "SKILL.md"))
	if err != nil || !strings.Contains(string(b), "old installer") {
		t.Fatalf("refusing must leave the old skill intact: %q %v", b, err)
	}
	e.run(t, "install", src, "--global", "--force")
	if info, err := os.Lstat(foreign); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("force must take over the foreign directory: %v", err)
	}
	b, err = os.ReadFile(filepath.Join(foreign, "SKILL.md"))
	if err != nil || !strings.Contains(string(b), "selected source") {
		t.Fatalf("agent must read selected source after takeover: %q %v", b, err)
	}
}
