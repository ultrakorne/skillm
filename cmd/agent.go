package cmd

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ultrakorne/skillm/internal/agentdir"
	"github.com/ultrakorne/skillm/internal/config"
	"github.com/ultrakorne/skillm/internal/linker"
	"github.com/ultrakorne/skillm/internal/state"
	"github.com/ultrakorne/skillm/internal/store"
	"github.com/ultrakorne/skillm/internal/ui"
)

func init() {
	rootCmd.AddCommand(newAgentCmd())
}

func newAgentCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "agent",
		Short: "Enable or disable agents, reconciling their links immediately",
		Long: "agent shows an interactive multiselect of the agents defined in config.toml, " +
			"seeded with the currently enabled set. Changing the selection enables or disables " +
			"the affected agents and reconciles their symlinks right away — it never only " +
			"\"affects future installs\". Enabling an agent creates a link for it at every place " +
			"the already-enabled agents are linked (the global folder and every tracked project), " +
			"bringing it to parity with its peers. Disabling an agent removes its links across " +
			"every scope and tracked project; the skills stay in Home and stay linked for the " +
			"other agents, so this is not the same as uninstall. At least one agent must remain " +
			"enabled — deselecting every agent is refused (use `skillm uninstall` to remove " +
			"skills themselves). A change that removes links confirms first on a terminal unless " +
			"--yes or --force is given; a blocked spot is skipped with a warning rather than " +
			"aborting the sweep.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgent()
		},
	}
	return c
}

func runAgent() error {
	home, err := store.Home(flagHome)
	if err != nil {
		return err
	}

	cfg, err := config.Load(home)
	if err != nil {
		return err
	}

	// Snapshot the enabled set BEFORE the toggle. The enable pass mirrors the
	// links these agents currently hold, so we must capture them before any
	// change is applied (config or disk).
	beforeEnabled := cfg.EnabledAgents()
	beforeNames := cfg.EnabledNames()

	// The picker offers every defined agent, pre-checking those enabled now.
	selection, err := ui.SelectAgents(cfg.AgentNames(), beforeNames)
	if err != nil {
		return err
	}

	// At least one agent must stay enabled. Deselecting everything would strip
	// every link, which is `uninstall`'s job (it also deletes the Home copy), so
	// we refuse here and point there. Nothing is written or unlinked.
	if len(selection) == 0 {
		return fmt.Errorf("at least one agent must stay enabled; to remove skills entirely use `skillm uninstall`")
	}

	// Partition the defined agents by how their enabled state is changing.
	// Unchanged agents are never touched: `agent` toggles, it does not repair
	// drift (use `skillm install` for that).
	after := nameSet(selection)
	before := nameSet(beforeNames)
	var newlyEnabled, newlyDisabled []agentdir.Agent
	for _, a := range cfg.AllAgents() {
		switch {
		case after[a.Name] && !before[a.Name]:
			newlyEnabled = append(newlyEnabled, a)
		case !after[a.Name] && before[a.Name]:
			newlyDisabled = append(newlyDisabled, a)
		}
	}
	if len(newlyEnabled) == 0 && len(newlyDisabled) == 0 {
		ui.Successf("no changes (enabled agents: %s)", strings.Join(selection, ", "))
		return nil
	}

	// Load everything the reconcile needs before writing anything, so a load
	// failure aborts cleanly without a half-applied change.
	st, err := state.Load(home)
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("determine current directory: %w", err)
	}

	// Confirm only when links will be removed (a disable is present); an
	// enable-only change is additive and safe, so it applies without a prompt.
	if len(newlyDisabled) > 0 && ui.IsTTY() && !flagYes && !flagForce {
		ok, err := ui.Confirm(confirmAgentPrompt(newlyEnabled, newlyDisabled))
		if err != nil {
			return err
		}
		if !ok {
			ui.Warnf("aborted; no agents changed")
			return nil
		}
	}

	// Persist the new enabled flags — the durable intent. Disk is then reconciled
	// best-effort below: a blocked spot is skipped with a warning.
	cfg.SetEnabled(selection)
	if err := config.Save(home, cfg); err != nil {
		return err
	}

	// Enable pass before disable pass, so a one-shot swap (disable A, enable B)
	// lets B copy A's links while they are still on disk.
	stateChanged := false
	for _, a := range newlyEnabled {
		if enableAgent(home, a, beforeEnabled, st, cwd) {
			stateChanged = true
		}
	}
	for _, a := range newlyDisabled {
		disableAgent(home, a, st, cwd)
	}

	// A project that lost its last skillm link is no longer worth tracking. Scan
	// across all defined agents so a root kept alive by a still-enabled agent
	// survives.
	if reconcileLocalRoots(home, cfg.AllAgents(), st) {
		stateChanged = true
	}
	if stateChanged {
		if err := state.Save(home, st); err != nil {
			return err
		}
	}

	ui.Successf("enabled agents: %s", strings.Join(selection, ", "))
	return nil
}

