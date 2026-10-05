package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ultrakorne/skillm/internal/agentdir"
	"github.com/ultrakorne/skillm/internal/config"
	"github.com/ultrakorne/skillm/internal/core"
	"github.com/ultrakorne/skillm/internal/protocol"
	"github.com/ultrakorne/skillm/internal/state"
	"github.com/ultrakorne/skillm/internal/ui"
)

var (
	uninstallFlagAll bool
	// uninstallFlagConfirmedRoots backs --confirmed-root: the projects whose
	// committed copies the caller's own confirmation named.
	uninstallFlagConfirmedRoots []string
)

func init() {
	rootCmd.AddCommand(newUninstallCmd())
}

func newUninstallCmd() *cobra.Command {
	var global, local bool
	var project string
	c := &cobra.Command{
		Use:   "uninstall [skill_id...]",
		Short: "Remove skills everywhere or from one scope or project",
		Long: "uninstall removes skills' selected installs. Without a target flag it removes their Global " +
			"install (the ~/.agents/skills copy and every agent's symlink) and its Local " +
			"installs in every recorded project (copy, links, and skills-lock.json entry, " +
			"tracked in state.toml) — the only copies there are — then drops its registry " +
			"entry when no installs remain. By default it clears every reference. " +
			"Use --local to remove only this project's install, --project <dir> for " +
			"another project, or --global to remove only the global install. " +
			"Pass one or more skill ids, --all to remove " +
			"every installed skill, or no arguments to pick interactively. On a terminal it " +
			"confirms first unless --yes or --force is given.\n\n" +
			"A caller that asked its own question passes each project it named with " +
			"--confirmed-root <dir> (or --confirmed-root= when it named none): if the " +
			"skills now have committed copies in another project, nothing is removed " +
			"and the uninstall fails with the new list (code needs_confirm in JSON). " +
			"With --json it never prompts: pass skill ids (or --all) and --yes. An id " +
			"that is not installed (any more) is skipped with a warning rather than " +
			"failing the batch, so an uninstall that stopped part-way (needs_force, " +
			"uninstall_failed, cancelled) can be retried with the same ids.",
		Args:        cobra.ArbitraryArgs,
		Annotations: map[string]string{annotationJSON: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			req := core.UninstallRequest{CheckRoots: cmd.Flags().Changed("confirmed-root")}
			if global {
				scope := agentdir.Global
				req.Scope = &scope
			} else if local || cmd.Flags().Changed("project") {
				opts, err := coreOptions(local || !filepath.IsAbs(project))
				if err != nil {
					return err
				}
				dir := project
				if local {
					dir = opts.Cwd
				}
				base, err := projectDir(dir, opts.Cwd)
				if err != nil {
					return err
				}
				scope := agentdir.Local
				req.Scope, req.Base = &scope, base
			}
			for _, r := range uninstallFlagConfirmedRoots {
				if r != "" {
					req.ConfirmedRoots = append(req.ConfirmedRoots, r)
				}
			}
			return runUninstall(cmd.Context(), args, uninstallFlagAll, req)
		},
	}
	c.Flags().BoolVar(&uninstallFlagAll, "all", false, "remove every skill in the selected scope (everywhere by default)")
	c.Flags().BoolVar(&global, "global", false, "remove only global installs")
	c.Flags().BoolVar(&local, "local", false, "remove only installs in the current project")
	c.Flags().StringVar(&project, "project", "", "remove only installs in this existing project directory")
	c.MarkFlagsMutuallyExclusive("global", "local", "project")
	c.Flags().StringArrayVar(&uninstallFlagConfirmedRoots, "confirmed-root", nil,
		"a project whose committed copies the caller confirmed deleting (repeat it; an empty value confirms none)")
	return c
}

