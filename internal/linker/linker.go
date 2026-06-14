// Package linker creates, removes, and discovers the symlinks that expose a
// skill in Home to the agents that read it. Each Link is a symlink at
// <agent-folder>/<id> whose target is <home>/skills/<id> (see docs/PLAN.md §3
// and the "Link"/"Unlink" entries in CONTEXT.md).
//
// The package is safe by default. It only ever creates, inspects, or removes
// symlinks that resolve into Home's skills/ subtree; it refuses to clobber or
// delete anything it did not create (a real file, a real directory, or a
// symlink pointing somewhere foreign). Which links currently exist is never
// persisted — ScanLinks reads them live from disk so they cannot drift.
package linker

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/ultrakorne/skillm/internal/agentdir"
	"github.com/ultrakorne/skillm/internal/store"
)

// Action describes what happened (or what was found) for a single
// agent/scope/skill combination.
type Action int

const (
	// ActionCreated means a new symlink into Home was created.
	ActionCreated Action = iota
	// ActionAlreadyLinked means a correct symlink into Home was already
	// present, so the operation was a no-op (Link only).
	ActionAlreadyLinked
	// ActionRemoved means an existing symlink into Home was removed (Unlink).
	ActionRemoved
	// ActionAbsent means there was nothing to remove (Unlink), or that the
	// scan found no link for this agent (ScanLinks).
	ActionAbsent
	// ActionFound means ScanLinks discovered a live symlink into Home.
	ActionFound
)

// String renders the action as a short lowercase label.
func (a Action) String() string {
	switch a {
	case ActionCreated:
		return "created"
	case ActionAlreadyLinked:
		return "already-linked"
	case ActionRemoved:
		return "removed"
	case ActionAbsent:
		return "absent"
	case ActionFound:
		return "found"
	default:
		return fmt.Sprintf("Action(%d)", int(a))
	}
}

// AgentResult is the outcome of a Link/Unlink/ScanLinks operation for one
// agent at one scope.
type AgentResult struct {
	// Agent is the agent whose skill folder was touched.
	Agent agentdir.Agent
	// Scope is the scope (Global or Local) the link was made at.
	Scope agentdir.Scope
	// ID is the skill id involved.
	ID string
	// Path is the absolute link path inside the agent's skill folder
	// (SkillsFolder/<id>).
	Path string
	// Target is the symlink's target. For Link it is the intended target
	// (the Home skill dir). For ScanLinks it is the resolved on-disk target
	// of the discovered link. Empty when there is no link.
	Target string
	// Action is what happened (or was found) for this agent.
	Action Action
}

// Result aggregates the per-agent outcomes of a single Link, Unlink, or
// ScanLinks call, in the order the agents were supplied.
type Result struct {
	Agents []AgentResult
}

// Link symlinks skill id into every supplied agent's skill folder at scope,
// pointing at store.SkillDir(home, id). For each agent:
//
//   - the agent's skill folder is created if missing;
//   - if no entry exists at the link path, a fresh symlink is created
//     (ActionCreated);
//   - if a symlink already points at the correct Home skill dir, it is left
//     as-is (ActionAlreadyLinked, a no-op);
//   - if a symlink points at a *different* place inside Home's skills/ subtree,
//     it is repointed to the correct target (ActionCreated);
//   - if the entry is a real file, a real directory, or a symlink resolving
//     OUTSIDE Home's skills/ subtree, Link refuses: it returns an error and
//     leaves that entry untouched.
//
// On the first refusal Link returns the partial Result gathered so far
// together with the error, having mutated nothing it should not have.
func Link(home, id string, agents []agentdir.Agent, scope agentdir.Scope, cwd string) (Result, error) {
	var res Result
	target := store.SkillDir(home, id)

	for _, a := range agents {
		folder := agentdir.SkillsFolder(a, scope, cwd)
		linkPath := agentdir.LinkPath(a, scope, cwd, id)

		ar := AgentResult{Agent: a, Scope: scope, ID: id, Path: linkPath, Target: target}

		// Inspect the existing entry, if any, without following symlinks.
		info, lerr := os.Lstat(linkPath)
		switch {
		case lerr == nil && info.Mode()&fs.ModeSymlink != 0:
			// An existing symlink. Decide if it is one of ours.
			ours, dest, err := linkIntoHome(home, linkPath)
			if err != nil {
				return res, fmt.Errorf("inspect existing link %s: %w", linkPath, err)
			}
			if !ours {
				return res, fmt.Errorf(
					"refusing to overwrite %s: it is a symlink to %s, which is not managed by skillm (use --force semantics in the caller or remove it manually)",
					linkPath, dest)
			}
			if filepath.Clean(dest) == filepath.Clean(target) {
				ar.Action = ActionAlreadyLinked
				res.Agents = append(res.Agents, ar)
				continue
			}
			// A skillm-managed link pointing at a different skill in Home.
			// Repoint it to the requested target.
			if err := replaceSymlink(linkPath, target); err != nil {
				return res, fmt.Errorf("repoint link %s: %w", linkPath, err)
			}
			ar.Action = ActionCreated
			res.Agents = append(res.Agents, ar)

		case lerr == nil:
			// A real file or directory occupies the link path. Never clobber it.
			kind := "file"
			if info.IsDir() {
				kind = "directory"
			}
			return res, fmt.Errorf(
				"refusing to overwrite %s: a %s already exists there and was not created by skillm",
				linkPath, kind)

		case errors.Is(lerr, fs.ErrNotExist):
			// Nothing there — create the folder and the symlink.
			if err := os.MkdirAll(folder, 0o755); err != nil {
				return res, fmt.Errorf("create skill folder %s: %w", folder, err)
			}
			if err := os.Symlink(target, linkPath); err != nil {
				return res, fmt.Errorf("create link %s -> %s: %w", linkPath, target, err)
			}
			ar.Action = ActionCreated
			res.Agents = append(res.Agents, ar)

		default:
			return res, fmt.Errorf("inspect %s: %w", linkPath, lerr)
		}
	}
	return res, nil
}

