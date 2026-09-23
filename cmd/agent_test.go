package cmd

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/ultrakorne/skillm/internal/core"
)

// TestConfirmAgentPrompt names the affected agents and reassures that the
// skills stay installed, in both the disable-only and swap shapes.
func TestConfirmAgentPrompt(t *testing.T) {
	disableOnly := confirmAgentPrompt(nil, []string{"claude"})
	if !strings.Contains(disableOnly, "claude") || !strings.Contains(disableOnly, "stay installed") {
		t.Fatalf("disable-only prompt missing agent or reassurance: %q", disableOnly)
	}
	if strings.Contains(disableOnly, "Enable") {
		t.Fatalf("disable-only prompt should not mention enabling: %q", disableOnly)
	}

	swap := confirmAgentPrompt([]string{"codex"}, []string{"claude"})
	if !strings.Contains(swap, "Enable codex") || !strings.Contains(swap, "disable claude") {
		t.Fatalf("swap prompt missing enable/disable detail: %q", swap)
	}
}

// TestAgentDiff splits a new selection into the agents to enable and to
// disable, leaving the unchanged ones out.
func TestAgentDiff(t *testing.T) {
	enable, disable := agentDiff([]string{"agents", "claude"}, []string{"claude", "codex"})
	if strings.Join(enable, ",") != "codex" || strings.Join(disable, ",") != "agents" {
		t.Fatalf("agentDiff = enable %v, disable %v; want [codex], [agents]", enable, disable)
	}
	enable, disable = agentDiff([]string{"claude"}, []string{"claude"})
	if len(enable) != 0 || len(disable) != 0 {
		t.Fatalf("agentDiff of an unchanged selection = %v, %v; want none", enable, disable)
	}
}

// TestAgentLogLines: the lines core's SetAgents reports print exactly as the
// command printed them before it moved into core — the terminal adds the
// command advice core's Text leaves out, and prints copies_kept as a hint.
func TestAgentLogLines(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = w
	termLog.Event(core.Event{Type: core.EventLog, Level: core.LevelSuccess, Code: core.CodeAgentEnabledEmpty,
		Text: "enabled codex — nothing to install yet"})
	termLog.Event(core.Event{Type: core.EventLog, Level: core.LevelInfo, Code: core.CodeCopiesKept,
		Text: "global copies in ~/.agents/skills stay in place"})
	os.Stdout = stdout
	w.Close()
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	want := "✓ enabled codex — nothing to install yet (run `skillm install`)\n" +
		"→ global copies in ~/.agents/skills stay in place; use `skillm uninstall` to remove skills entirely\n"
	if string(out) != want {
		t.Fatalf("output = %q, want %q", out, want)
	}
}
