package cmd

import (
	"errors"
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

var uninstallFlagAll bool

func init() {
	rootCmd.AddCommand(newUninstallCmd())
}

func newUninstallCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "uninstall [skill_id...]",
		Short: "Remove skills from Home, unlinking them from every agent first",
		Long: "uninstall removes skills entirely. For each skill it removes its Global " +
			"install (the ~/.agents/skills copy and every agent's symlink) and its Local " +
			"installs in every recorded project (copy, links, and skills-lock.json entry, " +
			"tracked in state.toml) — the only copies there are — then drops its registry " +
			"entry so no dangling symlinks are left behind. There is no per-scope uninstall " +
			"— it always clears every reference. Pass one or more skill ids, --all to remove " +
			"every installed skill, or no arguments to pick interactively. On a terminal it " +
			"confirms first unless --yes or --force is given.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUninstall(args, uninstallFlagAll)
		},
	}
	c.Flags().BoolVar(&uninstallFlagAll, "all", false, "remove every skill in Home")
	return c
}

func runUninstall(args []string, all bool) error {
	home, err := store.Home(flagHome)
	if err != nil {
		return err
	}

	cfg, err := config.Load(home)
	if err != nil {
		return err
	}

	st, err := state.Load(home)
	if err != nil {
		return err
	}

	ids, err := selectUninstallIDs(home, st, args, all)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil // selectUninstallIDs already reported why (empty Home / nothing picked)
	}

	// One confirmation covers the whole batch. As with the rest of skillm, the
	// prompt only appears on a TTY; a non-interactive run proceeds (pass --yes
	// to be explicit), so scripts are not blocked. The prompt names any project
	// where committed copies will be deleted, since that edits the user's repo.
	if ui.IsTTY() && !flagYes && !flagForce {
		ok, err := ui.Confirm(confirmUninstallPrompt(ids, vendoredDirsForIDs(st, ids)))
		if err != nil {
			return err
		}
		if !ok {
			ui.Warnf("aborted; nothing was removed")
			return nil
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("determine current directory: %w", err)
	}

	// Clear links for EVERY defined agent (not just the enabled ones): a link
	// made while an agent was enabled must not be left dangling just because it
	// is disabled now.
	agents := cfg.AllAgents()
	for _, id := range ids {
		if err := uninstallOne(home, agents, st, id, cwd); err != nil {
			return err
		}
		ui.Successf("uninstalled %s", id)
		// Persist after each skill so disk and registry never drift if a later
		// skill fails mid-batch.
		if err := state.Save(home, st); err != nil {
			return err
		}
	}

	// Drop any tracked root that no longer holds a link now that these skills'
	// local links are gone.
	if reconcileLocalRoots(home, agents, st) {
		if err := state.Save(home, st); err != nil {
			return err
		}
	}
	return nil
}

// selectUninstallIDs resolves which skills `uninstall` should act on. Explicit
// ids must each be known to skillm (present in Home or the registry); any
// unknown id is an atomic error so a typo removes nothing. --all targets every
// registered skill; with no arguments an interactive multiselect is shown (which
// refuses on a non-TTY). It returns an empty slice and no error when there is
// nothing to do, having already told the user why.
func selectUninstallIDs(home string, st *state.State, args []string, all bool) ([]string, error) {
	if len(args) > 0 {
		if all {
			return nil, errors.New("pass either skill ids or --all, not both")
		}
		var missing []string
		for _, id := range args {
			if _, inRegistry := st.Get(id); !inRegistry {
				missing = append(missing, id)
			}
		}
		if len(missing) > 0 {
			return nil, fmt.Errorf("not installed: %s; nothing to uninstall", strings.Join(missing, ", "))
		}
		return args, nil
	}

	registered := registeredIDs(st)
	if len(registered) == 0 {
		ui.Warnf("no skills in Home; nothing to uninstall")
		return nil, nil
	}
	if all {
		return registered, nil
	}

	opts := make([]ui.Option, 0, len(registered))
	for _, id := range registered {
		opts = append(opts, ui.Option{Label: id, Value: id})
	}
	ids, err := ui.SelectSkills("Select skills to uninstall", opts)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		ui.Warnf("nothing selected; no skills uninstalled")
		return nil, nil
	}
	return ids, nil
}

