package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ultrakorne/skillm/internal/core"
	"github.com/ultrakorne/skillm/internal/protocol"
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
			"every scope and tracked project; the skills stay installed and stay linked for the " +
			"other agents, so this is not the same as uninstall. At least one agent must remain " +
			"enabled — deselecting every agent is refused (use `skillm uninstall` to remove " +
			"skills themselves). A change that removes links confirms first on a terminal unless " +
			"--yes or --force is given; a blocked spot is skipped with a warning rather than " +
			"aborting the sweep.\n\n" +
			"`skillm agent ls` lists the defined agents and `skillm agent set --enable a " +
			"--disable b` makes the same change without the picker; both have a JSON mode.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgent(cmd.Context())
		},
	}
	c.AddCommand(newAgentLsCmd(), newAgentSetCmd())
	return c
}

func newAgentLsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ls",
		Short: "List the agents defined in config.toml and whether each is enabled",
		Long: "agent ls lists every agent defined in config.toml, sorted by name, with " +
			"whether it is enabled and the skill folders it reads globally and in a " +
			"project. It only reads config.toml.",
		Args: cobra.NoArgs,
		// It reads config.toml alone, so it works without git.
		Annotations: map[string]string{annotationJSON: "true", annotationSkipGitCheck: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentLs()
		},
	}
}

var (
	agentSetFlagEnable  []string
	agentSetFlagDisable []string
)

func newAgentSetCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "set --enable <agent> --disable <agent>",
		Short: "Enable or disable agents by name, reconciling their links immediately",
		Long: "agent set enables the agents named by --enable and disables those named by " +
			"--disable (repeat either flag, or separate names with commas), exactly as " +
			"picking them in `skillm agent` would: links are created or removed right " +
			"away, the skills stay installed, and at least one agent must remain enabled. " +
			"An agent already in the asked-for state is left alone. A change that removes " +
			"links confirms first on a terminal unless --yes or --force is given.\n\n" +
			"With --json it never prompts: a run with --disable needs --yes (confirm with " +
			"the user first). The working directory is not treated as a project in JSON " +
			"mode; only the global folders and the recorded projects are reconciled. An " +
			"agent that is not defined fails with code unknown_agent, and a change that " +
			"would leave no agent enabled with code no_agent_enabled; either way nothing " +
			"is written.",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{annotationJSON: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentSet(cmd.Context(), agentSetFlagEnable, agentSetFlagDisable)
		},
	}
	c.Flags().StringSliceVar(&agentSetFlagEnable, "enable", nil, "an agent to enable (repeatable, or comma-separated)")
	c.Flags().StringSliceVar(&agentSetFlagDisable, "disable", nil, "an agent to disable (repeatable, or comma-separated)")
	return c
}

// runAgentLs prints the defined agents, or returns them in JSON mode.
func runAgentLs() error {
	opts, err := coreOptions(false)
	if err != nil {
		return err
	}
	agents, err := core.Agents(opts)
	if err != nil {
		return err
	}
	if flagJSON {
		return jsonOut().Result(protocol.NewAgentsData(agents))
	}
	width := 0
	for _, a := range agents {
		width = max(width, len(a.Name))
	}
	for _, a := range agents {
		state := "disabled"
		if a.Enabled {
			state = "enabled"
		}
		fmt.Fprintf(os.Stdout, "%-*s  %-8s  global: %s  local: %s\n", width, a.Name, state, orDash(a.Global), orDash(a.Local))
	}
	return nil
}

// orDash is s, or "-" when it is empty.
func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// runAgentSet enables and disables the named agents: the flag-driven
// counterpart of runAgent's picker.
func runAgentSet(ctx context.Context, enable, disable []string) error {
	enable, disable = cleanNames(enable), cleanNames(disable)
	if len(enable) == 0 && len(disable) == 0 {
		return usageError("agent set needs --enable or --disable")
	}
	for _, n := range enable {
		if slices.Contains(disable, n) {
			return usageError(fmt.Sprintf("agent %q is both enabled and disabled; name it once", n))
		}
	}
	if flagJSON && len(disable) > 0 && !flagYes && !flagForce {
		return usageError("agent set --json with --disable needs --yes (confirm with the user first)")
	}
	// A GUI's working directory is not a project it means, so JSON mode
	// reconciles only the global folders and the recorded projects.
	opts, err := coreOptions(!flagJSON)
	if err != nil {
		return err
	}
	agents, err := core.Agents(opts)
	if err != nil {
		return err
	}
	var unknown, before []string
	for _, n := range append(slices.Clone(enable), disable...) {
		if !slices.ContainsFunc(agents, func(a core.AgentInfo) bool { return a.Name == n }) {
			unknown = append(unknown, n)
		}
	}
	if len(unknown) > 0 {
		return &core.UnknownAgentError{Names: unknown}
	}
	for _, a := range agents {
		if a.Enabled {
			before = append(before, a.Name)
		}
	}
	// Only a disable of an agent enabled now removes links, so only that asks.
	var removing []string
	for _, n := range disable {
		if slices.Contains(before, n) {
			removing = append(removing, n)
		}
	}
	if len(removing) > 0 && !flagJSON && ui.IsTTY() && !opts.Yes && !opts.Force {
		var adding []string
		for _, n := range enable {
			if !slices.Contains(before, n) {
				adding = append(adding, n)
			}
		}
		ok, err := ui.Confirm(confirmAgentPrompt(adding, removing))
		if err != nil {
			return err
		}
		if !ok {
			ui.Warnf("aborted; no agents changed")
			return nil
		}
	}
	if flagJSON {
		res, err := setAgentsLocked(ctx, opts, jsonOut(), enable, disable)
		if err != nil {
			return err
		}
		return jsonOut().Result(protocol.NewAgentsSetData(res))
	}
	return setAgents(ctx, opts, enable, disable)
}

