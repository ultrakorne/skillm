package protocol

import (
	"github.com/ultrakorne/skillm/internal/agentdir"
	"github.com/ultrakorne/skillm/internal/core"
)

// The data of the commands that change something: install, update, import,
// uninstall and upgrade. Every list is present (never null), so a GUI can
// decode it without optionals.

// InstallData is `skillm install --json`'s data.
type InstallData struct {
	// Scope is "global" or "local".
	Scope string `json:"scope"`
	// Root is a Local install's project root; omitted for Global.
	Root string `json:"root,omitempty"`
	// Skills has one entry per skill the install reached, in order.
	Skills []InstalledSkill `json:"skills"`
}

// InstalledSkill is what install did for one skill.
type InstalledSkill struct {
	// ID is the Skill ID it was installed under (the --as id, if given).
	ID string `json:"id"`
	// Action is one of the Action constants.
	Action string `json:"action"`
}

// Install actions: what happened at a skill's canonical slot.
const (
	// ActionInstalled: a copy was written where nothing was.
	ActionInstalled = "installed"
	// ActionConverted: a legacy skillm symlink was replaced by a copy.
	ActionConverted = "converted"
	// ActionRefreshed: the existing recorded copy was rewritten.
	ActionRefreshed = "refreshed"
	// ActionOverwritten: an entry skillm did not create was overwritten
	// (--yes or --force).
	ActionOverwritten = "overwritten"
	// ActionSkipped: an entry skillm did not create was left alone
	// (--skip-foreign); nothing was written for the skill.
	ActionSkipped = "skipped"
)

// installAction names a core.VendorAction.
func installAction(a core.VendorAction) string {
	switch a {
	case core.VendorConverted:
		return ActionConverted
	case core.VendorRefreshed:
		return ActionRefreshed
	case core.VendorAdopted:
		return ActionOverwritten
	case core.VendorBlocked:
		return ActionSkipped
	default:
		return ActionInstalled
	}
}

// NewInstallData converts core.InstallSkills' result.
func NewInstallData(res core.InstallResult) InstallData {
	out := InstallData{Scope: string(core.ScopeGlobal), Skills: make([]InstalledSkill, 0, len(res.Skills))}
	if res.Scope == agentdir.Local {
		out.Scope, out.Root = string(core.ScopeLocal), res.Base
	}
	for _, s := range res.Skills {
		out.Skills = append(out.Skills, InstalledSkill{ID: s.ID, Action: installAction(s.Action)})
	}
	return out
}

// UpdateData is `skillm update --json`'s data.
type UpdateData struct {
	// Skills has one entry per skill in scope: the git skills in Registry
	// order, then the local ones.
	Skills []UpdatedSkill `json:"skills"`
	// Imported lists what an all-skills update's adoption of the tracked
	// projects' skills-lock.json files did.
	Imported []ImportedSkill `json:"imported"`
	// Updated counts the skills whose upstream Revision advanced.
	Updated int `json:"updated"`
	// Synced says a copy was rewritten or a link made (for any reason).
	Synced bool `json:"synced"`
}

// UpdatedSkill is what update did for one skill.
type UpdatedSkill struct {
	ID string `json:"id"`
	// Kind is "git" or "local".
	Kind string `json:"kind"`
	// Outcome is "updated", "up_to_date", "synced", "pruned", "failed" or
	// "drift_check_skipped".
	Outcome string `json:"outcome"`
	// Revision is the recorded Revision afterwards (git skills).
	Revision string `json:"revision,omitempty"`
	// Advanced says the upstream Revision advanced and was recorded; it
	// stays true when the skill was then pruned.
	Advanced bool `json:"advanced"`
	// Pruned lists the installs forgotten because their copy had vanished:
	// "global" or a project root.
	Pruned []string `json:"pruned"`
	// Error is the cause of "failed" or "drift_check_skipped".
	Error string `json:"error,omitempty"`
	// Warnings are the writes that failed for installs that stay recorded
	// (a partial success; the next update retries them).
	Warnings []string `json:"warnings"`
}

// NewUpdateData converts core.Update's result.
func NewUpdateData(res core.UpdateResult) UpdateData {
	out := UpdateData{
		Skills:   make([]UpdatedSkill, 0, len(res.Skills)),
		Imported: newImportedSkills(res.Imported),
		Synced:   res.Synced,
	}
	for _, s := range res.Skills {
		us := UpdatedSkill{
			ID:       s.ID,
			Kind:     s.Kind,
			Outcome:  string(s.Outcome),
			Revision: s.Revision,
			Advanced: s.Advanced,
			Pruned:   nonNil(s.Pruned),
			Error:    errText(s.Err),
			Warnings: errTexts(s.Warnings),
		}
		if s.Advanced {
			out.Updated++
		}
		out.Skills = append(out.Skills, us)
	}
	return out
}