// enableAgent links one newly-enabled agent at every place the before-enabled
// agents are currently linked: the global folder and every tracked local root
// (plus the current directory). The footprint is read live from disk, so it
// reflects exactly what the peer agents have. A spot blocked by a foreign file
// or symlink is skipped with a warning instead of aborting the sweep. It returns
// true when it changes the tracked local roots in state.
func enableAgent(home string, a agentdir.Agent, beforeEnabled []agentdir.Agent, st *state.State, cwd string) bool {
	one := []agentdir.Agent{a}
	skills := map[string]bool{}
	stateChanged := false
	var places []string

	linkAt := func(scope agentdir.Scope, base string) {
		// At local scope, ignore the footprint of any before-enabled peer whose
		// local folder aliases its global one at base (e.g. home): scanning it
		// would read that peer's GLOBAL links as a phantom local footprint and
		// mirror those global-only skills into the newly enabled agent. Global
		// scope needs no such filter — a global folder is always real.
		sources := beforeEnabled
		if scope == agentdir.Local {
			sources, _ = splitLocalAliased(beforeEnabled, base)
		}
		got := false
		for _, id := range footprintIDs(home, sources, scope, base) {
			res, err := linker.Link(home, id, one, scope, base)
			if err != nil {
				ui.Warnf("%v", err)
			}
			for _, ar := range res.Agents {
				if ar.Action == linker.ActionCreated || ar.Action == linker.ActionAlreadyLinked {
					skills[id] = true
					got = true
				}
			}
		}
		if got {
			places = append(places, scopeLabel(scope, base, cwd))
			if scope == agentdir.Local && st.AddLocalRoot(base) {
				stateChanged = true
			}
		}
	}

	if a.Supports(agentdir.Global) {
		linkAt(agentdir.Global, cwd) // base is ignored for global scope
	}
	if a.Supports(agentdir.Local) {
		for _, dir := range localScanDirs(st.LocalRoots, cwd) {
			// Skip a dir where this agent's local folder is its global one (e.g.
			// home): the global pass already mirrored those links, and recording
			// the dir as a local root would be bogus.
			if agentdir.LocalAliasesGlobal(a, dir) {
				continue
			}
			linkAt(agentdir.Local, dir)
		}
	}

	if len(skills) == 0 {
		ui.Successf("enabled %s — nothing to link yet (run `skillm install`)", a.Name)
		return stateChanged
	}
	ui.Successf("enabled %s — linked %d skill%s (%s)", a.Name, len(skills), plural(len(skills)), strings.Join(places, ", "))
	return stateChanged
}

// disableAgent removes every skillm-managed link one newly-disabled agent holds,
// across the global folder and every tracked local root (plus the current
// directory). It scans for the agent's own links and unlinks each; foreign files
// and symlinks are never touched. A refusal is reported as a warning so one
// obstruction never aborts the sweep. The Home copy is left intact — disabling an
// agent is not uninstalling a skill.
func disableAgent(home string, a agentdir.Agent, st *state.State, cwd string) {
	one := []agentdir.Agent{a}
	skills := map[string]bool{}
	var places []string

	unlinkAt := func(scope agentdir.Scope, base string) {
		infos, err := linker.ScanAll(home, one, scope, base)
		if err != nil {
			ui.Warnf("scan %s (%s): %v", a.Name, scopeLabel(scope, base, cwd), err)
			return
		}
		got := false
		for _, li := range infos {
			res, err := linker.Unlink(home, li.ID, one, scope, base)
			if err != nil {
				ui.Warnf("%v", err)
			}
			for _, ar := range res.Agents {
				if ar.Action == linker.ActionRemoved {
					skills[li.ID] = true
					got = true
				}
			}
		}
		if got {
			places = append(places, scopeLabel(scope, base, cwd))
		}
	}

	if a.Supports(agentdir.Global) {
		unlinkAt(agentdir.Global, cwd)
	}
	if a.Supports(agentdir.Local) {
		for _, dir := range localScanDirs(st.LocalRoots, cwd) {
			// Skip a dir where this agent's local folder is its global one (e.g.
			// home): the global pass already removed those links there.
			if agentdir.LocalAliasesGlobal(a, dir) {
				continue
			}
			unlinkAt(agentdir.Local, dir)
		}
	}

	if len(skills) == 0 {
		ui.Successf("disabled %s — no links to remove", a.Name)
		return
	}
	ui.Successf("disabled %s — unlinked %d skill%s (%s)", a.Name, len(skills), plural(len(skills)), strings.Join(places, ", "))
}

// footprintIDs returns the sorted, de-duplicated skill ids that any of agents
// has linked at (scope, base) — the set a newly-enabled agent mirrors. A scan
// error yields no ids rather than failing the reconcile.
func footprintIDs(home string, agents []agentdir.Agent, scope agentdir.Scope, base string) []string {
	infos, err := linker.ScanAll(home, agents, scope, base)
	if err != nil {
		return nil
	}
	seen := make(map[string]bool, len(infos))
	ids := make([]string, 0, len(infos))
	for _, li := range infos {
		if !seen[li.ID] {
			seen[li.ID] = true
			ids = append(ids, li.ID)
		}
	}
	sort.Strings(ids)
	return ids
}

// confirmAgentPrompt builds the single confirmation shown before a reconcile
// that removes links, naming the agents and reassuring that Home is untouched.
func confirmAgentPrompt(newlyEnabled, newlyDisabled []agentdir.Agent) string {
	dis := strings.Join(agentNames(newlyDisabled), ", ")
	if len(newlyEnabled) == 0 {
		return fmt.Sprintf("Disable %s? This removes its links from every scope and project; the skills stay in Home.", dis)
	}
	en := strings.Join(agentNames(newlyEnabled), ", ")
	return fmt.Sprintf("Enable %s and disable %s? Disabling removes links from every scope and project; the skills stay in Home.", en, dis)
}

// agentNames returns the names of agents in slice order.
func agentNames(agents []agentdir.Agent) []string {
	names := make([]string, 0, len(agents))
	for _, a := range agents {
		names = append(names, a.Name)
	}
	return names
}

// nameSet returns the set of names for fast membership tests.
func nameSet(names []string) map[string]bool {
	s := make(map[string]bool, len(names))
	for _, n := range names {
		s[n] = true
	}
	return s
}

// plural returns "s" unless n is exactly 1, for simple count messages.
func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