// cleanNames trims names and drops empty and repeated ones, keeping order.
func cleanNames(names []string) []string {
	var out []string
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n != "" && !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}

func runAgent(ctx context.Context) error {
	opts, err := coreOptions(false)
	if err != nil {
		return err
	}
	// The picker and the confirmation run without Home's lock, so an open
	// prompt never blocks another skillm process; core.SetAgents re-reads
	// config.toml under the lock and applies the change to what it finds.
	agents, err := core.Agents(opts)
	if err != nil {
		return err
	}
	var all, before []string
	for _, a := range agents {
		all = append(all, a.Name)
		if a.Enabled {
			before = append(before, a.Name)
		}
	}

	// The picker offers every defined agent, pre-checking those enabled now.
	selection, err := ui.SelectAgents(all, before)
	if err != nil {
		return err
	}

	// At least one agent must stay enabled. Deselecting everything would strip
	// every link, which is `uninstall`'s job (it also deletes the canonical
	// copies), so we refuse here and point there. Nothing is written or unlinked.
	if len(selection) == 0 {
		return errNoAgentEnabled
	}

	// Only the agents whose state changes are touched: `agent` toggles, it
	// does not repair drift (use `skillm install` for that).
	enable, disable := agentDiff(before, selection)
	if len(enable) == 0 && len(disable) == 0 {
		ui.Successf("no changes (enabled agents: %s)", strings.Join(selection, ", "))
		return nil
	}

	// Confirm only when links will be removed (a disable is present); an
	// enable-only change is additive and safe, so it applies without a prompt.
	if len(disable) > 0 && ui.IsTTY() && !opts.Yes && !opts.Force {
		ok, err := ui.Confirm(confirmAgentPrompt(enable, disable))
		if err != nil {
			return err
		}
		if !ok {
			ui.Warnf("aborted; no agents changed")
			return nil
		}
	}
	opts, err = coreOptions(true)
	if err != nil {
		return err
	}
	return setAgents(ctx, opts, enable, disable)
}

// errNoAgentEnabled is the CLI's wording of core.ErrNoAgentEnabled.
var errNoAgentEnabled = errors.New("at least one agent must stay enabled; to remove skills entirely use `skillm uninstall`")

// setAgents enables and disables the named agents under Home's lock and
// prints the enabled set afterwards.
func setAgents(ctx context.Context, opts core.Options, enable, disable []string) error {
	res, err := setAgentsLocked(ctx, opts, termLog, enable, disable)
	if errors.Is(err, core.ErrNoAgentEnabled) {
		return errNoAgentEnabled
	}
	if err != nil {
		return err
	}
	if len(res.Changes) == 0 {
		ui.Successf("no changes (enabled agents: %s)", strings.Join(res.Enabled, ", "))
		return nil
	}
	ui.Successf("enabled agents: %s", strings.Join(res.Enabled, ", "))
	return nil
}

// setAgentsLocked runs core.SetAgents under Home's lock; SetAgents re-reads
// config.toml and the Registry under it.
func setAgentsLocked(ctx context.Context, opts core.Options, rep core.Reporter, enable, disable []string) (core.AgentsResult, error) {
	unlock, err := lockHome(ctx, opts.Home, "skillm agent")
	if err != nil {
		return core.AgentsResult{}, err
	}
	defer unlock()
	return core.SetAgents(ctx, opts, rep, enable, disable)
}

// agentDiff returns the agents in after but not in before (to enable) and
// those in before but not in after (to disable), each in its input order.
func agentDiff(before, after []string) (enable, disable []string) {
	for _, n := range after {
		if !slices.Contains(before, n) {
			enable = append(enable, n)
		}
	}
	for _, n := range before {
		if !slices.Contains(after, n) {
			disable = append(disable, n)
		}
	}
	return enable, disable
}

// confirmAgentPrompt builds the single confirmation shown before a reconcile
// that removes links, naming the agents and reassuring that the skills'
// canonical copies (global and the projects' committed ones) are untouched.
func confirmAgentPrompt(newlyEnabled, newlyDisabled []string) string {
	dis := strings.Join(newlyDisabled, ", ")
	if len(newlyEnabled) == 0 {
		return fmt.Sprintf("Disable %s? This removes its links from every scope and project; the skills stay installed (their canonical copies are untouched).", dis)
	}
	en := strings.Join(newlyEnabled, ", ")
	return fmt.Sprintf("Enable %s and disable %s? Disabling removes links from every scope and project; the skills stay installed (their canonical copies are untouched).", en, dis)
}
