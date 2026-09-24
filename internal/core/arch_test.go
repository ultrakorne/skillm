package core

import (
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// forbiddenDeps are the packages core must never depend on, directly or
// transitively: presentation (ui, lipgloss, bubbletea, bubbles, huh), the
// cobra/fang CLI layer and cmd itself. A prefix matches the package and
// everything under it.
var forbiddenDeps = []string{
	"github.com/ultrakorne/skillm/cmd",
	"github.com/ultrakorne/skillm/internal/ui",
	"github.com/spf13/cobra",
	"github.com/charmbracelet/fang",
	"charm.land/huh",
	"charm.land/bubbletea",
	"charm.land/bubbles",
	"charm.land/lipgloss",
}

// TestCoreStaysPresentationFree fails when internal/core depends on a
// presentation or CLI package, per `go list -deps`.
func TestCoreStaysPresentationFree(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH")
	}
	out, err := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps: %v\n%s", err, out)
	}
	for _, dep := range strings.Fields(string(out)) {
		for _, bad := range forbiddenDeps {
			if dep == bad || strings.HasPrefix(dep, bad+"/") {
				t.Errorf("internal/core depends on %s", dep)
			}
		}
	}
}

// forbiddenRefs are the process-global inputs and outputs core must take as
// parameters instead: the working directory (read directly, or implicitly by
// filepath.Abs — use ResolvePath with Options.Cwd) and the standard streams.
var forbiddenRefs = []string{"os.Getwd", "filepath.Abs", "os.Stdout", "os.Stderr", "os.Stdin"}

// TestCoreReadsNoProcessGlobals fails when a non-test core source file
// mentions os.Getwd, filepath.Abs or a standard stream.
func TestCoreReadsNoProcessGlobals(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		// Parse first so a comment mentioning one of them is not a hit.
		file, err := parser.ParseFile(fset, f, src, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
		code := string(src)
		for _, c := range file.Comments {
			start, end := fset.Position(c.Pos()).Offset, fset.Position(c.End()).Offset
			code = code[:start] + strings.Repeat(" ", end-start) + code[end:]
		}
		for _, ref := range forbiddenRefs {
			if strings.Contains(code, ref) {
				t.Errorf("%s uses %s; take it as a parameter instead", f, ref)
			}
		}
	}
}