// Unlink removes skill id's symlink from every supplied agent's skill folder at
// scope. It only removes a symlink that resolves into Home's skills/ subtree;
// it never touches a real file, a real directory, or a foreign symlink. For
// each agent:
//
//   - a skillm-managed symlink is removed (ActionRemoved);
//   - a missing entry is reported as ActionAbsent (idempotent — not an error);
//   - a real file/dir or a foreign symlink causes a refusal error, leaving it
//     untouched.
//
// On the first refusal Unlink returns the partial Result and the error.
func Unlink(home, id string, agents []agentdir.Agent, scope agentdir.Scope, cwd string) (Result, error) {
	var res Result

	for _, a := range agents {
		linkPath := agentdir.LinkPath(a, scope, cwd, id)
		ar := AgentResult{Agent: a, Scope: scope, ID: id, Path: linkPath}

		info, lerr := os.Lstat(linkPath)
		switch {
		case errors.Is(lerr, fs.ErrNotExist):
			ar.Action = ActionAbsent
			res.Agents = append(res.Agents, ar)

		case lerr != nil:
			return res, fmt.Errorf("inspect %s: %w", linkPath, lerr)

		case info.Mode()&fs.ModeSymlink == 0:
			// A real file or directory — never delete it.
			kind := "file"
			if info.IsDir() {
				kind = "directory"
			}
			return res, fmt.Errorf(
				"refusing to remove %s: it is a %s, not a skillm-managed link",
				linkPath, kind)

		default:
			ours, dest, err := linkIntoHome(home, linkPath)
			if err != nil {
				return res, fmt.Errorf("inspect link %s: %w", linkPath, err)
			}
			if !ours {
				return res, fmt.Errorf(
					"refusing to remove %s: it is a symlink to %s, which is not managed by skillm",
					linkPath, dest)
			}
			if err := os.Remove(linkPath); err != nil {
				return res, fmt.Errorf("remove link %s: %w", linkPath, err)
			}
			ar.Target = dest
			ar.Action = ActionRemoved
			res.Agents = append(res.Agents, ar)
		}
	}
	return res, nil
}

