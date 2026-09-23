package protocol

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/ultrakorne/skillm/internal/core"
	"github.com/ultrakorne/skillm/internal/linker"
	"github.com/ultrakorne/skillm/internal/store"
)

// TestErrorFrom pins the error → code mapping, including wrapped errors and
// the fields each code carries.
func TestErrorFrom(t *testing.T) {
	if ErrorFrom(nil) != nil {
		t.Fatal("ErrorFrom(nil) != nil")
	}
	own := &Error{Code: CodeGitMissing, Message: "git missing"}
	cases := []struct {
		name      string
		err       error
		code      string
		skill     string
		path      string
		paths     []string
		retryable bool
	}{
		{"protocol error as is", fmt.Errorf("wrapped: %w", own), CodeGitMissing, "", "", nil, false},
		{"plain", errors.New("boom"), CodeError, "", "", nil, false},
		{"cancelled", fmt.Errorf("check: %w", context.Canceled), CodeCancelled, "", "", nil, true},
		{"deadline", context.DeadlineExceeded, CodeTimeout, "", "", nil, true},
		{"home locked", &store.LockTimeoutError{Path: "/h/.lock"}, CodeHomeLocked, "", "/h/.lock", nil, true},
		{"foreign files", &core.ForeignFilesError{Paths: []string{"/a", "/b"}}, CodeForeignFiles, "", "", []string{"/a", "/b"}, false},
		{"scope changed", &core.UninstallScopeChangedError{Roots: []string{"/p"}}, CodeNeedsConfirm, "", "", []string{"/p"}, false},
		{"blocked by foreign", &core.UninstallBlockedError{ID: "x", Err: fmt.Errorf("refusing: %w", linker.ErrNotManaged)}, CodeNeedsForce, "x", "", nil, false},
		{"blocked by io", &core.UninstallBlockedError{ID: "x", Err: errors.New("permission denied")}, CodeUninstallFailed, "x", "", nil, false},
		{"collision", &core.SourceCollisionError{ID: "x"}, CodeSourceCollision, "x", "", nil, false},
		{"as multiple", core.ErrAsMultiple, CodeAsMultiple, "", "", nil, false},
		{"aliased", &core.LocalScopeAliasedError{Base: "/Users/me"}, CodeLocalScopeAliased, "", "/Users/me", nil, false},
		{"commit mismatch", &core.CommitMismatchError{Want: "a", Got: "b"}, CodeCommitMismatch, "", "", nil, false},
		{"unknown skill", &core.UnknownSkillError{ID: "x"}, CodeNotInstalled, "x", "", nil, false},
		{"not installed one", &core.NotInstalledError{IDs: []string{"x"}}, CodeNotInstalled, "x", "", nil, false},
		{"not installed many", &core.NotInstalledError{IDs: []string{"x", "y"}}, CodeNotInstalled, "", "", nil, false},
		{"update failed", &core.UpdateFailedError{Failures: []string{"x: boom"}}, CodeUpdateFailed, "", "", nil, false},
		{"no agent", core.ErrNoAgentEnabled, CodeNoAgentEnabled, "", "", nil, false},
		{"unknown agent", &core.UnknownAgentError{Names: []string{"z"}}, CodeUnknownAgent, "", "", nil, false},
		{"managed by app", core.ErrManagedByApp, CodeManagedByApp, "", "", nil, false},
		{"source build", core.ErrSourceBuild, CodeSourceBuild, "", "", nil, false},
		{"no upgrade", core.ErrNoUpgrade, CodeNoUpgrade, "", "", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ErrorFrom(tc.err)
			if got.Code != tc.code || got.SkillID != tc.skill || got.Path != tc.path ||
				!slices.Equal(got.Paths, tc.paths) || got.Retryable != tc.retryable {
				t.Fatalf("ErrorFrom(%v) = %+v", tc.err, got)
			}
			if got.Message != tc.err.Error() && got != own {
				t.Fatalf("message = %q, want %q", got.Message, tc.err.Error())
			}
		})
	}
}