// uninstallOne removes a single skill: it removes its Global install (agent
// links and the ~/.agents/skills copy) and its Local installs (agent links,
// canonical copy, and skills-lock.json entry) from every recorded project,
// unlinks it from every tracked local folder, and drops the registry entry from
// st (in memory — the caller persists). Those canonical copies are the skill's
// only copies; there is no separate Home library to delete. linker.Unlink is
// idempotent for absent links and refuses to touch foreign symlinks or real
// files; under --force such refusals are downgraded to warnings so the entry can
// still be dropped (the foreign entry stays put).
func uninstallOne(home string, agents []agentdir.Agent, st *state.State, id, cwd string) error {
	// Delete the canonical copies FIRST — the Global one, then the committed
	// Local ones in every recorded project — so a later symlink sweep over the
	// same place sees an empty slot rather than refusing on a real directory.
	// vendorRemove also clears each scope's agent links, and only deletes a
	// directory the registry records as skillm's own copy. The Local removals
	// edit the user's git working tree; the batch confirmation already named
	// those directories. A missing copy (project moved/deleted) is silently
	// skipped.
	removedGlobal, err := vendorRemove(home, id, agents, agentdir.Global, cwd, st.IsGlobal(id), agentdir.Global.String())
	if err != nil {
		if flagForce {
			ui.Warnf("%v", err)
		} else {
			return err
		}
	}
	if removedGlobal {
		ui.Successf("deleted copy of %s in %s (global)", id, canonicalDisplay(agentdir.Global))
	}

	for _, dir := range st.VendoredRoots(id) {
		localAgents, _ := splitLocalAliased(agents, dir)
		removed, err := vendorRemove(home, id, localAgents, agentdir.Local, dir, true, scopeLabel(agentdir.Local, dir, cwd))
		if err != nil {
			if flagForce {
				ui.Warnf("%v", err)
			} else {
				return err
			}
		}
		if removed {
			ui.Successf("deleted copy of %s in %s (%s)", id, agentdir.CanonicalLocalRel, scopeLabel(agentdir.Local, dir, cwd))
		}
		removeLockEntry(id, dir)
	}

	// Sweep tracked local roots AND vendored roots for stray symlinks: a
	// vendored root may also hold one, and need not be in LocalRoots.
	sweepDirs := append(append([]string{}, st.LocalRoots...), st.VendoredRoots(id)...)
	for _, dir := range localScanDirs(sweepDirs, cwd) {
		// Skip a local dir where every agent's local folder is its global one
		// (e.g. home): the global pass above already removed those links, so a
		// local pass would only repeat the work and double-report it.
		real, _ := splitLocalAliased(agents, dir)
		if len(real) == 0 {
			continue
		}
		res, err := linker.Unlink(home, id, real, agentdir.Local, dir)
		if err != nil {
			if flagForce {
				ui.Warnf("%v", err)
			} else {
				return err
			}
		}
		for _, ar := range res.Agents {
			if ar.Action == linker.ActionRemoved {
				ui.Successf("unlinked %s from %s (%s)", id, ar.Agent.Name, scopeLabel(agentdir.Local, dir, cwd))
			}
		}
	}

	// There is no Home copy to delete — the canonical install copies removed
	// above were the only ones. Drop the registry entry so, per the model, an
	// entry exists only while the skill is installed somewhere.
	st.Remove(id) // drops the entry, including its VendoredAt/Global records
	return nil
}

// vendoredDirsForIDs returns the sorted, de-duplicated set of project roots
// where any of the named skills has a Vendored copy — the directories an
// uninstall will delete committed files from, named in the confirmation.
func vendoredDirsForIDs(st *state.State, ids []string) []string {
	seen := make(map[string]bool)
	var dirs []string
	for _, id := range ids {
		for _, d := range st.VendoredRoots(id) {
			if !seen[d] {
				seen[d] = true
				dirs = append(dirs, d)
			}
		}
	}
	sort.Strings(dirs)
	return dirs
}

// confirmUninstallPrompt builds the single confirmation shown before a batch
// uninstall, naming the skills so the user sees exactly what will be removed and
// warning, when applicable, that committed copies in named projects are deleted.
func confirmUninstallPrompt(ids, vendoredDirs []string) string {
	var head string
	if len(ids) == 1 {
		head = fmt.Sprintf("Remove skill %q from Home and unlink it from all agents?", ids[0])
	} else {
		head = fmt.Sprintf("Remove %d skills (%s) from Home and unlink them from all agents?",
			len(ids), strings.Join(ids, ", "))
	}
	if len(vendoredDirs) > 0 {
		head += fmt.Sprintf("\nThis also DELETES committed copies in: %s", strings.Join(vendoredDirs, ", "))
	}
	return head
}
