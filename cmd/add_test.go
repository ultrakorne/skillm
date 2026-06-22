package cmd

import (
	"testing"

	"github.com/ultrakorne/skillm/internal/skill"
	"github.com/ultrakorne/skillm/internal/source"
)

// TestNewAddCmdWiring verifies the command's argument arity and the flags add
// declares, so the cobra wiring stays in sync with PLAN §3's surface. add is
// strictly fetch-only, so it carries no scope/copy flags:
//
//	skillm add <url|local-path> [skill_id] [--as] [--ref] [--all]
func TestNewAddCmdWiring(t *testing.T) {
	c := newAddCmd()

	if c.Use == "" || c.Args == nil || c.RunE == nil {
		t.Fatalf("add command not fully configured: Use=%q Args set=%t RunE set=%t",
			c.Use, c.Args != nil, c.RunE != nil)
	}

	// Args: 1 (source) or 2 (source + skill_id); 0 or 3 should be rejected.
	argCases := []struct {
		name string
		args []string
		ok   bool
	}{
		{"zero args", []string{}, false},
		{"one arg", []string{"https://example.com/repo.git"}, true},
		{"two args", []string{"https://example.com/repo.git", "my-skill"}, true},
		{"three args", []string{"a", "b", "c"}, false},
	}
	for _, tc := range argCases {
		t.Run("args/"+tc.name, func(t *testing.T) {
			err := c.Args(c, tc.args)
			if tc.ok && err != nil {
				t.Fatalf("expected args %v to validate, got %v", tc.args, err)
			}
			if !tc.ok && err == nil {
				t.Fatalf("expected args %v to be rejected", tc.args)
			}
		})
	}

	for _, name := range []string{"as", "ref", "all"} {
		if c.Flags().Lookup(name) == nil {
			t.Errorf("expected --%s flag to be registered", name)
		}
	}
	// add is fetch-only: the scope/copy flags must NOT exist on it.
	for _, name := range []string{"global", "local", "copy"} {
		if c.Flags().Lookup(name) != nil {
			t.Errorf("add must not declare --%s (it is fetch-only)", name)
		}
	}
}

func TestSelectFound(t *testing.T) {
	mk := func(id string) source.Found {
		return source.Found{Id: id, Dir: "/tmp/" + id, Skill: &skill.Skill{ID: id, Name: id}}
	}
	multi := []source.Found{mk("alpha"), mk("beta"), mk("gamma")}
	single := []source.Found{mk("solo")}

	cases := []struct {
		name       string
		found      []source.Found
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
			got, err := selectFound(tc.found, tc.selectArgs, tc.all)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %v", ids(got))
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			gotIDs := ids(got)
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

func TestRepoRelSubpath(t *testing.T) {
	cases := []struct {
		repo string
		dir  string
		want string
	}{
		{"/tmp/repo", "/tmp/repo", ""},
		{"/tmp/repo", "/tmp/repo/skills/foo", "skills/foo"},
		{"/tmp/repo", "/tmp/repo/foo", "foo"},
	}
	for _, tc := range cases {
		if got := repoRelSubpath(tc.repo, tc.dir); got != tc.want {
			t.Errorf("repoRelSubpath(%q,%q) = %q, want %q", tc.repo, tc.dir, got, tc.want)
		}
	}
}

func ids(found []source.Found) []string {
	out := make([]string, 0, len(found))
	for _, f := range found {
		out = append(out, f.Id)
	}
	return out
}
