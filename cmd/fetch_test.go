package cmd

import (
	"testing"

	"github.com/ultrakorne/skillm/internal/core"
)

func TestSelectFound(t *testing.T) {
	mk := func(id string) core.InspectedSkill {
		return core.InspectedSkill{ID: id, Name: id, Path: "/tmp/" + id}
	}
	multi := []core.InspectedSkill{mk("alpha"), mk("beta"), mk("gamma")}
	single := []core.InspectedSkill{mk("solo")}

	cases := []struct {
		name       string
		found      []core.InspectedSkill
		selectArgs []string
		all        bool
		wantIDs    []string
		wantErr    bool
	}{
		{"single auto-selects without prompt", single, nil, false, []string{"solo"}, false},
		{"explicit id selects that one", multi, []string{"beta"}, false, []string{"beta"}, false},
		{"multiple ids select those, in discovery order", multi, []string{"gamma", "alpha"}, false, []string{"alpha", "gamma"}, false},
		{"--all selects everything", multi, nil, true, []string{"alpha", "beta", "gamma"}, false},
		{"unknown id errors", multi, []string{"nope"}, false, nil, true},
		{"one unknown among known errors (atomic)", multi, []string{"alpha", "nope"}, false, nil, true},
		{"single with matching id", single, []string{"solo"}, false, []string{"solo"}, false},
		{"single with mismatched id errors", single, []string{"other"}, false, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotIDs, err := selectFound(&core.Inspection{Skills: tc.found}, tc.selectArgs, tc.all)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %v", gotIDs)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(gotIDs) != len(tc.wantIDs) {
				t.Fatalf("ids = %v, want %v", gotIDs, tc.wantIDs)
			}
			for i := range gotIDs {
				if gotIDs[i] != tc.wantIDs[i] {
					t.Fatalf("ids = %v, want %v", gotIDs, tc.wantIDs)
				}
			}
		})
	}
}
