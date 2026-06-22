package ui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"charm.land/huh/v2"
)

// Option is one choice in the `add` skill picker. Label is what the user
// sees; Value is the skill id returned in the selection.
type Option struct {
	Label string
	Value string
}

// errNonInteractiveSkills is returned by SelectSkills when stdout is not a
// terminal — the message tells the caller exactly how to proceed without a
// prompt (PLAN §3 add, §4).
var errNonInteractiveSkills = errors.New("non-interactive: pass a skill id or --all")

// SelectSkills shows a huh multiselect for the `add` picker and returns the
// chosen skill ids. On a non-TTY it refuses and returns a message naming the
// escape hatch. An empty options slice returns no selection and no error.
func SelectSkills(prompt string, options []Option) ([]string, error) {
	if !IsTTY() {
		return nil, errNonInteractiveSkills
	}
	if len(options) == 0 {
		return nil, nil
	}

	opts := make([]huh.Option[string], 0, len(options))
	for _, o := range options {
		opts = append(opts, huh.NewOption(o.Label, o.Value))
	}

	var selected []string
	// Height must be set explicitly: huh v2's static-Options path leaves the
	// field height at 0, and Group.WithHeight only ever shrinks a field (never
	// grows it), so the options viewport collapses to zero and no rows render.
	// Cap the visible window at 10 so large catalogs scroll instead of sprawl.
	field := huh.NewMultiSelect[string]().
		Title(prompt).
		Options(opts...).
		Height(min(len(opts), 10) + 2).
		Value(&selected)

	if err := runForm(field); err != nil {
		return nil, err
	}
	return selected, nil
}

// SelectAgents shows a huh multiselect of the supported agents (all), with the
// currently enabled set pre-checked, for `skillm agent`. It returns the new
// selection. On a non-TTY it refuses with a clear message.
func SelectAgents(all []string, enabled []string) ([]string, error) {
	if !IsTTY() {
		return nil, errors.New("non-interactive: edit agents in config.toml")
	}
	if len(all) == 0 {
		return nil, nil
	}

	enabledSet := make(map[string]bool, len(enabled))
	for _, e := range enabled {
		enabledSet[e] = true
	}

	opts := make([]huh.Option[string], 0, len(all))
	for _, a := range all {
		opts = append(opts, huh.NewOption(a, a).Selected(enabledSet[a]))
	}

	selected := make([]string, 0, len(enabled))
	for _, a := range all { // seed value in registry order so a no-op confirm preserves it
		if enabledSet[a] {
			selected = append(selected, a)
		}
	}

	// Height is required — see SelectSkills for why huh v2 collapses the options
	// viewport to zero without it. +3 covers the title, description, and a row
	// of breathing space; the agent list is short so it never needs to scroll.
	field := huh.NewMultiSelect[string]().
		Title("Enabled agents").
		Description("Links apply to every enabled agent.").
		Options(opts...).
		Filterable(false).
		Height(len(all) + 3).
		Value(&selected)

	if err := runForm(field); err != nil {
		return nil, err
	}
	return selected, nil
}

// ScopeSelection is the outcome of SelectScope. Global is true when the user
// chose the agents' user-level scope; otherwise the link is project-level and
// Path is the base directory it is rooted at (the current directory for the
// "Local" choice, or a directory the user typed for the "Custom path" choice).
// Copy is set for a project-level selection when the user chose to vendor a real
// copy (committed to git) instead of a symlink into Home; it is always false for
// a Global selection.
type ScopeSelection struct {
	Global bool
	Path   string
	Copy   bool
}

// errNonInteractiveScope is returned by SelectScope on a non-TTY: there is no
// way to prompt, so the caller is told which flags pick a scope explicitly.
var errNonInteractiveScope = errors.New("non-interactive: pass --global or --local")

// SelectScope asks where a skill should be linked: Global (the agents'
// user-level folders), Local (the current directory's project folders), or a
// custom directory typed with Tab path-completion. cwd is the current working
// directory; it labels the Local choice and seeds the custom-path input so
// completion is useful from the first keystroke. On a non-TTY it refuses and
// names the --global/--local escape hatch.
func SelectScope(cwd string) (ScopeSelection, error) {
	if !IsTTY() {
		return ScopeSelection{}, errNonInteractiveScope
	}

	const (
		choiceGlobal = "global"
		choiceLocal  = "local"
		choicePath   = "path"
	)

	// Show the current directory's full path on the Local option: a bare "./"
	// hides which folder the user is actually in.
	localLabel := "Local — this folder (" + filepath.Join(cwd, ".<agent>", "skills") + ")"

	var choice string
	// Height must be set explicitly — see SelectSkills for why huh v2 collapses
	// a static-Options field to zero height otherwise.
	sel := huh.NewSelect[string]().
		Title("Where should this skill be linked?").
		Options(
			huh.NewOption("Global — every project (~/.<agent>/skills)", choiceGlobal),
			huh.NewOption(localLabel, choiceLocal),
			huh.NewOption("Custom path…", choicePath),
		).
		Height(5).
		Value(&choice)
	if err := runForm(sel); err != nil {
		return ScopeSelection{}, err
	}

	switch choice {
	case choiceGlobal:
		return ScopeSelection{Global: true}, nil
	case choiceLocal:
		vendor, err := selectVendorMethod()
		if err != nil {
			return ScopeSelection{}, err
		}
		return ScopeSelection{Path: cwd, Copy: vendor}, nil
	case choicePath:
		path, err := selectPath(cwd)
		if err != nil {
			return ScopeSelection{}, err
		}
		vendor, err := selectVendorMethod()
		if err != nil {
			return ScopeSelection{}, err
		}
		return ScopeSelection{Path: path, Copy: vendor}, nil
	default:
		return ScopeSelection{}, errors.New("no scope selected")
	}
}

