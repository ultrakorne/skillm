package protocol

import (
	"os/exec"
	"strings"
	"testing"
)

// forbiddenDeps are the presentation and CLI packages protocol must never
// depend on: it serializes core's results for any front-end, so it stays
// below cmd and the terminal UI.
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

// TestProtocolStaysPresentationFree fails when internal/protocol depends on
// a presentation or CLI package, per `go list -deps`.
func TestProtocolStaysPresentationFree(t *testing.T) {
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
				t.Errorf("internal/protocol depends on %s", dep)
			}
		}
	}
}