// ScanLinks reads the live link state of skill id across every supplied agent
// at scope. It inspects each agent's skill folder for a symlink at <folder>/<id>
// whose target resolves into Home's skills/ subtree. Agents with such a link get
// ActionFound (with Target set to the resolved destination); agents without one
// get ActionAbsent. Foreign symlinks and real files are reported as ActionAbsent
// (they are not skillm links) and never mutated. ScanLinks changes nothing on
// disk.
func ScanLinks(home, id string, agents []agentdir.Agent, scope agentdir.Scope, cwd string) (Result, error) {
	var res Result

	for _, a := range agents {
		linkPath := agentdir.LinkPath(a, scope, cwd, id)
		ar := AgentResult{Agent: a, Scope: scope, ID: id, Path: linkPath, Action: ActionAbsent}

		info, lerr := os.Lstat(linkPath)
		switch {
		case errors.Is(lerr, fs.ErrNotExist):
			// Nothing here.
		case lerr != nil:
			return res, fmt.Errorf("inspect %s: %w", linkPath, lerr)
		case info.Mode()&fs.ModeSymlink == 0:
			// A real file/dir — not a skillm link.
		default:
			ours, dest, err := linkIntoHome(home, linkPath)
			if err != nil {
				return res, fmt.Errorf("inspect link %s: %w", linkPath, err)
			}
			if ours {
				ar.Target = dest
				ar.Action = ActionFound
			}
		}
		res.Agents = append(res.Agents, ar)
	}
	return res, nil
}

// LinkInfo is a flattened view of a link discovered by ScanAll: one record per
// (agent, scope) a skill is linked at.
type LinkInfo struct {
	// ID is the skill id (the link path's basename).
	ID string
	// Agent is the agent whose folder holds the link.
	Agent agentdir.Agent
	// Scope is the scope (Global or Local) the link lives at.
	Scope agentdir.Scope
	// Path is the absolute symlink path.
	Path string
	// Target is the resolved on-disk target inside Home's skills/ subtree.
	Target string
}

// ScanAll discovers every skillm-managed link across the supplied agents at
// scope, regardless of skill id, by listing each agent's skill folder and
// keeping the entries that are symlinks resolving into Home's skills/ subtree.
// It is the basis for `skillm list` (which needs every linked id, not a single
// one). Missing skill folders are simply skipped. ScanAll changes nothing.
//
// The returned slice is ordered by agent (in the supplied order); within an
// agent, ids follow the directory listing order returned by the OS.
func ScanAll(home string, agents []agentdir.Agent, scope agentdir.Scope, cwd string) ([]LinkInfo, error) {
	var out []LinkInfo

	for _, a := range agents {
		folder := agentdir.SkillsFolder(a, scope, cwd)
		entries, err := os.ReadDir(folder)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue // agent has no skill folder yet
			}
			return nil, fmt.Errorf("read skill folder %s: %w", folder, err)
		}
		for _, e := range entries {
			if e.Type()&fs.ModeSymlink == 0 {
				continue // real file/dir, not a link skillm made
			}
			linkPath := filepath.Join(folder, e.Name())
			ours, dest, err := linkIntoHome(home, linkPath)
			if err != nil {
				return nil, fmt.Errorf("inspect link %s: %w", linkPath, err)
			}
			if !ours {
				continue
			}
			out = append(out, LinkInfo{
				ID:     e.Name(),
				Agent:  a,
				Scope:  scope,
				Path:   linkPath,
				Target: dest,
			})
		}
	}
	return out, nil
}

// linkIntoHome reports whether linkPath is a symlink whose target resolves to a
// location inside Home's skills/ subtree. It returns:
//
//   - ours:  true iff the resolved target lies within <home>/skills;
//   - dest:  the resolved (cleaned, absolute where possible) target path;
//   - err:   only for unexpected read errors (a dangling or unreadable link is
//     reported as not-ours with a best-effort dest, not as an error).
//
// The target is read with os.Readlink and resolved relative to the link's
// directory when it is not absolute, matching how the OS dereferences it.
func linkIntoHome(home, linkPath string) (ours bool, dest string, err error) {
	raw, err := os.Readlink(linkPath)
	if err != nil {
		return false, "", err
	}
	dest = raw
	if !filepath.IsAbs(dest) {
		dest = filepath.Join(filepath.Dir(linkPath), dest)
	}
	dest = filepath.Clean(dest)

	skillsRoot := filepath.Clean(store.SkillsDir(home))
	return underDir(skillsRoot, dest), dest, nil
}

// underDir reports whether path is dir itself or lies beneath it, using a
// cleaned relative path so that sibling directories sharing a prefix (e.g.
// /home/.skills vs /home/.skillsX) are not mistaken for descendants.
func underDir(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	// Anything that has to climb out of dir (rel == ".." or starts with
	// "../") is not contained within it.
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

// replaceSymlink atomically replaces the symlink at linkPath so it points at
// target. It writes a temporary link beside the destination and renames it over
// the old one, avoiding a window where linkPath does not exist.
func replaceSymlink(linkPath, target string) error {
	tmp := linkPath + ".skillm-tmp"
	_ = os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, linkPath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