// ImportData is `skillm import --json`'s data.
type ImportData struct {
	// Root is the absolute project directory whose skills-lock.json was read.
	Root string `json:"root"`
	// Entries is the number of entries in it (0 when there is none).
	Entries int `json:"entries"`
	// Skills lists the entries imported, adopted or skipped; an entry that
	// was already fully adopted is not listed.
	Skills []ImportedSkill `json:"skills"`
}

// ImportedSkill is what an import did with one lockfile entry.
type ImportedSkill struct {
	ID string `json:"id"`
	// Root is the project the entry is in.
	Root string `json:"root"`
	// Outcome is "imported", "adopted" or "skipped".
	Outcome string `json:"outcome"`
	// Error is why a skipped entry was skipped.
	Error string `json:"error,omitempty"`
}

// NewImportData converts core.Import's result.
func NewImportData(res core.ImportResult) ImportData {
	return ImportData{Root: res.Root, Entries: res.Entries, Skills: newImportedSkills(res.Skills)}
}

func newImportedSkills(skills []core.ImportedSkill) []ImportedSkill {
	out := make([]ImportedSkill, 0, len(skills))
	for _, s := range skills {
		out = append(out, ImportedSkill{ID: s.ID, Root: s.Root, Outcome: string(s.Outcome), Error: errText(s.Err)})
	}
	return out
}

// UninstallData is `skillm uninstall --json`'s data.
type UninstallData struct {
	// Skills has one entry per uninstalled skill, in the order asked.
	Skills []UninstalledSkill `json:"skills"`
}

// UninstalledSkill is what uninstall removed for one skill.
type UninstalledSkill struct {
	ID string `json:"id"`
	// RemovedCopies lists the installs whose canonical copy was deleted:
	// "global" or a project root.
	RemovedCopies []string `json:"removed_copies"`
	// Warnings are the removals that failed without stopping the uninstall
	// (the entry is left in place).
	Warnings []string `json:"warnings"`
}

// NewUninstallData converts core.Uninstall's result.
func NewUninstallData(res core.UninstallResult) UninstallData {
	out := UninstallData{Skills: make([]UninstalledSkill, 0, len(res.Skills))}
	for _, s := range res.Skills {
		out.Skills = append(out.Skills, UninstalledSkill{
			ID:            s.ID,
			RemovedCopies: nonNil(s.RemovedCopies),
			Warnings:      errTexts(s.Warnings),
		})
	}
	return out
}

// SelfStatusData is `skillm upgrade --check --json`'s data: the running
// skillm against the latest release.
type SelfStatusData struct {
	// Current is the running version, without a leading "v" ("dev" for an
	// unstamped source build).
	Current string `json:"current"`
	// Latest is the latest release's version; omitted for method "dev",
	// which looks nothing up.
	Latest string `json:"latest,omitempty"`
	// Available says Latest is newer than Current.
	Available bool `json:"available"`
	// Eligible says `skillm upgrade` can install Latest (available, and
	// method "binary").
	Eligible bool `json:"eligible"`
	// Method is "binary", "bundled" (inside an app bundle, which upgrades
	// it) or "dev" (a source build).
	Method string `json:"method"`
	// Executable is the running binary's resolved path, when known.
	Executable string `json:"executable,omitempty"`
}

// NewSelfStatusData converts core.CheckSelf's result.
func NewSelfStatusData(st core.SelfStatus) SelfStatusData {
	return SelfStatusData{
		Current:    st.Current,
		Latest:     st.Latest,
		Available:  st.Available,
		Eligible:   st.Eligible,
		Method:     string(st.Method),
		Executable: st.Executable,
	}
}

// UpgradeData is `skillm upgrade --json`'s data.
type UpgradeData struct {
	// Upgraded says the binary was replaced; false when From is already
	// the latest release.
	Upgraded bool `json:"upgraded"`
	// From is the version that was running.
	From string `json:"from"`
	// To is the latest release's version.
	To string `json:"to"`
	// Path is the upgraded binary's path; omitted when nothing was upgraded.
	Path string `json:"path,omitempty"`
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func errTexts(errs []error) []string {
	out := make([]string, 0, len(errs))
	for _, err := range errs {
		out = append(out, err.Error())
	}
	return out
}
