// Package linker creates, removes, and discovers the symlinks that expose a
// skill to the agents that read it. Both scopes point at a canonical
// .agents/skills copy; the link shape depends on the scope:
//
//   - Global: an absolute symlink at <agent-global-folder>/<id> pointing at
//     the canonical global copy, ~/.agents/skills/<id>;
//   - Local: a RELATIVE symlink at <base>/<agent-local>/<id> pointing at the
//     project's canonical copy, <base>/.agents/skills/<id> (e.g.
//     .claude/skills/x -> ../../.agents/skills/x). Relative links survive a
//     clone, so they are committable alongside the canonical copy — the same
//     layout vercel's skills CLI writes.
//
// At either scope, an agent whose folder IS the canonical store needs no link
// at all and is skipped.
//
// The package is safe by default. It only ever creates, inspects, or removes
// symlinks it recognizes as its own — those resolving into the scope's
// canonical .agents/skills store, or into Home's legacy skills/ subtree (the
// pre-vendoring link shape: skillm no longer maintains that subtree, but a link
// still pointing into it is recognized so re-running install can convert an old
// layout and uninstall can clear it); it refuses to clobber or delete anything
// else (a real file, a real directory, or a symlink pointing somewhere
// foreign). Which links currently exist is never persisted — ScanLinks reads
// them live from disk so they cannot drift.
package linker

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/ultrakorne/skillm/internal/agentdir"
)

// legacyHomeSkillsSubdir is the name of the pre-vendoring skills library under
// Home (<home>/skills). skillm no longer writes it, but links pointing into it
// are still recognized as skillm's own so old layouts convert and uninstall.
const legacyHomeSkillsSubdir = "skills"

// symlinkHint wraps a symlink error with actionable guidance on Windows when
// the root cause is a missing privilege (Developer Mode not enabled).
func symlinkHint(err error) error {
	if err == nil || runtime.GOOS != "windows" {
		return err
	}
	var errno syscall.Errno
	if errors.As(err, &errno) && errno == 1314 {
		return fmt.Errorf("%w\n\nHint: skillm uses symbolic links, which on Windows require Developer Mode.\n"+
			"Enable it at: Settings → System → For developers → Developer Mode\n"+
			"Then restart your terminal.", err)
	}
	return err
}

// replaceLink replaces the link at linkPath so it points at target.
// On Unix this is atomic (temp link + rename). On Windows directory symlinks
// cannot be atomically renamed over one another, so we remove first.
func replaceLink(linkPath, target string) error {
	if runtime.GOOS == "windows" {
		if err := os.Remove(linkPath); err != nil && !os.IsNotExist(err) {
			return err
		}
		return symlinkHint(os.Symlink(target, linkPath))
	}
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
	// (the scope's canonical skill dir). For ScanLinks it is the resolved
	// on-disk target of the discovered link. Empty when there is no link.
	Target string
	// Action is what happened (or was found) for this agent.
	Action Action
}

// Result aggregates the per-agent outcomes of a single Link, Unlink, or
// ScanLinks call, in the order the agents were supplied.
type Result struct {
	Agents []AgentResult
}

