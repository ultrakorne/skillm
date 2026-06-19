package cmd

import (
	"testing"

	"github.com/ultrakorne/skillm/internal/agentdir"
	"github.com/ultrakorne/skillm/internal/skill"
	"github.com/ultrakorne/skillm/internal/source"
)

// TestNewAddCmdWiring verifies the command's argument arity and the flags add
// declares, so the cobra wiring stays in sync with PLAN §3's surface:
//
//	skillm add <url|local-path> [skill_id] [--as] [--ref] [--all] [--global|--local]
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

	for _, name := range []string{"as", "ref", "all", "global", "local"} {
		if c.Flags().Lookup(name) == nil {
			t.Errorf("expected --%s flag to be registered", name)
		}
	}
}

func TestAddLinkScope(t *testing.T) {
	cases := []struct {
		name       string
		global     bool
		local      bool
		copy       bool
		wantScope  agentdir.Scope
		wantDoLink bool
		wantVendor bool
		wantErr    bool
	}{
		{"bare add is fetch-only", false, false, false, agentdir.Global, false, false, false},
		{"--global links global", true, false, false, agentdir.Global, true, false, false},
		{"--local links local", false, true, false, agentdir.Local, true, false, false},
		{"--copy implies local vendor", false, false, true, agentdir.Local, true, true, false},
		{"--local --copy vendors", false, true, true, agentdir.Local, true, true, false},
		{"--global --copy is an error", true, false, true, agentdir.Global, false, false, true},
		{"both global and local is an error", true, true, false, agentdir.Global, false, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// addLinkScope reads the package-level flag vars.
			addGlobal, addLocal, addCopy = tc.global, tc.local, tc.copy
			t.Cleanup(func() { addGlobal, addLocal, addCopy = false, false, false })

			scope, doLink, vendor, err := addLinkScope()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for global=%v local=%v copy=%v", tc.global, tc.local, tc.copy)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if scope != tc.wantScope {
				t.Errorf("scope = %v, want %v", scope, tc.wantScope)
			}
			if doLink != tc.wantDoLink {
				t.Errorf("doLink = %v, want %v", doLink, tc.wantDoLink)
			}
			if vendor != tc.wantVendor {
				t.Errorf("vendor = %v, want %v", vendor, tc.wantVendor)
			}
		})
	}
}

func TestSelectFound(t *testing.T) {
	mk := func(id string) source.Found {
		return source.Found{Id: id, Dir: "/tmp/" + id, Skill: &skill.Skill{ID: id, Name: id}}
	}
	multi := []source.Found{mk("alpha"), mk("beta"), mk("gamma")}
	single := []source.Found{mk("solo")}

	cases := []struct {
		name      string
		found     []source.Found
		selectArg string
		all       bool
		wantIDs   []string
		wantErr   bool
	}{
		{"single auto-selects without prompt", single, "", false, []string{"solo"}, false},
		{"explicit id selects that one", multi, "beta", false, []string{"beta"}, false},
		{"--all selects everything", multi, "", true, []string{"alpha", "beta", "gamma"}, false},
		{"unknown id errors", multi, "nope", false, nil, true},
		{"single with matching id", single, "solo", false, []string{"solo"}, false},
		{"single with mismatched id errors", single, "other", false, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			addAll = tc.all
			t.Cleanup(func() { addAll = false })

			got, err := selectFound(tc.found, tc.selectArg)
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
