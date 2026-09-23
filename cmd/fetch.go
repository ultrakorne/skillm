package cmd

import (
	"github.com/ultrakorne/skillm/internal/core"
	"github.com/ultrakorne/skillm/internal/ui"
)

// selectFound resolves which of an inspected Source's skills to install,
// returning their ids in discovery order.
//
//   - selectArgs given: exactly those skills; an id that is not in the source
//     is an error naming all the unknown ones (atomic).
//   - exactly one skill found (and no selectArgs): that one, without prompting.
//   - --all given: every discovered skill.
//   - otherwise: the interactive picker (which refuses on a non-TTY with a
//     message naming skill_id / --all). An empty pick returns no ids.
func selectFound(insp *core.Inspection, selectArgs []string, all bool) ([]string, error) {
	if len(selectArgs) > 0 {
		chosen, err := insp.Select(selectArgs)
		if err != nil {
			return nil, err
		}
		return inspectedIDs(chosen), nil
	}
	if len(insp.Skills) == 1 || all {
		return inspectedIDs(insp.Skills), nil
	}

	opts := make([]ui.Option, 0, len(insp.Skills))
	for _, s := range insp.Skills {
		// Label with the skill id only: descriptions wrap to the next line in
		// the picker and clutter the selection.
		opts = append(opts, ui.Option{Label: s.ID, Value: s.ID})
	}
	ids, err := ui.SelectSkills("Select skills to install", opts)
	if err != nil {
		return nil, err
	}
	// Keep discovery order, whatever order the picker returns.
	chosen, err := insp.Select(ids)
	if err != nil {
		return nil, err
	}
	return inspectedIDs(chosen), nil
}

// inspectedIDs returns the ids of skills, in order.
func inspectedIDs(skills []core.InspectedSkill) []string {
	ids := make([]string, 0, len(skills))
	for _, s := range skills {
		ids = append(ids, s.ID)
	}
	return ids
}
