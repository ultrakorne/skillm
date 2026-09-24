package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestUpdatePrunedAfterAdvancingSummary: when a skill's upstream advanced but
// every recorded copy had vanished, update reports it updated and forgotten,
// and must not then claim "Everything is up to date.".
func TestUpdatePrunedAfterAdvancingSummary(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	bin := skillmBinary(t)
	repo, url := initSkillRepo(t)
	e := env{home: t.TempDir(), userDir: t.TempDir(), bin: bin}

	e.run(t, "install", url, "alpha", "--global")
	writeSkillMD(t, filepath.Join(repo, "alpha"), "alpha", "alpha body v2")
	runGit(t, repo, "add", "-A")
	runGit(t, repo, "commit", "-q", "-m", "alpha v2")
	if err := os.RemoveAll(agentsGlobalCopy(e, "alpha")); err != nil {
		t.Fatal(err)
	}

	out := e.run(t, "update")
	if !strings.Contains(out, "Updated alpha.") || !strings.Contains(out, "forgetting global install of alpha") {
		t.Fatalf("update should report alpha updated and forgotten, got:\n%s", out)
	}
	if strings.Contains(out, "Everything is up to date.") {
		t.Fatalf("a skill advanced, so update must not claim everything was up to date:\n%s", out)
	}
}
