package linker

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ultrakorne/skillm/internal/agentdir"
)

func TestLinkConflicts(t *testing.T) {
	for _, kind := range []string{"absent", "managed", "directory", "file", "foreign link", "aliased folder"} {
		t.Run(kind, func(t *testing.T) {
			fx := newFixture(t, "demo")
			a := claude(t)
			path, _ := agentdir.LinkPath(a, agentdir.Local, fx.cwd, "demo")
			folder := filepath.Dir(path)
			if err := os.MkdirAll(folder, 0o755); err != nil {
				t.Fatal(err)
			}
			var err error
			switch kind {
			case "managed":
				_, err = Link(fx.home, "demo", []agentdir.Agent{a}, agentdir.Local, fx.cwd)
			case "directory":
				err = os.Mkdir(path, 0o755)
			case "file":
				err = os.WriteFile(path, []byte("mine"), 0o644)
			case "foreign link":
				err = os.Symlink(t.TempDir(), path)
			case "aliased folder":
				if err := os.Remove(folder); err != nil {
					t.Fatal(err)
				}
				err = os.Symlink(agentdir.CanonicalLocalDir(fx.cwd), folder)
			}
			if err != nil {
				t.Fatal(err)
			}
			paths, err := LinkConflicts(fx.home, "demo", []agentdir.Agent{a, {Name: "agents", Local: ".agents/skills"}}, agentdir.Local, fx.cwd)
			foreign := kind == "directory" || kind == "file" || kind == "foreign link"
			if err != nil || (foreign && (len(paths) != 1 || paths[0] != path)) || (!foreign && len(paths) != 0) {
				t.Fatalf("conflicts=%v err=%v, foreign=%v", paths, err, foreign)
			}
		})
	}
}
