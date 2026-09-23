package cmd

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ultrakorne/skillm/internal/config"
	"github.com/ultrakorne/skillm/internal/core"
	"github.com/ultrakorne/skillm/internal/state"
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
			return runUninstall(cmd.Context(), args, uninstallFlagAll)
		},
	}
	c.Flags().BoolVar(&uninstallFlagAll, "all", false, "remove every skill in Home")
	return c
}

func runUninstall(ctx context.Context, args []string, all bool) error {
	opts, err := coreOptions(true)
	if err != nil {
		return err
	}
	// The picker and the confirmation run without Home's lock, so an open
	// prompt never blocks another skillm process; core.Uninstall re-reads the
	// Registry under the lock and refuses a skill that is gone by then. A
	// broken config.toml is still reported before any question.
	if _, err := config.Load(opts.Home); err != nil {
		return err
	}
	st, err := state.Load(opts.Home)
	if err != nil {
		return err
	}

	ids, err := selectUninstallIDs(st, args, all)
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
	if ui.IsTTY() && !opts.Yes && !opts.Force {
		ok, err := ui.Confirm(confirmUninstallPrompt(ids, vendoredDirsForIDs(st, ids)))
		if err != nil {
			return err
		}
		if !ok {
			ui.Warnf("aborted; nothing was removed")
			return nil
		}
	}

	unlock, err := lockHome(ctx, opts.Home, "skillm uninstall")
	if err != nil {
		return err
	}
	defer unlock()
	_, err = core.Uninstall(ctx, opts, termLog, ids)
	return err
}

// selectUninstallIDs resolves which skills `uninstall` should act on. Explicit
// ids must each be known to skillm (present in the registry); any unknown id
// is an atomic error so a typo removes nothing. --all targets every
// registered skill; with no arguments an interactive multiselect is shown (which
// refuses on a non-TTY). It returns an empty slice and no error when there is
// nothing to do, having already told the user why.
func selectUninstallIDs(st *state.State, args []string, all bool) ([]string, error) {
	if len(args) > 0 {
		if all {
			return nil, errors.New("pass either skill ids or --all, not both")
		}
		if err := core.CheckInstalled(st, args); err != nil {
			return nil, err
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
