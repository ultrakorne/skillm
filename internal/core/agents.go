package core

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/ultrakorne/skillm/internal/agentdir"
	"github.com/ultrakorne/skillm/internal/config"
	"github.com/ultrakorne/skillm/internal/linker"
	"github.com/ultrakorne/skillm/internal/state"
)

// Event codes SetAgents reports with, beyond the vendor primitives' own
// (link_failed, unlink_refused, unlink_failed).
const (
	// CodeAgentEnabled: an agent was enabled and linked to Skills.
	CodeAgentEnabled = "agent_enabled"
	// CodeAgentEnabledEmpty: an agent was enabled, but no skill is installed
	// anywhere it could be linked to yet.
	CodeAgentEnabledEmpty = "agent_enabled_empty"
	// CodeAgentDisabled: an agent was disabled and its links removed.
	CodeAgentDisabled = "agent_disabled"
	// CodeLinkSkipped: an entry skillm did not create sits where a newly
	// enabled agent's link would go; it is left alone and the link skipped.
	CodeLinkSkipped = "link_skipped"
	// CodeScanFailed: an agent's skill folder could not be read, so its links
	// there were not removed.
	CodeScanFailed = "scan_failed"
	// CodeCopiesKept (info): disabling the agent that reads the canonical
	// .agents/skills store left the skills' canonical copies in place.
	CodeCopiesKept = "copies_kept"
)

// AgentInfo is one agent defined in Config.
type AgentInfo struct {
	Name    string
	Enabled bool
	// Global and Local are the agent's skill-folder location templates, as
	// written in config.toml. Empty means the agent has no folder there.
	Global, Local string
}

// Agents returns every agent defined in Config, sorted by name, with its
// enabled state. It reads Config only and takes no lock.
func Agents(opts Options) ([]AgentInfo, error) {
	cfg, err := config.Load(opts.Home)
	if err != nil {
		return nil, err
	}
	enabled := cfg.EnabledNames()
	var out []AgentInfo
	for _, a := range cfg.AllAgents() {
		out = append(out, AgentInfo{
			Name:    a.Name,
			Enabled: slices.Contains(enabled, a.Name),
			Global:  a.Global,
			Local:   a.Local,
		})
	}
	return out, nil
}

// AgentChange is what SetAgents did for one agent whose enabled state changed.
type AgentChange struct {
	Name string
	// Enabled is the agent's new state: true when it was enabled, false when
	// it was disabled.
	Enabled bool
	// Skills are the sorted ids of the skills it was linked to (enabled) or
	// unlinked from (disabled).
	Skills []string
	// Places label where that happened: "global", "local" (Options.Cwd) or
	// "local: <root>".
	Places []string
	// Warnings are the spots the sweep skipped: a link that could not be
	// created (link_skipped for a foreign entry in the way, link_failed) or
	// removed (unlink_refused, unlink_failed), and an agent folder that could
	// not be read (scan_failed). Each was also reported as a warning event.
	Warnings []error
}

// AgentsResult is SetAgents' outcome.
type AgentsResult struct {
	// Enabled lists the enabled agents afterwards, sorted by name.
	Enabled []string
	// Changes has one entry per agent whose state changed: the enabled ones
	// first, then the disabled ones, each sorted by name. Empty means nothing
	// changed and nothing was written.
	Changes []AgentChange
}

// ErrNoAgentEnabled means a change would leave no agent enabled. Removing
// every link is not an agent change: uninstalling the skills is.
var ErrNoAgentEnabled = errors.New("at least one agent must stay enabled")

// UnknownAgentError means some agent names are not defined in Config.
type UnknownAgentError struct {
	Names []string
}

func (e *UnknownAgentError) Error() string {
	return fmt.Sprintf("not defined in config.toml: %s", strings.Join(e.Names, ", "))
}

