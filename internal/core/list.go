package core

import (
	"path/filepath"
	"sort"
	"time"

	"github.com/ultrakorne/skillm/internal/agentdir"
	"github.com/ultrakorne/skillm/internal/config"
	"github.com/ultrakorne/skillm/internal/state"
)

// Scope is where an install makes a skill available.
type Scope string

const (
	ScopeGlobal Scope = "global"
	ScopeLocal  Scope = "local"
)

// Install is one place a skill is installed, read live from disk.
type Install struct {
	Scope Scope
	// Root is the absolute project root of a Local install; empty for Global.
	Root string
	// Path is where the install's canonical copy lives.
	Path string
	// Agents are the enabled agents the install currently serves, sorted;
	// empty, never nil, when it serves none.
	Agents []string
	// Recorded says the Registry records a canonical copy here.
	Recorded bool
	// Exists says the recorded canonical copy is on disk. It is false when
	// the install is not recorded (only agent links were found there).
	Exists bool
}

// ListedSkill is one registered skill and its installs.
type ListedSkill struct {
	ID          string
	Kind        string
	Source      string
	Subpath     string
	Ref         string
	Revision    string
	InstalledAt time.Time
	// Installs lists the Global install first, then Local ones by Root. An
	// install is listed when it is recorded or serves at least one agent.
	Installs []Install
}

// SourceLabel renders s's Source for display (see the package-level
// SourceLabel).
func (s ListedSkill) SourceLabel() string {
	return SourceLabel(s.Kind, s.Source, s.Subpath)
}

// ListResult lists every registered skill in Registry order.
type ListResult struct {
	Skills []ListedSkill
}

// List reports every registered skill with where it is installed, read live
// from disk for the enabled agents. It is offline and read-only, and takes no
// Home lock. Local installs are looked for in opts.Cwd (when set), every
// tracked root and every recorded install root.
func List(opts Options) (ListResult, error) {
	cfg, err := config.Load(opts.Home)
	if err != nil {
		return ListResult{}, err
	}
	st, err := state.Load(opts.Home)
	if err != nil {
		return ListResult{}, err
	}
	agents := cfg.EnabledAgents()

	res := ListResult{Skills: make([]ListedSkill, 0, len(st.Skills))}
	for _, e := range st.Skills {
		res.Skills = append(res.Skills, ListedSkill{
			ID:          e.ID,
			Kind:        e.Kind,
			Source:      e.Source,
			Subpath:     e.Path,
			Ref:         e.Ref,
			Revision:    e.Revision,
			InstalledAt: e.InstalledAt,
			Installs:    installsOf(opts.Home, e, agents, st.LocalRoots, opts.Cwd),
		})
	}
	return res, nil
}

// installsOf scans, live from disk, where skill e is installed for agents. At
// Global scope an agent is served when it holds a skillm link in its
// user-level folder or — for the canonical ~/.agents/skills agent — when the
// recorded global copy exists. Local installs are looked for in cwd (when
// set), every tracked root and every recorded install root, counting agents'
// links there plus the canonical agent when the recorded copy exists. A
// canonical copy counts only for installs recorded in the Registry (e.Global,
// e.VendoredAt), so a foreign directory is never mistaken for skillm's.
func installsOf(home string, e state.SkillEntry, agents []agentdir.Agent, roots []string, cwd string) []Install {
	var out []Install

	g := Install{
		Scope:    ScopeGlobal,
		Path:     agentdir.CanonicalSkillDirAt(agentdir.Global, "", e.ID),
		Recorded: e.Global,
	}
	if e.Global {
		g.Exists = copyExists(home, e.ID, agentdir.Global, "")
		g.Agents = servedAgents(home, e.ID, agents, agentdir.Global, "")
	} else {
		g.Agents = scanLinkNames(home, e.ID, agents, agentdir.Global, "")
	}
	g.Agents = sortedNames(g.Agents)
	if g.Recorded || len(g.Agents) > 0 {
		out = append(out, g)
	}

	recorded := make(map[string]bool)
	for _, dir := range uniqueAbs(e.VendoredAt) {
		recorded[dir] = true
	}
	for _, dir := range localScanDirs(append(append([]string{}, roots...), e.VendoredAt...), cwd) {
		// Skip agents whose local folder aliases their global one at dir, so a
		// global link (e.g. when dir is home) is never also listed as local.
		localAgents := nonAliasedAt(agents, dir)
		l := Install{
			Scope:    ScopeLocal,
			Root:     dir,
			Path:     agentdir.CanonicalSkillDirAt(agentdir.Local, dir, e.ID),
			Recorded: recorded[dir],
		}
		if l.Recorded {
			l.Exists = copyExists(home, e.ID, agentdir.Local, dir)
			l.Agents = servedAgents(home, e.ID, localAgents, agentdir.Local, dir)
		} else {
			l.Agents = scanLinkNames(home, e.ID, localAgents, agentdir.Local, dir)
		}
		l.Agents = sortedNames(l.Agents)
		if l.Recorded || len(l.Agents) > 0 {
			out = append(out, l)
		}
	}
	return out
}

// sortedNames sorts names in place and returns them, never nil, so an
// install serving no agent reports an empty list rather than none.
func sortedNames(names []string) []string {
	if names == nil {
		return []string{}
	}
	sort.Strings(names)
	return names
}

// absOr returns p made absolute, or p itself when that fails.
func absOr(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}