// runUninstall uninstalls the skills args names (or all of them). confirmed
// carries --confirmed-root (CheckRoots and ConfirmedRoots); its IDs are
// ignored.
func runUninstall(ctx context.Context, args []string, all bool, confirmed core.UninstallRequest) error {
	if flagJSON {
		// JSON mode never prompts: the caller asks its own question.
		if !flagYes {
			return usageError("uninstall --json needs --yes (confirm with the user first)")
		}
		if len(args) == 0 && !all {
			return usageError("uninstall --json needs skill ids or --all")
		}
	}
	// Scoped requests use cwd for discovery and labels, without requiring it
	// to exist. They only remove entries in the explicitly selected target.
	opts, err := coreOptions(confirmed.Scope == nil)
	if err != nil {
		return err
	}
	if confirmed.Scope != nil {
		opts.Cwd, _ = os.Getwd()
	}
	// The picker and the confirmation run without Home's lock, so an open
	// prompt never blocks another skillm process; core.Uninstall re-reads the
	// Registry under the lock and refuses a skill that is gone by then. A
	// broken config.toml is still reported before any question.
	cfg, err := config.Load(opts.Home)
	if err != nil {
		return err
	}
	st, err := state.Load(opts.Home)
	if err != nil {
		return err
	}

	ids, err := selectUninstallIDs(opts, cfg.AllAgents(), st, args, all, confirmed)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		if flagJSON {
			return jsonOut().Result(protocol.NewUninstallData(core.UninstallResult{}))
		}
		return nil // selectUninstallIDs already reported why (empty Home / nothing picked)
	}

	// One confirmation covers the whole batch. As with the rest of skillm, the
	// prompt only appears on a TTY; a non-interactive run proceeds (pass --yes
	// to be explicit), so scripts are not blocked. The prompt names any project
	// where committed copies will be deleted, since that edits the user's repo,
	// and core holds the uninstall to exactly those projects: if another
	// process added one while the question was open, the lock is released and
	// the question asked again with the new list.
	req := confirmed
	req.IDs = ids
	req.SkipMissing = flagJSON
	ask := !flagJSON && ui.IsTTY() && !opts.Yes && !opts.Force
	roots := req.Roots(st)
	for {
		if ask {
			prompt := confirmUninstallPrompt(ids, roots)
			if req.Scope != nil {
				prompt = fmt.Sprintf("Remove %s from %s?", strings.Join(ids, ", "), scopeLabel(*req.Scope, req.Base, opts.Cwd))
				if len(roots) > 0 {
					prompt += "\nThis DELETES committed copies in: " + strings.Join(roots, ", ")
				}
			}
			ok, err := ui.Confirm(prompt)
			if err != nil {
				return err
			}
			if !ok {
				ui.Warnf("aborted; nothing was removed")
				return nil
			}
			req.CheckRoots, req.ConfirmedRoots = true, roots
		}
		if flagJSON {
			return uninstallJSON(ctx, opts, req)
		}
		res, err := uninstallLocked(ctx, opts, termLog, req)
		var changed *core.UninstallScopeChangedError
		if ask && errors.As(err, &changed) {
			roots = changed.Roots
			continue
		}
		if errors.Is(err, context.Canceled) {
			return fmt.Errorf("uninstall interrupted; %d of %d skills removed", len(res.Skills), len(ids))
		}
		return err
	}
}

// uninstallJSON is `uninstall --json --yes`: the uninstall with no question.
// A changed set of projects fails with code needs_confirm and the new list; a
// cancelled run fails with code cancelled, naming how many skills were
// removed before it stopped. The ids already gone are skipped with a warning
// (req.SkipMissing), so every failure is retried with the same ids.
func uninstallJSON(ctx context.Context, opts core.Options, req core.UninstallRequest) error {
	out := jsonOut()
	res, err := uninstallLocked(ctx, opts, out, req)
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("uninstall interrupted; %d of %d skills removed: %w", len(res.Skills), len(req.IDs), err)
	}
	if err != nil {
		return err
	}
	return out.Result(protocol.NewUninstallData(res))
}

// uninstallLocked runs core.Uninstall under Home's lock.
func uninstallLocked(ctx context.Context, opts core.Options, rep core.Reporter, req core.UninstallRequest) (core.UninstallResult, error) {
	unlock, err := lockHome(ctx, opts.Home, "skillm uninstall")
	if err != nil {
		return core.UninstallResult{}, err
	}
	defer unlock()
	return core.Uninstall(ctx, opts, rep, req)
}

// selectUninstallIDs resolves which skills `uninstall` should act on. Explicit
// ids must each be known to skillm (present in the registry); any unknown id
// is an atomic error so a typo removes nothing (in JSON mode they are passed
// through, and core skips the unknown ones with a warning). --all targets every
// registered skill; with no arguments an interactive multiselect is shown (which
// refuses on a non-TTY). It returns an empty slice and no error when there is
// nothing to do, having already told the user why.
func selectUninstallIDs(opts core.Options, agents []agentdir.Agent, st *state.State, args []string, all bool, req core.UninstallRequest) ([]string, error) {
	if len(args) > 0 {
		if all {
			return nil, errors.New("pass either skill ids or --all, not both")
		}
		// JSON mode skips the ids already gone (core warns about each), so
		// a GUI can retry an uninstall that stopped part-way with the same
		// ids.
		if flagJSON {
			return args, nil
		}
		var missing []string
		for _, id := range args {
			included, err := req.Includes(opts, st, agents, id)
			if err != nil {
				return nil, err
			}
			if !included {
				missing = append(missing, id)
			}
		}
		if len(missing) > 0 {
			return nil, &core.NotInstalledError{IDs: missing}
		}
		return args, nil
	}

	var registered []string
	for _, id := range registeredIDs(st) {
		included, err := req.Includes(opts, st, agents, id)
		if err != nil {
			return nil, err
		}
		if included {
			registered = append(registered, id)
		}
	}
	if len(registered) == 0 {
		if !flagJSON {
			ui.Warnf("no skills in the requested scope; nothing to uninstall")
		}
		return nil, nil
	}
	if all {
		return registered, nil
	}

	choices := make([]ui.Option, 0, len(registered))
	for _, id := range registered {
		choices = append(choices, ui.Option{Label: id, Value: id})
	}
	ids, err := ui.SelectSkills("Select skills to uninstall", choices)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		ui.Warnf("nothing selected; no skills uninstalled")
		return nil, nil
	}
	return ids, nil
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