// SetAgents enables the agents in enable and disables those in disable,
// reconciling their links immediately: a newly enabled agent is linked
// wherever the agents enabled before it are linked, and to every recorded
// install whose canonical copy exists; a newly disabled agent's links are
// removed from every Scope and tracked project. Canonical copies are never
// deleted. An agent already in the asked-for state is left alone (SetAgents
// toggles, it does not repair drift), so a call that changes nothing writes
// nothing.
//
// The new enabled flags are saved first, as the durable intent; the links
// are then reconciled best effort, and a blocked spot is reported as a
// warning event rather than aborting the sweep. Enables run before disables,
// so a swap lets the new agent copy the old one's links while they are
// still on disk.
//
// The caller holds Home's lock for the whole call and has already confirmed
// any disable with the user; SetAgents reads Config and the Registry itself.
// A name that is not defined is an *UnknownAgentError, a name in both lists
// an error, and a change that would leave no agent enabled ErrNoAgentEnabled,
// each before anything is written. Options.Cwd is scanned for links as well.
func SetAgents(ctx context.Context, opts Options, rep Reporter, enable, disable []string) (AgentsResult, error) {
	rep = nopIfNil(rep)
	var res AgentsResult
	cfg, err := config.Load(opts.Home)
	if err != nil {
		return res, err
	}

	defined := cfg.AgentNames()
	var unknown []string
	for _, n := range append(append([]string{}, enable...), disable...) {
		if !slices.Contains(defined, n) && !slices.Contains(unknown, n) {
			unknown = append(unknown, n)
		}
	}
	if len(unknown) > 0 {
		return res, &UnknownAgentError{Names: unknown}
	}
	for _, n := range enable {
		if slices.Contains(disable, n) {
			return res, fmt.Errorf("agent %q cannot be both enabled and disabled", n)
		}
	}

	// Snapshot the enabled set BEFORE the toggle. The enable pass mirrors the
	// links these agents currently hold, so we must capture them before any
	// change is applied (config or disk).
	beforeEnabled := cfg.EnabledAgents()
	before := make(map[string]bool)
	for _, n := range cfg.EnabledNames() {
		before[n] = true
	}
	after := make(map[string]bool, len(before))
	for n := range before {
		after[n] = true
	}
	for _, n := range enable {
		after[n] = true
	}
	for _, n := range disable {
		delete(after, n)
	}
	for _, n := range defined {
		if after[n] {
			res.Enabled = append(res.Enabled, n)
		}
	}

	// At least one agent must stay enabled. Deselecting everything would strip
	// every link, which is uninstall's job (it also deletes the canonical
	// copies), so it is refused. Nothing is written or unlinked.
	if len(res.Enabled) == 0 {
		return res, ErrNoAgentEnabled
	}

	// Partition the defined agents by how their enabled state is changing.
	// Unchanged agents are never touched.
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
		return res, nil
	}
	if err := ctx.Err(); err != nil {
		return res, err
	}

	// Load everything the reconcile needs before writing anything, so a load
	// failure aborts cleanly without a half-applied change.
	st, err := state.Load(opts.Home)
	if err != nil {
		return res, err
	}

	// Persist the new enabled flags — the durable intent. Disk is then reconciled
	// best-effort below: a blocked spot is skipped with a warning.
	cfg.SetEnabled(res.Enabled)
	if err := config.Save(opts.Home, cfg); err != nil {
		return res, err
	}

	// Enable pass before disable pass, so a one-shot swap (disable A, enable B)
	// lets B copy A's links while they are still on disk.
	stateChanged := false
	for _, a := range newlyEnabled {
		change, changed := enableAgent(rep, opts.Home, a, beforeEnabled, st, opts.Cwd)
		res.Changes = append(res.Changes, change)
		stateChanged = stateChanged || changed
	}
	for _, a := range newlyDisabled {
		res.Changes = append(res.Changes, disableAgent(rep, opts.Home, a, st, opts.Cwd))
	}

	// A project that lost its last skillm link (and holds no recorded copy) is
	// no longer worth tracking, and a recorded root whose copy vanished is no
	// longer current. Scan across all defined agents so a root kept alive by a
	// still-enabled agent survives.
	if reconcileLocalRoots(opts.Home, cfg.AllAgents(), st) {
		stateChanged = true
	}
	if reconcileVendoredRoots(opts.Home, st) {
		stateChanged = true
	}
	if stateChanged {
		if err := state.Save(opts.Home, st); err != nil {
			return res, err
		}
	}
	return res, nil
}

