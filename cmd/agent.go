package cmd

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ultrakorne/skillm/internal/core"
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
			"aborting the sweep.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgent(cmd.Context())
		},
	}
	return c
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
	return setAgents(ctx, enable, disable)
}

// errNoAgentEnabled is the CLI's wording of core.ErrNoAgentEnabled.
var errNoAgentEnabled = errors.New("at least one agent must stay enabled; to remove skills entirely use `skillm uninstall`")

// setAgents enables and disables the named agents under Home's lock and
// reports the enabled set afterwards.
func setAgents(ctx context.Context, enable, disable []string) error {
	opts, err := coreOptions(true)
	if err != nil {
		return err
	}
	unlock, err := lockHome(ctx, opts.Home, "skillm agent")
	if err != nil {
		return err
	}
	defer unlock()

	res, err := core.SetAgents(ctx, opts, termLog, enable, disable)
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
