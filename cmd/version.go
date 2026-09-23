package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/ultrakorne/skillm/internal/protocol"
)

func init() {
	rootCmd.AddCommand(newVersionCmd())
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print skillm's version",
		Long: "Version prints the running skillm's version, as `skillm --version` does. " +
			"With --json it also reports the JSON protocol's api_version and the " +
			"commands that have a JSON mode, so a GUI can refuse a CLI it does not " +
			"understand.",
		Args: cobra.NoArgs,
		Annotations: map[string]string{
			// Printing the version needs no git; a GUI runs this first,
			// before it can tell the user git is missing.
			annotationSkipGitCheck: "true",
			annotationJSON:         "true",
		},
		RunE: func(c *cobra.Command, args []string) error {
			if flagJSON {
				return jsonOut().Result(protocol.VersionData{
					Version:      version,
					APIVersion:   protocol.APIVersion,
					Capabilities: jsonCapabilities(),
				})
			}
			// The same line as `skillm --version`.
			fmt.Fprintf(os.Stdout, "%s version %s\n", c.Root().Name(), c.Root().Version)
			return nil
		},
	}
}