// enableAgent links one newly-enabled agent at every place the before-enabled
// agents are currently linked: the global folder and every tracked local root
// (plus cwd). The footprint is read live from disk, so it reflects exactly
// what the peer agents have. A spot blocked by a foreign file or symlink is
// skipped with a warning instead of aborting the sweep. It returns what it
// did, and true when it changed the tracked local roots in st.
func enableAgent(rep Reporter, home string, a agentdir.Agent, beforeEnabled []agentdir.Agent, st *state.State, cwd string) (AgentChange, bool) {
	rep = nopIfNil(rep)
	one := []agentdir.Agent{a}
	skills := map[string]bool{}
	stateChanged := false
	var places []string
	var warnings []error

	warn := func(id string, err error) {
		code := CodeLinkFailed
		if errors.Is(err, linker.ErrNotManaged) {
			code = CodeLinkSkipped
		}
		rep.Event(logEvent(LevelWarn, id, code, err.Error()))
		warnings = append(warnings, err)
	}

	linkAt := func(scope agentdir.Scope, base string) {
		// At local scope, ignore the footprint of any before-enabled peer whose
		// local folder aliases its global one at base (e.g. home): scanning it
		// would read that peer's GLOBAL links as a phantom local footprint and
		// mirror those global-only skills into the newly enabled agent. Global
		// scope needs no such filter — a global folder is always real.
		sources := beforeEnabled
		if scope == agentdir.Local {
			sources, _ = SplitLocalAliased(beforeEnabled, base)
		}
		got := false
		for _, id := range footprintIDs(home, sources, scope, base) {
			// A link points at the scope's canonical copy; without one (a
			// legacy install whose peers still hold old Home links) a new link
			// would dangle — skip it.
			if !CopyExists(home, id, scope, base) {
				continue
			}
			res, err := linker.Link(home, id, one, scope, base)
			if err != nil {
				warn(id, err)
			}
			for _, ar := range res.Agents {
				if ar.Action == linker.ActionCreated || ar.Action == linker.ActionAlreadyLinked {
					skills[id] = true
					got = true
				}
			}
		}
		if got {
			places = append(places, ScopeLabel(scope, base, cwd))
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

	// Link the recorded installs too — the Global install and every root where
	// a skill's canonical copy exists — so the newly-enabled agent gets its
	// link even where no peer holds one (a canonical-folder agent is served by
	// the copy itself and needs nothing). A foreign entry at the agent's own
	// link path is warned about, never clobbered.
	linkRecorded := func(id string, scope agentdir.Scope, base string) {
		if !CopyExists(home, id, scope, base) {
			return // copy vanished; nothing to link to
		}
		res, lerr := linker.Link(home, id, one, scope, base)
		if lerr != nil {
			warn(id, lerr)
		}
		for _, ar := range res.Agents {
			if ar.Action == linker.ActionCreated || ar.Action == linker.ActionAlreadyLinked {
				skills[id] = true
				places = append(places, ScopeLabel(scope, base, cwd))
			}
		}
	}
	if a.Supports(agentdir.Global) && !agentdir.IsCanonicalGlobal(a) {
		for _, e := range st.Skills {
			if e.Global {
				linkRecorded(e.ID, agentdir.Global, cwd)
			}
		}
	}
	if a.Supports(agentdir.Local) && !agentdir.IsCanonicalLocal(a) {
		for _, e := range st.Skills {
			for _, root := range e.VendoredAt {
				if agentdir.LocalAliasesGlobal(a, root) {
					continue
				}
				linkRecorded(e.ID, agentdir.Local, root)
			}
		}
	}

	change := AgentChange{Name: a.Name, Enabled: true, Skills: sortedKeys(skills), Places: dedupe(places), Warnings: warnings}
	if len(skills) == 0 {
		rep.Event(Event{Type: EventLog, Level: LevelSuccess, Code: CodeAgentEnabledEmpty,
			Text: fmt.Sprintf("enabled %s — nothing to install yet", a.Name)})
		return change, stateChanged
	}
	rep.Event(Event{Type: EventLog, Level: LevelSuccess, Code: CodeAgentEnabled,
		Text: fmt.Sprintf("enabled %s — installed %d skill%s (%s)", a.Name, len(skills), plural(len(skills)), strings.Join(change.Places, ", "))})
	return change, stateChanged
}

// disableAgent removes every skillm-managed link one newly-disabled agent
// holds, across the global folder and every tracked local root (plus cwd). It
// scans for the agent's own links and unlinks each; foreign files and
// symlinks are never touched. A refusal is reported as a warning so one
// obstruction never aborts the sweep. The canonical copies are left intact —
// disabling an agent is not uninstalling a skill.
func disableAgent(rep Reporter, home string, a agentdir.Agent, st *state.State, cwd string) AgentChange {
	rep = nopIfNil(rep)
	one := []agentdir.Agent{a}
	skills := map[string]bool{}
	var places []string
	var warnings []error

	unlinkAt := func(scope agentdir.Scope, base string) {
		infos, err := linker.ScanAll(home, one, scope, base)
		if err != nil {
			err = fmt.Errorf("scan %s (%s): %w", a.Name, ScopeLabel(scope, base, cwd), err)
			rep.Event(Event{Type: EventLog, Level: LevelWarn, Code: CodeScanFailed, Text: err.Error()})
			warnings = append(warnings, err)
			return
		}
		got := false
		for _, li := range infos {
			res, err := linker.Unlink(home, li.ID, one, scope, base)
			if err != nil {
				code := CodeUnlinkFailed
				if errors.Is(err, linker.ErrNotManaged) {
					code = CodeUnlinkRefused
				}
				rep.Event(logEvent(LevelWarn, li.ID, code, err.Error()))
				warnings = append(warnings, err)
			}
			for _, ar := range res.Agents {
				if ar.Action == linker.ActionRemoved {
					skills[li.ID] = true
					got = true
				}
			}
		}
		if got {
			places = append(places, ScopeLabel(scope, base, cwd))
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

	// The canonical .agents/skills copies are NOT deleted, even when the
	// "agents" entry itself is disabled: they are the scope's skill store and
	// the target of every other agent's link. Removing copies is uninstall's
	// job.
	if agentdir.IsCanonicalLocal(a) && anyVendoredRoot(st) {
		rep.Event(Event{Type: EventLog, Level: LevelInfo, Code: CodeCopiesKept,
			Text: fmt.Sprintf("committed copies in the projects' %s folders stay in place", agentdir.CanonicalLocalRel)})
	}
	if agentdir.IsCanonicalGlobal(a) && anyGlobalInstall(st) {
		rep.Event(Event{Type: EventLog, Level: LevelInfo, Code: CodeCopiesKept,
			Text: fmt.Sprintf("global copies in %s stay in place", CanonicalDisplay(agentdir.Global))})
	}

	change := AgentChange{Name: a.Name, Enabled: false, Skills: sortedKeys(skills), Places: dedupe(places), Warnings: warnings}
	text := fmt.Sprintf("disabled %s — nothing to remove", a.Name)
	if len(skills) > 0 {
		text = fmt.Sprintf("disabled %s — removed %d skill%s (%s)", a.Name, len(skills), plural(len(skills)), strings.Join(change.Places, ", "))
	}
	rep.Event(Event{Type: EventLog, Level: LevelSuccess, Code: CodeAgentDisabled, Text: text})
	return change
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

// anyVendoredRoot reports whether any skill has a recorded Local install root.
func anyVendoredRoot(st *state.State) bool {
	for _, e := range st.Skills {
		if len(e.VendoredAt) > 0 {
			return true
		}
	}
	return false
}

// anyGlobalInstall reports whether any skill has a recorded Global install.
func anyGlobalInstall(st *state.State) bool {
	for _, e := range st.Skills {
		if e.Global {
			return true
		}
	}
	return false
}

// sortedKeys returns the keys of set, sorted.
func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// plural returns "s" unless n is exactly 1, for simple count messages.
func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