// selectVendorMethod asks, for a project-level install, whether to symlink the
// skill into Home (the default — not committable to git) or to write a real
// copy into the project (committable, shareable with teammates). The cursor
// starts on the safe Symlink choice, but the user always picks. It is only
// reached on a TTY (SelectScope already refused otherwise).
func selectVendorMethod() (bool, error) {
	const (
		choiceLink = "link"
		choiceCopy = "copy"
	)
	choice := choiceLink
	sel := huh.NewSelect[string]().
		Title("How should it be installed here?").
		Description("A copy can be committed to the project's git repo; a symlink cannot.").
		Options(
			huh.NewOption("Symlink — (best for personal use)", choiceLink),
			huh.NewOption("Copy the files in — (best for git)", choiceCopy),
		).
		Height(4).
		Value(&choice)
	if err := runForm(sel); err != nil {
		return false, err
	}
	return choice == choiceCopy, nil
}

// selectPath prompts for a directory with Tab path-completion. It seeds the
// input with cwd (so the first Tab lists its subdirectories) and validates that
// the chosen path is an existing directory. The returned path is tilde-expanded
// and cleaned so the linker can use it directly.
func selectPath(cwd string) (string, error) {
	value := withTrailingSep(cwd)
	field := huh.NewInput().
		Title("Directory to link into").
		Description("Type a path; press Tab to complete. Links go in <path>/.<agent>/skills.").
		Value(&value).
		// Bind the suggestions to the input's own value so they refresh on every
		// keystroke (huh re-evaluates the func whenever the binding's hash changes).
		SuggestionsFunc(func() []string { return dirSuggestions(value) }, &value).
		Validate(validateDir)
	if err := runForm(field); err != nil {
		return "", err
	}
	return filepath.Clean(expandTilde(strings.TrimSpace(value))), nil
}

// dirSuggestions returns the subdirectory paths that extend the partial path
// typed so far, for the path input's Tab-completion. It lists the directory
// holding the partial leaf (or the partial path itself when it ends in a
// separator) and keeps the subdirectories whose full path has the typed value
// as a prefix — the match rule bubbles' textinput applies. It is best-effort:
// an unreadable or missing directory yields no suggestions.
func dirSuggestions(partial string) []string {
	partial = strings.TrimSpace(partial)

	dir := partial
	if !strings.HasSuffix(partial, string(os.PathSeparator)) {
		dir = filepath.Dir(partial)
	}
	if dir == "" {
		dir = "."
	}

	entries, err := os.ReadDir(expandTilde(dir))
	if err != nil {
		return nil
	}

	lowerPartial := strings.ToLower(partial)
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		cand := filepath.Join(dir, e.Name())
		if strings.HasPrefix(strings.ToLower(cand), lowerPartial) {
			out = append(out, cand)
		}
	}
	return out
}

// validateDir is the path input's validator: the trimmed value must name an
// existing directory (tilde-expanded for the check).
func validateDir(p string) error {
	p = strings.TrimSpace(p)
	if p == "" {
		return errors.New("enter a directory path")
	}
	info, err := os.Stat(expandTilde(p))
	if err != nil {
		return fmt.Errorf("%s: not found", p)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", p)
	}
	return nil
}

// expandTilde replaces a leading "~" (alone or as "~/…") with the user's home
// directory so typed paths resolve like the shell does. Other paths and an
// unavailable home are returned unchanged.
func expandTilde(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~"+string(os.PathSeparator)) {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == "~" {
		return home
	}
	return filepath.Join(home, p[2:])
}

// withTrailingSep ensures p ends in a path separator (unless empty), so the
// path input starts ready to list the directory's contents on the first Tab.
func withTrailingSep(p string) string {
	if p == "" || strings.HasSuffix(p, string(os.PathSeparator)) {
		return p
	}
	return p + string(os.PathSeparator)
}

// Confirm asks a yes/no question and returns the answer. On a non-TTY it
// refuses with a message telling the caller to pass --yes to skip the prompt.
func Confirm(prompt string) (bool, error) {
	if !IsTTY() {
		return false, errors.New("non-interactive: pass --yes to confirm")
	}

	var answer bool
	field := huh.NewConfirm().
		Title(prompt).
		Affirmative("Yes").
		Negative("No").
		Value(&answer)

	if err := runForm(field); err != nil {
		return false, err
	}
	return answer, nil
}

// runForm assembles a single-field form and runs it against the terminal.
// huh renders to stderr by default; we keep that so stdout stays reserved for
// machine-readable command output.
func runForm(field huh.Field) error {
	form := huh.NewForm(huh.NewGroup(field)).WithOutput(os.Stderr)
	return form.Run()
}
