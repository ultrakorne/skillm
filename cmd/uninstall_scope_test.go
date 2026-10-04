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

func TestInstallForeignAgentDirectory(t *testing.T) {
	e := env{home: t.TempDir(), userDir: t.TempDir(), bin: skillmBinary(t)}
	src := filepath.Join(t.TempDir(), "demo")
	writeSkillMD(t, src, "demo", "selected source")
	foreign := claudeGlobalLink(e, "demo")
	writeSkillMD(t, foreign, "demo", "old installer")

	out, err := e.tryRun(t, "install", src, "--global")
	if err == nil || !strings.Contains(out, foreign) || !strings.Contains(out, "--force") {
		t.Fatalf("non-interactive install must explain takeover: err=%v out=%s", err, out)
	}
	assertNoLink(t, agentsGlobalCopy(e, "demo"), "refusal must happen before installing a duplicate")
	perr := e.jsonFail(t, e.userDir, "install", src, "demo", "--global", "--json", "--yes")
	if perr.Code != protocol.CodeForeignFiles || !slices.Equal(perr.Paths, []string{foreign}) {
		t.Fatalf("foreign agent path must be included in JSON refusal: %+v", perr)
	}
	var installed protocol.InstallData
	e.jsonOK(t, e.userDir, &installed, "install", src, "demo", "--global", "--json", "--skip-foreign")
	if len(installed.Skills) != 1 || installed.Skills[0].Action != protocol.ActionSkipped {
		t.Fatalf("skip result: %+v", installed)
	}
	assertNoLink(t, agentsGlobalCopy(e, "demo"), "skipping must leave no duplicate")
	b, err := os.ReadFile(filepath.Join(foreign, "SKILL.md"))
	if err != nil || !strings.Contains(string(b), "old installer") {
		t.Fatalf("skipping changed the old skill: %q %v", b, err)
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
