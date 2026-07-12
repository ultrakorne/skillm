package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ultrakorne/skillm/internal/agentdir"
	"github.com/ultrakorne/skillm/internal/config"
	"github.com/ultrakorne/skillm/internal/linker"
	"github.com/ultrakorne/skillm/internal/state"
	"github.com/ultrakorne/skillm/internal/store"
	"github.com/ultrakorne/skillm/internal/ui"
)

// Codex never adopted .codex/skills — it reads the cross-agent .agents/skills
// convention instead (as do Cursor, Amp, Gemini CLI and others). Older skillm
// versions seeded config.toml with the dead pair below under the name
// "codex", and config treats an existing file as authoritative, so users who
// installed back then keep linking skills into folders no agent reads.
// migrateDeadAgentDirs detects that seeded pair and, with the user's consent,
// rewrites the config and relocates what is already on disk.
//
// The seeded entry is also renamed "codex" -> "agents" along the way: the
// entry serves every .agents-native agent, and keeping the old name would
// make `skillm agent` suggest that disabling it only affects Codex.
const (
	deadCodexGlobal = "~/.codex/skills"
	deadCodexLocal  = ".codex/skills"
	agentsGlobal    = "~/.agents/skills"
	agentsLocal     = ".agents/skills"
)

// migrateDeadAgentDirs runs before every command (from the root command's
// PersistentPreRunE). The no-migration paths are deliberately cheap — one
// stat for fresh installs, one config parse otherwise — because this cost is
// paid on every invocation.
func migrateDeadAgentDirs() error {
	home, err := store.Home(flagHome)
	if err != nil {
		return err
	}
	// A fresh install has no config.toml yet and gets the corrected defaults
	// straight from Default()/EnsureExists — only an existing file can carry
	// the seeded shapes below, so absence means there is nothing to migrate.
	if _, err := os.Stat(config.Path(home)); err != nil {
		return nil
	}
	cfg, err := config.Load(home)
	if err != nil {
		return err
	}

	// Only an agent whose locations BOTH equal the seeded dead pair is
	// migrated. Anything else — even one half of the pair changed — is the
	// user's deliberate choice and must never be rewritten.
	var deadMatched []string
	for name, def := range cfg.Agents {
		if def.Global == deadCodexGlobal && def.Local == deadCodexLocal {
			deadMatched = append(deadMatched, name)
		}
	}
	sort.Strings(deadMatched)

	// The rename applies only to the exact seeded key "codex" carrying either
	// seeded pair: the dead one (fixed below anyway) or the corrected one (a
	// config seeded in the brief window before the entry was renamed). Any
	// other name or paths is user-authored and keeps its identity.
	codexDef, hasCodex := cfg.Agents["codex"]
	_, hasAgents := cfg.Agents["agents"]
	renameDue := hasCodex &&
		((codexDef.Global == deadCodexGlobal && codexDef.Local == deadCodexLocal) ||
			(codexDef.Global == agentsGlobal && codexDef.Local == agentsLocal))

	if len(deadMatched) == 0 && !renameDue {
		return nil
	}

	// A user-defined "agents" entry blocks the rename: neither definition may
	// be overwritten or dropped, so the "codex" entry keeps its name (its
	// paths are still fixed when they are the dead pair). When the blocked
	// rename was the only pending work, stop here — prompting for a change
	// that cannot be applied would only nag.
	if renameDue && hasAgents {
		ui.Warnf("cannot rename agent \"codex\" to \"agents\": an \"agents\" entry already exists in config.toml — resolve the name collision by hand")
		renameDue = false
		if len(deadMatched) == 0 {
			return nil
		}
	}

	names := strings.Join(deadMatched, ", ")

	// One consent gate covers everything this migration will do, so the texts
	// must describe the full change: path fix, rename, or both.
	var hint, prompt, declined string
	switch {
	case len(deadMatched) > 0 && renameDue:
		hint = fmt.Sprintf("agent %s points at %s, which Codex no longer reads (it reads %s)", names, deadCodexGlobal, agentsGlobal)
		prompt = fmt.Sprintf(
			"Agent %q points at %s, which Codex no longer reads (it reads %s). Update config — renaming \"codex\" to \"agents\", since that folder serves every .agents-native agent — and move existing links there?",
			names, deadCodexGlobal, agentsGlobal)
		declined = fmt.Sprintf("skipped migration: skills linked for %s stay in %s, which Codex does not read", names, deadCodexGlobal)
	case len(deadMatched) > 0:
		hint = fmt.Sprintf("agent %s points at %s, which Codex no longer reads (it reads %s)", names, deadCodexGlobal, agentsGlobal)
		prompt = fmt.Sprintf(
			"Agent %q points at %s, which Codex no longer reads (it reads %s). Update config and move existing links there?",
			names, deadCodexGlobal, agentsGlobal)
		declined = fmt.Sprintf("skipped migration: skills linked for %s stay in %s, which Codex does not read", names, deadCodexGlobal)
	default: // rename only: the paths already point at the shared folder
		hint = fmt.Sprintf("agent \"codex\" is the seeded %s entry, which serves every .agents-native agent (Cursor, Amp, Gemini CLI, …) and is now named \"agents\"", agentsGlobal)
		prompt = fmt.Sprintf(
			"Agent \"codex\" is the seeded %s entry, which serves every .agents-native agent (Cursor, Amp, Gemini CLI, …), not just Codex. Rename it to \"agents\" in config?",
			agentsGlobal)
		declined = "skipped rename: the seeded entry stays named \"codex\""
	}

	if !flagYes && !flagForce {
		if !ui.IsTTY() {
			// Never block a script on a prompt: nag once and let the command
			// proceed against the stale config.
			ui.Warnf("%s — run any skillm command with --yes to migrate", hint)
			return nil
		}
		ok, err := ui.Confirm(prompt)
		if err != nil {
			return err
		}
		if !ok {
			ui.Warnf("%s", declined)
			return nil
		}
	}

	// Persist the corrected config first — the durable intent — then
	// reconcile disk best-effort: a blocked path is skipped with a warning,
	// never aborting the sweep. Rewriting the config is also what makes the
	// migration idempotent: once saved, nothing matches the seeded shapes.
	for _, name := range deadMatched {
		def := cfg.Agents[name]
		def.Global = agentsGlobal
		def.Local = agentsLocal
		cfg.Agents[name] = def
	}
	if renameDue {
		// Move the definition wholesale (paths already corrected above when
		// they were the dead pair) so the enabled flag travels with it.
		def := cfg.Agents["codex"]
		delete(cfg.Agents, "codex")
		cfg.Agents["agents"] = def
	}
	if err := config.Save(home, cfg); err != nil {
		return err
	}

	if len(deadMatched) == 0 {
		// Rename-only: the locations are identical, so every link and copy
		// already lives in the right place — config was the whole fix.
		ui.Successf("renamed agent \"codex\" to \"agents\" (its %s locations are unchanged)", agentsGlobal)
		return nil
	}

	// Load what the relocation needs. The config is already corrected, so a
	// failure here surfaces rather than silently leaving links stranded in
	// the dead folders with nothing left to detect them.
	st, err := state.Load(home)
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("determine current directory: %w", err)
	}

	links := 0
	// Local bases whose old folder lost entries and may now be empty; the
	// cleanup pass below only considers these, so untouched directories (like
	// an unrelated cwd) are never poked.
	touched := map[string]bool{}

	for _, name := range deadMatched {
		oldA := agentdir.Agent{Name: name, Global: deadCodexGlobal, Local: deadCodexLocal}
		newA := agentdir.Agent{Name: name, Global: agentsGlobal, Local: agentsLocal}

		links += relocateLinks(home, oldA, newA, agentdir.Global, cwd, cwd)
		// Local links are NOT relocated: local installs are no longer symlinks
		// into Home but committed copies in .agents/skills, so an old link has
		// no equivalent to move to. Point at the reinstall instead; the dead
		// folder is unread either way.
		for _, base := range localScanDirs(st.LocalRoots, cwd) {
			if agentdir.LocalAliasesGlobal(oldA, base) {
				continue
			}
			if infos, err := linker.ScanAll(home, []agentdir.Agent{oldA}, agentdir.Local, base); err == nil && len(infos) > 0 {
				ui.Warnf("%s holds %d old skillm link%s no agent reads; run `skillm install --local` in %s to reinstall those skills, then delete the folder",
					filepath.Join(base, deadCodexLocal), len(infos), plural(len(infos)), base)
			}
		}
	}

	// Vendored copies are real directories recorded per skill in state. The
	// dead pair is identical for every matched agent, so one sweep over the
	// recorded roots moves each copy exactly once.
	oldTpl := agentdir.Agent{Name: deadMatched[0], Global: deadCodexGlobal, Local: deadCodexLocal}
	newTpl := agentdir.Agent{Name: deadMatched[0], Global: agentsGlobal, Local: agentsLocal}
	copies := 0
	for _, e := range st.Skills {
		for _, root := range e.VendoredAt {
			oldPath, ok := agentdir.LinkPath(oldTpl, agentdir.Local, root, e.ID)
			if !ok {
				continue
			}
			// Only a real directory is a Vendored copy; symlinks were handled
			// by the link passes and anything else is not skillm's to move.
			if kind, _, err := linker.Classify(home, oldPath); err != nil || kind != linker.TargetDir {
				continue
			}
			newPath, _ := agentdir.LinkPath(newTpl, agentdir.Local, root, e.ID)
			if kind, _, err := linker.Classify(home, newPath); err != nil || kind != linker.TargetAbsent {
				ui.Warnf("cannot move vendored copy %s: %s already exists", oldPath, newPath)
				continue
			}
			if err := os.MkdirAll(filepath.Dir(newPath), 0o755); err != nil {
				ui.Warnf("move vendored copy %s: %v", oldPath, err)
				continue
			}
			if err := os.Rename(oldPath, newPath); err != nil {
				ui.Warnf("move vendored copy %s: %v", oldPath, err)
				continue
			}
			// The copy now sits at the canonical local location; give it the
			// lockfile entry a fresh local install would have written.
			upsertLockEntry(e, root)
			copies++
			touched[root] = true
		}
	}

	// The old folders were created by skillm; once emptied by the moves they
	// are clutter, so try removing them. os.Remove refuses a non-empty
	// directory, which is exactly the desired "leave anything foreign alone".
	if folder, ok := agentdir.SkillsFolder(oldTpl, agentdir.Global, ""); ok {
		_ = os.Remove(folder)
	}
	for base := range touched {
		if folder, ok := agentdir.SkillsFolder(oldTpl, agentdir.Local, base); ok {
			_ = os.Remove(folder)
		}
	}

	renamed := ""
	if renameDue {
		renamed = " (renamed \"codex\" to \"agents\")"
	}
	copyWord := "copies"
	if copies == 1 {
		copyWord = "copy"
	}
	ui.Successf("migrated %s to %s%s: moved %d link%s and %d vendored %s", names, agentsGlobal, renamed, links, plural(links), copies, copyWord)
	return nil
}

// relocateLinks moves every skillm-managed link oldA holds at (scope, base)
// into newA's folder there. Only called at Global scope — local installs are
// copies now and are handled separately. The new link is created BEFORE the
// old one is removed, so a failure can at worst leave a skill linked twice —
// never stranded with no link at all. Blocked spots are skipped with a warning
// so one obstruction does not abort the sweep. It returns how many links now
// exist at the new location.
func relocateLinks(home string, oldA, newA agentdir.Agent, scope agentdir.Scope, base, cwd string) int {
	infos, err := linker.ScanAll(home, []agentdir.Agent{oldA}, scope, base)
	if err != nil {
		ui.Warnf("scan %s (%s): %v", oldA.Name, scopeLabel(scope, base, cwd), err)
		return 0
	}
	moved := 0
	for _, li := range infos {
		if _, err := linker.Link(home, li.ID, []agentdir.Agent{newA}, scope, base); err != nil {
			ui.Warnf("%v", err)
			continue // the old link stays: dead folder, but recoverable by re-running
		}
		moved++
		if _, err := linker.Unlink(home, li.ID, []agentdir.Agent{oldA}, scope, base); err != nil {
			ui.Warnf("%v", err)
		}
	}
	return moved
}