// Link symlinks skill id into every supplied agent's skill folder at scope.
// At Global scope the link is absolute and points at the canonical global
// copy, ~/.agents/skills/<id>; at Local scope it is relative and points at the
// project's canonical copy, <cwd>/.agents/skills/<id>. At either scope an
// agent whose folder IS the canonical store is skipped (the copy itself serves
// it). For each agent:
//
//   - the agent's skill folder is created if missing;
//   - if no entry exists at the link path, a fresh symlink is created
//     (ActionCreated);
//   - if a symlink already resolves to the correct target, it is left
//     as-is (ActionAlreadyLinked, a no-op);
//   - if a symlink resolves to a *different* skillm-managed place (another
//     skill, or a legacy absolute link into Home's store), it is repointed
//     to the correct target (ActionCreated);
//   - if the entry is a real file, a real directory, or a foreign symlink,
//     Link refuses: it returns an error and leaves that entry untouched.
//
// On the first refusal Link returns the partial Result gathered so far
// together with the error, having mutated nothing it should not have.
func Link(home, id string, agents []agentdir.Agent, scope agentdir.Scope, cwd string) (Result, error) {
	var res Result

	for _, a := range agents {
		if agentdir.IsCanonicalAt(a, scope) {
			continue // served directly by the canonical copy; no link needed
		}
		folder, ok := agentdir.SkillsFolder(a, scope, cwd)
		if !ok {
			continue // agent has no folder at this scope; nothing to link
		}
		linkPath := filepath.Join(folder, id)

		// The link target: absolute into the canonical global store at Global
		// scope, relative into the project's canonical store at Local scope.
		// resolved is the absolute form used for comparisons.
		var target, resolved string
		if scope == agentdir.Local {
			resolved = agentdir.CanonicalSkillDir(cwd, id)
			rel, rerr := filepath.Rel(folder, resolved)
			if rerr != nil {
				return res, fmt.Errorf("relativize %s from %s: %w", resolved, folder, rerr)
			}
			target = rel
		} else {
			target = agentdir.CanonicalSkillDirAt(scope, cwd, id)
			resolved = target
		}

		ar := AgentResult{Agent: a, Scope: scope, ID: id, Path: linkPath, Target: resolved}

		// Inspect the existing entry, if any, without following symlinks.
		info, lerr := os.Lstat(linkPath)
		switch {
		case lerr == nil && info.Mode()&fs.ModeSymlink != 0:
			// An existing symlink. Decide if it is one of ours.
			ours, dest, err := ownedLink(home, cwd, scope, linkPath)
			if err != nil {
				return res, fmt.Errorf("inspect existing link %s: %w", linkPath, err)
			}
			if !ours {
				return res, fmt.Errorf(
					"refusing to overwrite %s: it is a symlink to %s, which is not managed by skillm (use --force semantics in the caller or remove it manually)",
					linkPath, dest)
			}
			if filepath.Clean(dest) == filepath.Clean(resolved) {
				ar.Action = ActionAlreadyLinked
				res.Agents = append(res.Agents, ar)
				continue
			}
			// A skillm-managed link pointing elsewhere (another skill, or a
			// legacy link into Home's store). Repoint it.
			if err := replaceLink(linkPath, target); err != nil {
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
				return res, fmt.Errorf("create link %s -> %s: %w", linkPath, target, symlinkHint(err))
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
		if agentdir.IsCanonicalAt(a, scope) {
			// The canonical agent holds the copy itself, not a link; removing
			// the copy is the install machinery's job, not Unlink's.
			continue
		}
		linkPath, ok := agentdir.LinkPath(a, scope, cwd, id)
		if !ok {
			continue // agent has no folder at this scope; nothing to unlink
		}
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
			ours, dest, err := ownedLink(home, cwd, scope, linkPath)
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
// that skillm owns (resolving into the scope's canonical .agents/skills store,
// or into Home's skills/ subtree). Agents with such a link
// get ActionFound (with Target set to the resolved destination); agents without
// one get ActionAbsent. Foreign symlinks and real files are reported as
// ActionAbsent (they are not skillm links) and never mutated. ScanLinks changes
// nothing on disk.
func ScanLinks(home, id string, agents []agentdir.Agent, scope agentdir.Scope, cwd string) (Result, error) {
	var res Result

	for _, a := range agents {
		linkPath, ok := agentdir.LinkPath(a, scope, cwd, id)
		if !ok {
			continue // agent has no folder at this scope; nothing to scan
		}
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
			ours, dest, err := ownedLink(home, cwd, scope, linkPath)
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
	// Target is the link's resolved on-disk target (a canonical store or,
	// for a legacy link, Home's skills/ subtree).
	Target string
}

// ScanAll discovers every skillm-managed link across the supplied agents at
// scope, regardless of skill id, by listing each agent's skill folder and
// keeping the entries that are symlinks skillm owns (into the scope's
// canonical .agents/skills store, or into Home's skills/ subtree). It is the
// basis for `skillm list` (which needs every linked id, not
// a single one). Missing skill folders are simply skipped. ScanAll changes
// nothing.
//
// The returned slice is ordered by agent (in the supplied order); within an
// agent, ids follow the directory listing order returned by the OS.
func ScanAll(home string, agents []agentdir.Agent, scope agentdir.Scope, cwd string) ([]LinkInfo, error) {
	var out []LinkInfo

	for _, a := range agents {
		folder, ok := agentdir.SkillsFolder(a, scope, cwd)
		if !ok {
			continue // agent has no folder at this scope
		}
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
			ours, dest, err := ownedLink(home, cwd, scope, linkPath)
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

// TargetKind classifies what currently occupies an install target path
// (<agent-folder>/<id>). It lets the vendoring layer decide whether a copy can
// be written there: skillm freely replaces its own symlink (TargetOurLink) or
// refreshes a copy it recorded, but treats a foreign file/dir/symlink as
// off-limits unless the caller forces it.
type TargetKind int

const (
	// TargetAbsent: nothing exists at the path.
	TargetAbsent TargetKind = iota
	// TargetOurLink: a symlink resolving into Home's skills/ subtree — a
	// skillm-managed symlink install.
	TargetOurLink
	// TargetForeignLink: a symlink resolving outside Home (or dangling) — not
	// skillm's.
	TargetForeignLink
	// TargetDir: a real directory. Whether it is a skillm Vendored copy or a
	// foreign directory cannot be told from disk alone (a copy carries no
	// marker); the caller decides using the recorded vendored roots.
	TargetDir
	// TargetFile: a real (non-directory, non-symlink) file.
	TargetFile
)

// String renders the kind as a short lowercase label for diagnostics.
func (k TargetKind) String() string {
	switch k {
	case TargetAbsent:
		return "absent"
	case TargetOurLink:
		return "skillm-symlink"
	case TargetForeignLink:
		return "foreign-symlink"
	case TargetDir:
		return "directory"
	case TargetFile:
		return "file"
	default:
		return fmt.Sprintf("TargetKind(%d)", int(k))
	}
}

// Classify inspects path (without following symlinks) and reports what occupies
// it, relative to Home. For TargetOurLink and TargetForeignLink, dest is the
// symlink's resolved target; it is empty otherwise. A non-existent path is
// TargetAbsent with a nil error; only unexpected stat/readlink failures return
// an error.
func Classify(home, path string) (kind TargetKind, dest string, err error) {
	info, lerr := os.Lstat(path)
	switch {
	case errors.Is(lerr, fs.ErrNotExist):
		return TargetAbsent, "", nil
	case lerr != nil:
		return TargetAbsent, "", fmt.Errorf("inspect %s: %w", path, lerr)
	case info.Mode()&fs.ModeSymlink != 0:
		ours, d, err := linkIntoHome(home, path)
		if err != nil {
			// A dangling or unreadable symlink: treat as foreign, never ours.
			return TargetForeignLink, "", nil
		}
		if ours {
			return TargetOurLink, d, nil
		}
		return TargetForeignLink, d, nil
	case info.IsDir():
		return TargetDir, "", nil
	default:
		return TargetFile, "", nil
	}
}

// linkIntoHome reports whether linkPath is a symlink whose target resolves to a
// location inside Home's legacy skills/ subtree. It returns:
//
//   - ours:  true iff the resolved target lies within <home>/skills;
//   - dest:  the resolved (cleaned, absolute where possible) target path;
//   - err:   only for unexpected read errors (a dangling or unreadable link is
//     reported as not-ours with a best-effort dest, not as an error).
//
// The target is read with os.Readlink and resolved relative to the link's
// directory when it is not absolute, matching how the OS dereferences it.
// skillm no longer creates such links; this recognition only lets it clean up
// or convert links left by a pre-vendoring version.
func linkIntoHome(home, linkPath string) (ours bool, dest string, err error) {
	dest, err = resolveLinkTarget(linkPath)
	if err != nil {
		return false, "", err
	}
	skillsRoot := filepath.Clean(filepath.Join(home, legacyHomeSkillsSubdir))
	return underDir(skillsRoot, dest), dest, nil
}

// ownedLink reports whether linkPath is a symlink skillm owns at the given
// scope: one resolving into the scope's canonical .agents/skills store
// (~/.agents/skills at Global, <base>/.agents/skills at Local — the current
// link shapes), or into Home's skills/ subtree (the legacy shape at both
// scopes, recognized so old links can still be repointed or removed). dest is
// the resolved target either way.
func ownedLink(home, base string, scope agentdir.Scope, linkPath string) (ours bool, dest string, err error) {
	ours, dest, err = linkIntoHome(home, linkPath)
	if err != nil || ours {
		return ours, dest, err
	}
	if scope != agentdir.Local || base != "" {
		canonical := filepath.Clean(agentdir.CanonicalDirAt(scope, base))
		if underDir(canonical, dest) {
			return true, dest, nil
		}
	}
	return false, dest, nil
}

// resolveLinkTarget reads linkPath's symlink target and resolves it to a
// cleaned path, joining a relative target onto the link's directory the way
// the OS dereferences it.
func resolveLinkTarget(linkPath string) (string, error) {
	raw, err := os.Readlink(linkPath)
	if err != nil {
		return "", err
	}
	dest := raw
	if !filepath.IsAbs(dest) {
		dest = filepath.Join(filepath.Dir(linkPath), dest)
	}
	return filepath.Clean(dest), nil
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
