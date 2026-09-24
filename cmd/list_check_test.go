package cmd

import (
	"fmt"
	"testing"

	"github.com/ultrakorne/skillm/internal/core"
)

// TestInstalledLabel verifies the Installed column rendered from core's
// installs: global first, local installs by their project root, installs
// serving no agent left out, and "-" for none at all.
func TestInstalledLabel(t *testing.T) {
	cwd := t.TempDir()
	other := t.TempDir()
	installs := []core.Install{
		{Scope: core.ScopeGlobal, Agents: []string{"agents", "claude"}},
		{Scope: core.ScopeLocal, Root: cwd, Agents: []string{"claude"}},
		{Scope: core.ScopeLocal, Root: other, Agents: []string{"agents"}},
		{Scope: core.ScopeLocal, Root: t.TempDir(), Recorded: true},
	}
	want := fmt.Sprintf("global; %s; %s", cwd, other)
	if got := installedLabel(installs); got != want {
		t.Fatalf("label = %q, want %q", got, want)
	}
	if got := installedLabel(installs[3:]); got != "-" {
		t.Fatalf("label with no served agents = %q, want \"-\"", got)
	}
}
