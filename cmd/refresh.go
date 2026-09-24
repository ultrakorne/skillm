package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ultrakorne/skillm/internal/config"
	"github.com/ultrakorne/skillm/internal/core"
	"github.com/ultrakorne/skillm/internal/protocol"
	"github.com/ultrakorne/skillm/internal/status"
	"github.com/ultrakorne/skillm/internal/ui"
)

func init() {
	rootCmd.AddCommand(newRefreshCmd(), newStatusCmd())
}

// refreshFlagIfDue backs `refresh --if-due`.
var refreshFlagIfDue bool

func newRefreshCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "refresh",
		Short: "Check for skill and skillm updates and cache the result",
		Long: "Refresh runs `skillm check` and `skillm upgrade --check` together and writes " +
			"the outcome to ~/.skillm/status.json, the cache every skillm GUI shows its " +
			"update badge from (read it with `skillm status`). The badge is on when a skill " +
			"has an update or a newer skillm release exists; a skill whose check failed is " +
			"reported as an error, never as current. `skillm update`, `install`, `uninstall` " +
			"and `upgrade` keep the cache in line with what they change, so the badge clears " +
			"without another check.\n\n" +
			"With --if-due it checks only when a scheduled check is due: refresh.enabled is " +
			"on in config.toml and refresh.interval_hours have passed since the last check " +
			"(an hour, when every lookup of the last one failed). Otherwise it changes " +
			"nothing and makes no network request. A GUI or a timer runs `skillm refresh " +
			"--if-due` as often as it likes; skillm decides.",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{annotationJSON: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRefresh(cmd.Context(), refreshFlagIfDue)
		},
	}
	c.Flags().BoolVar(&refreshFlagIfDue, "if-due", false, "check only when a scheduled check is due")
	return c
}

func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the cached update status from the last refresh",
		Long: "Status prints what the last `skillm refresh` found, offline: which skills have " +
			"updates, whether a newer skillm exists, and the problems it hit. It is stale " +
			"when it is older than refresh.interval_hours.",
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			// Reading the cache needs no git.
			annotationSkipGitCheck: "true",
			annotationJSON:         "true",
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStatus()
		},
	}
}

func runRefresh(ctx context.Context, ifDue bool) error {
	opts, err := coreOptions(false)
	if err != nil {
		return err
	}
	// The checks run unlocked; only the cache write waits for Home's lock.
	opts.Lock = func(ctx context.Context) (func(), error) {
		return lockHome(ctx, opts.Home, "skillm refresh")
	}
	req := core.RefreshRequest{Version: Version(), Now: time.Now(), IfDue: ifDue}
	if flagJSON {
		out := jsonOut()
		res, err := core.Refresh(ctx, opts, out, req)
		if err != nil {
			return err
		}
		return out.Result(protocol.NewRefreshData(res))
	}

	// The per-skill lines are check's business, and printStatus names a
	// failed self lookup; refresh prints the summary.
	res, err := core.Refresh(ctx, opts, dropCodes{rep: termLog, codes: []string{core.CodeSelfCheckFailed}}, req)
	if err != nil {
		return err
	}
	if !res.Refreshed {
		cfg, err := config.Load(opts.Home)
		if err != nil {
			return err
		}
		if !cfg.RefreshEnabled() {
			fmt.Fprintln(os.Stdout, "No check due: scheduled checks are off (refresh.enabled).")
			return nil
		}
		interval := time.Duration(cfg.RefreshIntervalHours()) * time.Hour
		fmt.Fprintf(os.Stdout, "No check due before %s.\n", localTime(status.DueAt(res.Status, interval)))
		return nil
	}
	printStatus(res)
	return nil
}

func runStatus() error {
	opts, err := coreOptions(false)
	if err != nil {
		return err
	}
	if flagJSON {
		out := jsonOut()
		res, err := core.ReadStatus(opts, out, Version(), time.Now())
		if err != nil {
			return err
		}
		return out.Result(protocol.NewStatusData(res))
	}
	res, err := core.ReadStatus(opts, termLog, Version(), time.Now())
	if err != nil {
		return err
	}
	printStatus(res)
	return nil
}

// printStatus renders the refresh cache on the terminal.
func printStatus(res core.StatusResult) {
	f := res.Status
	if f.CheckedAt.IsZero() {
		fmt.Fprintln(os.Stdout, "Never checked; run `skillm refresh`.")
		return
	}
	fmt.Fprintf(os.Stdout, "Checked %s.\n", localTime(f.CheckedAt))
	var updates []string
	for _, s := range f.Skills {
		if s.Status == status.SkillUpdateAvailable {
			updates = append(updates, s.ID)
		}
	}
	switch len(updates) {
	case 0:
	case 1:
		ui.Warnf("1 skill has an update available: %s", updates[0])
	default:
		ui.Warnf("%d skills have updates available: %s", len(updates), strings.Join(updates, ", "))
	}
	if f.Self != nil && f.Self.Available {
		ui.Warnf("skillm %s is available (you have %s).", f.Self.Latest, f.Self.Current)
	}
	for _, p := range f.Errors {
		switch {
		case p.Code == status.CodeSelfCheck:
			ui.Errorf("could not look up the latest skillm release: %s", p.Message)
		case p.Code == status.SkillUntracked:
			ui.Errorf("%s: its subdir was not found upstream", p.SkillID)
		default:
			ui.Errorf("%s: could not read upstream: %s", p.SkillID, p.Message)
		}
	}
	switch {
	case len(updates) > 0:
		ui.Hintf("Run `skillm update` to apply the skill updates.")
	case !f.Badge && len(f.Errors) == 0:
		ui.Successf("Everything is up to date.")
	}
	if res.Stale {
		ui.Hintf("This is older than the refresh interval; run `skillm refresh` to check again.")
	}
}

// localTime renders t for the terminal, in local time.
func localTime(t time.Time) string {
	return t.Local().Format("2006-01-02 15:04")
}
