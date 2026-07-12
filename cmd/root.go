// Package cmd holds skillm's cobra commands. The root command lives here; every
// subcommand lives in its own file and registers itself with the root via an
// init() that calls rootCmd.AddCommand(...). root.go never references concrete
// subcommands, so subcommand files are added independently.
package cmd

import (
	"errors"
	"fmt"
	"os/exec"

	"github.com/spf13/cobra"
)

// version is the build version, overridable at link time:
//
//	go build -ldflags "-X github.com/ultrakorne/skillm/cmd.version=v0.1.0"
var version = "dev"

// Version returns the build version (used by main.go to configure fang).
func Version() string { return version }

// Global persistent flags, bound on the root command and readable by every
// subcommand file in this package:
//
//	flagForce — --force: skip safety refusals / overwrite where the command allows it.
//	flagYes   — --yes:   assume "yes" to confirmation prompts (non-interactive).
//	flagHome  — --home:  override the Home directory (default ~/.skillm). Empty means default.
var (
	flagForce bool
	flagYes   bool
	flagHome  string
)

// rootCmd is the skillm root command. Subcommands attach themselves to it from
// their own files via init().
var rootCmd = newRootCmd()

// Root returns the fully assembled root command (with every subcommand that has
// registered itself via init). main.go hands this to fang.Execute.
func Root() *cobra.Command { return rootCmd }

func newRootCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "skillm",
		Short: "Manage AI-agent skills from a single central Home",
		Long: "skillm keeps every AI-agent skill in one central Home and links them " +
			"(via symlinks) into the skill folders that agents read. It seeds definitions " +
			"for Claude and for the cross-agent .agents/skills folder (read by Codex, " +
			"Cursor, Amp, Gemini CLI, and more); any other agent can be added by defining " +
			"its folders in config.toml.",
		Version: version,
		// Quiet cobra's own error/usage printing; fang renders errors.
		SilenceErrors: true,
		SilenceUsage:  true,
		// Verify the runtime prerequisites before any command runs, then give
		// the one-time config migration a chance (cheap when there is nothing
		// to migrate — see migrate.go).
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if err := checkGit(); err != nil {
				return err
			}
			return migrateDeadAgentDirs()
		},
	}

	pf := c.PersistentFlags()
	pf.BoolVar(&flagForce, "force", false, "skip confirmations and safety refusals")
	pf.BoolVar(&flagYes, "yes", false, "assume yes to confirmation prompts")
	pf.StringVar(&flagHome, "home", "", "override the Home directory (default ~/.skillm)")

	return c
}

// checkGit ensures the system git binary is available on PATH, returning a
// friendly error if it is not. System git is required by skillm at runtime.
func checkGit() error {
	if _, err := exec.LookPath("git"); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return errors.New("git was not found on your PATH; skillm requires the system git binary — install git and try again")
		}
		return fmt.Errorf("could not locate the git binary: %w", err)
	}
	return nil
}
