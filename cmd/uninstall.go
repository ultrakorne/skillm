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
		Long: "uninstall removes skills entirely. For each skill it first removes the symlink " +
			"from every agent at the global scope and in every local folder skillm linked it " +
			"into (tracked in state.toml), then deletes the Home copy and its registry entry " +
			"so no dangling symlinks are left behind. There is no per-scope uninstall — it " +
			"always clears every reference. Pass one or more skill ids, --all to remove every " +
			"skill in Home, or no arguments to pick interactively from the skills in Home. On " +
			"a terminal it confirms first unless --yes or --force is given.",
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
			if _, inRegistry := st.Get(id); !inRegistry && !store.Exists(home, id) {
				missing = append(missing, id)
			}
		}
		if len(missing) > 0 {
			return nil, fmt.Errorf("not in Home: %s; nothing to uninstall", strings.Join(missing, ", "))
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

// uninstallOne removes a single skill: it unlinks the skill from every supplied
// agent at the global scope and in every tracked local folder, deletes any
// Vendored copies it has in tracked projects, deletes the Home copy, and drops
// the registry entry from st (in memory — the caller persists). linker.Unlink is
// idempotent for absent links and refuses to touch foreign symlinks or real
// files; under --force such refusals are downgraded to warnings so the Home copy
// can still be deleted (the foreign entry stays put).
func uninstallOne(home string, agents []agentdir.Agent, st *state.State, id, cwd string) error {
	// Delete the committed Vendored copies in every recorded project FIRST, so a
	// later symlink sweep over the same root sees an empty slot rather than
	// refusing on a real directory. This edits the user's git working tree; the
	// batch confirmation already named these directories. A missing copy (project
	// moved/deleted) is silently skipped.
	for _, dir := range st.VendoredRoots(id) {
		removed, err := vendorRemove(home, id, agents, dir)
		if err != nil {
			if flagForce {
				ui.Warnf("%v", err)
			} else {
				return err
			}
		}
		for _, a := range removed {
			ui.Successf("deleted copy of %s for %s (%s)", id, a.Name, scopeLabel(agentdir.Local, dir, cwd))
		}
	}

	type target struct {
		scope  agentdir.Scope
		agents []agentdir.Agent
		dir    string
	}
	targets := []target{{agentdir.Global, agents, cwd}}
	// Sweep tracked local roots AND vendored roots for symlinks: a vendored root
	// may also hold a stray symlink, and need not be in LocalRoots.
	sweepDirs := append(append([]string{}, st.LocalRoots...), st.VendoredRoots(id)...)
	for _, dir := range localScanDirs(sweepDirs, cwd) {
		// Skip a local dir where every agent's local folder is its global one
		// (e.g. home): the global pass above already removed those links, so a
		// local pass would only repeat the work and double-report it.
		real, _ := splitLocalAliased(agents, dir)
		if len(real) == 0 {
			continue
		}
		targets = append(targets, target{agentdir.Local, real, dir})
	}

	for _, tg := range targets {
		res, err := linker.Unlink(home, id, tg.agents, tg.scope, tg.dir)
		if err != nil {
			if flagForce {
				ui.Warnf("%v", err)
			} else {
				return err
			}
		}
		for _, ar := range res.Agents {
			if ar.Action == linker.ActionRemoved {
				ui.Successf("unlinked %s from %s (%s)", id, ar.Agent.Name, scopeLabel(tg.scope, tg.dir, cwd))
			}
		}
	}

	if err := store.RemoveSkillDir(home, id); err != nil {
		return err
	}
	st.Remove(id) // drops the entry, including its VendoredAt record
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
