package cmd

import (
	"context"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/charmbracelet/fang"
	"github.com/spf13/cobra"

	"github.com/ultrakorne/skillm/internal/protocol"
)

// annotationJSON marks a command that has a JSON mode (see internal/protocol).
// `--json` on any other command is refused with code "json_unsupported", so
// a GUI never gets terminal output where it expects JSON. The annotated
// commands are also the "capabilities" `skillm version --json` reports.
const annotationJSON = "skillm:json"

// Global protocol flags, bound on the root command:
//
//	flagJSON   — --json:   write the JSON protocol to stdout instead of terminal output.
//	flagEvents — --events: with --json, stream NDJSON events, then a result line.
var (
	flagJSON   bool
	flagEvents bool
)

// Execute runs the root command through fang. In JSON mode an error is
// written to stdout as the protocol's error envelope instead of fang's styled
// message on stderr; the returned error still makes the process exit non-zero.
func Execute(ctx context.Context) error {
	return fang.Execute(ctx, rootCmd,
		fang.WithVersion(version),
		fang.WithErrorHandler(handleError))
}

// handleError is fang's error handler: the error envelope in JSON mode, fang's
// own rendering otherwise.
func handleError(w io.Writer, styles fang.Styles, err error) {
	if !jsonMode() {
		fang.DefaultErrorHandler(w, styles, err)
		return
	}
	if isUsageError(err) {
		err = &protocol.Error{Code: protocol.CodeUsage, Message: err.Error()}
	}
	_ = jsonOut().Fail(err)
}

// checkJSONFlags refuses --events without --json, and --json on a command
// that has no JSON mode.
func checkJSONFlags(c *cobra.Command) error {
	if flagEvents && !flagJSON {
		return &protocol.Error{Code: protocol.CodeUsage, Message: "--events requires --json"}
	}
	if flagJSON && c.Annotations[annotationJSON] != "true" {
		return &protocol.Error{
			Code:    protocol.CodeJSONUnsupported,
			Message: "`" + c.CommandPath() + "` has no JSON mode",
		}
	}
	return nil
}

// jsonMode reports whether this run writes the JSON protocol. It also looks at
// the raw arguments, so an error cobra hit before it parsed --json (an
// unknown flag in front of it, say) is still reported as JSON.
func jsonMode() bool {
	return flagJSON || argFlag(os.Args[1:], "json")
}

// argFlag reports whether args set the boolean flag --name (as "--name" or
// "--name=<true>") before any "--".
func argFlag(args []string, name string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		if a == "--"+name {
			return true
		}
		if v, ok := strings.CutPrefix(a, "--"+name+"="); ok {
			if b, err := strconv.ParseBool(v); err == nil && b {
				return true
			}
		}
	}
	return false
}

var (
	jsonOnce   sync.Once
	jsonWriter *protocol.Writer
)

// jsonOut is this run's protocol writer on stdout (an NDJSON stream with
// --events). It is the core.Reporter of a command in JSON mode, and the
// command ends by calling its Result; handleError calls Fail.
func jsonOut() *protocol.Writer {
	jsonOnce.Do(func() {
		jsonWriter = protocol.NewWriter(os.Stdout, flagEvents || argFlag(os.Args[1:], "events"))
	})
	return jsonWriter
}

// isUsageError reports whether err is cobra's complaint about the command
// line, by its text (as fang does: cobra has no typed usage errors).
func isUsageError(err error) bool {
	s := err.Error()
	for _, prefix := range []string{
		"flag needs an argument:",
		"unknown flag:",
		"unknown shorthand flag:",
		"unknown command",
		"invalid argument",
		"accepts ",
		"requires at least",
		"requires at most",
	} {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

// jsonCapabilities lists, sorted, the command paths (without the root name)
// of every command with a JSON mode, plus "events" for --events streaming.
func jsonCapabilities() []string {
	caps := []string{"events"}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			if sub.Annotations[annotationJSON] == "true" {
				caps = append(caps, strings.TrimPrefix(sub.CommandPath(), rootCmd.Name()+" "))
			}
			walk(sub)
		}
	}
	walk(rootCmd)
	sort.Strings(caps)
	return caps
}
