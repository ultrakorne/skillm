package ui

import (
	"errors"
	"os"

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
