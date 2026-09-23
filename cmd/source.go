package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ultrakorne/skillm/internal/core"
	"github.com/ultrakorne/skillm/internal/protocol"
	"github.com/ultrakorne/skillm/internal/state"
)

var sourceInspectFlagRef string

func init() {
	rootCmd.AddCommand(newSourceCmd())
}

func newSourceCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "source",
		Short: "Look at a Source without installing from it",
	}
	c.AddCommand(newSourceInspectCmd())
	return c
}

func newSourceInspectCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "inspect <source>",
		Short: "List the skills a Source holds, without installing anything",
		Long: "source inspect reads a Source once — a git repository (URL, GitHub owner/repo " +
			"shorthand, or a path to a repository) or a local directory — and lists the " +
			"skills it holds: id, name, description and where each lives in the Source. " +
			"It prompts for nothing and writes nothing outside a temporary directory, which " +
			"it removes.\n\n" +
			"For git it also reports the ref an install would record (--ref, or the default " +
			"branch) and the commit it inspected. To install exactly what was shown, pass " +
			"that commit to install: `skillm install <source> <ids…> --ref <ref> --commit " +
			"<commit>`; the install fails with code commit_mismatch if the ref has moved on " +
			"since. A GUI should pass an absolute path for a local Source.",
		Args:        cobra.ExactArgs(1),
		Annotations: map[string]string{annotationJSON: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSourceInspect(cmd, args[0], sourceInspectFlagRef)
		},
	}
	c.Flags().StringVar(&sourceInspectFlagRef, "ref", "", "inspect this branch, tag, or commit of a git source (default: its default branch)")
	return c
}

func runSourceInspect(cmd *cobra.Command, src, ref string) error {
	opts, err := coreOptions(false)
	if err != nil {
		return err
	}
	// The working directory resolves a relative Source only; a run that
	// names its Source absolutely (as a GUI does) works without one.
	if cwd, err := os.Getwd(); err == nil {
		opts.Cwd = cwd
	}
	insp, err := core.Inspect(cmd.Context(), opts, src, ref)
	if err != nil {
		return err
	}
	defer insp.Close()
	if insp.Kind == state.KindLocal && ref != "" {
		return usageError("the --ref flag only applies to a git source")
	}
	if flagJSON {
		return jsonOut().Result(protocol.NewInspectData(insp))
	}

	w := os.Stdout
	fmt.Fprintf(w, "Source: %s (%s)\n", insp.Source, insp.Kind)
	if insp.Kind == state.KindGit {
		fmt.Fprintf(w, "Ref:    %s at commit %s\n", insp.Ref, insp.Commit)
	}
	fmt.Fprintf(w, "Skills: %d\n", len(insp.Skills))
	for _, s := range insp.Skills {
		line := "  " + s.ID
		if s.Description != "" {
			line += " — " + strings.TrimSpace(s.Description)
		}
		fmt.Fprintln(w, line)
	}
	return nil
}
