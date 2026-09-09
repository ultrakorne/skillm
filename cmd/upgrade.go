package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ultrakorne/skillm/internal/selfupdate"
	"github.com/ultrakorne/skillm/internal/ui"
)

func init() {
	rootCmd.AddCommand(newUpgradeCmd())
}

// upgradeFlagCheck backs `upgrade --check`: report what is available and exit
// without touching the binary.
var upgradeFlagCheck bool

func newUpgradeCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "upgrade",
		Short: "Upgrade skillm itself to the latest release",
		Long: "Upgrade replaces the running skillm binary with the latest published " +
			"release. It asks GitHub for the newest release, and when that is newer than " +
			"the running build it downloads the archive for this platform, verifies it " +
			"against the release's checksums.txt, and swaps the new binary into the path " +
			"skillm is running from — resolving a symlinked install to the real file. " +
			"Nothing is written until the checksum matches, and the previous binary is " +
			"restored if the swap fails. This upgrades the CLI only; use `skillm update` " +
			"to pull new revisions of installed skills. A binary built from source (its " +
			"version is not a release tag) corresponds to no release and is left alone.",
		Args: cobra.NoArgs,
		// Upgrading the CLI needs no git, unlike every other command.
		Annotations: map[string]string{annotationSkipGitCheck: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUpgrade(cmd.Context(), upgradeFlagCheck)
		},
	}
	c.Flags().BoolVar(&upgradeFlagCheck, "check", false, "report whether a newer release exists without installing it")
	return c
}

func runUpgrade(ctx context.Context, checkOnly bool) error {
	current := Version()
	if !selfupdate.IsReleaseVersion(current) {
		// A source build has no release to compare against; say so plainly
		// rather than offering to overwrite the user's own binary.
		ui.Warnf("skillm %s is built from source, so there is no release to upgrade to.", selfupdate.Display(current))
		ui.Hintf("Install a release with the installer in the README, or `go install github.com/ultrakorne/skillm@latest`.")
		return nil
	}

	rel, err := selfupdate.Latest(ctx, current)
	if err != nil {
		return fmt.Errorf("check for a newer skillm: %w", err)
	}

	if !selfupdate.IsNewer(rel.Tag, current) {
		ui.Successf("skillm %s is the latest release.", selfupdate.Display(current))
		return nil
	}

	from, to := selfupdate.Display(current), selfupdate.Display(rel.Tag)
	if checkOnly {
		ui.Warnf("skillm %s is available (you have %s).", to, from)
		ui.Hintf("Run `skillm upgrade` to install it.")
		return nil
	}

	if ui.IsTTY() && !flagYes && !flagForce {
		ok, err := ui.Confirm(fmt.Sprintf("Upgrade skillm %s → %s?", from, to))
		if err != nil {
			return err
		}
		if !ok {
			ui.Warnf("Upgrade cancelled; still on %s.", from)
			return nil
		}
	}

	path, err := selfupdate.Apply(ctx, rel, current)
	if err != nil {
		return fmt.Errorf("upgrade to %s: %w", to, err)
	}
	ui.Successf("Upgraded skillm %s → %s (%s).", from, to, path)
	return nil
}
