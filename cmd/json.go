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

// flagsParsed is set once cobra has parsed the command line without error
// (in the root's PersistentPreRunE). From then on flagJSON and flagEvents are
// the truth; before it, only the raw arguments can tell (see jsonMode).
var flagsParsed bool

// Execute runs the root command through fang. In JSON mode an error is
// written to stdout as the protocol's error envelope instead of fang's styled
// message on stderr; the returned error still makes the process exit non-zero.
func Execute(ctx context.Context) error {
	if jsonMode() {
		if err := refuseTerminalOnly(os.Args[1:]); err != nil {
			_ = jsonOut().Fail(err)
			return err
		}
	}
	return fang.Execute(ctx, rootCmd,
		fang.WithVersion(version),
		fang.WithErrorHandler(handleError))
}

// refuseTerminalOnly refuses, in JSON mode, the invocations cobra answers
// itself before any hook runs, with terminal output on stdout and exit 0:
// --help/-h anywhere before "--", and a command that only groups others
// (bare `skillm --json`, `skillm --version --json`, `skillm config --json`,
// `skillm completion --json`),
// for which cobra prints its help. A command line cobra cannot resolve is
// left to cobra, which reports it as a usage error.
func refuseTerminalOnly(args []string) error {
	for _, a := range args {
		if a == "--" {
			break
		}
		if a == "-h" || a == "--help" || strings.HasPrefix(a, "--help=") {
			return &protocol.Error{Code: protocol.CodeJSONUnsupported, Message: "--help has no JSON mode"}
		}
	}
	// Cobra adds its help and completion commands only inside Execute; add
	// them now (both are idempotent) so `skillm completion --json` resolves
	// to the group it is rather than to an unknown command.
	rootCmd.InitDefaultHelpCmd()
	rootCmd.InitDefaultCompletionCmd(args...)
	if c, _, err := rootCmd.Find(args); err == nil && !c.Runnable() {
		return &protocol.Error{
			Code:    protocol.CodeJSONUnsupported,
			Message: "`" + c.CommandPath() + "` has no JSON mode; name a command",
		}
	}
	return nil
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
		// Not starting with the flag: fang capitalizes an error's first letter.
		return &protocol.Error{Code: protocol.CodeUsage, Message: "the --events flag requires --json"}
	}
	if flagJSON && c.Annotations[annotationJSON] != "true" {
		return &protocol.Error{
			Code:    protocol.CodeJSONUnsupported,
			Message: "`" + c.CommandPath() + "` has no JSON mode",
		}
	}
	return nil
}

// jsonMode reports whether this run writes the JSON protocol: the parsed
// --json once cobra has parsed the command line, the raw arguments before
// that, so an error cobra hit before it parsed --json (an unknown flag in
// front of it, say) is still reported as JSON.
func jsonMode() bool {
	return protocolFlag(flagJSON, "json")
}

// protocolFlag is a global boolean flag's value: the parsed one after a
// successful parse, else what the raw arguments say.
func protocolFlag(parsed bool, name string) bool {
	if flagsParsed {
		return parsed
	}
	return argFlag(os.Args[1:], name)
}

// argFlag reports the value the raw args give the boolean flag --name, as
// pflag would: the last "--name" (true) or "--name=<bool>" before any "--"
// wins, and a value that is not a bool is ignored.
func argFlag(args []string, name string) bool {
	val := false
	for _, a := range args {
		if a == "--" {
			break
		}
		if a == "--"+name {
			val = true
		} else if v, ok := strings.CutPrefix(a, "--"+name+"="); ok {
			if b, err := strconv.ParseBool(v); err == nil {
				val = b
			}
		}
	}
	return val
}

// quietGit makes every git child of a JSON-mode run non-interactive, since a
// JSON run never prompts: git asks for no credentials on the terminal
// (GIT_TERMINAL_PROMPT=0), and ssh runs in BatchMode (no password or host-key
// questions) unless the user set their own GIT_SSH_COMMAND or GIT_SSH.
// Git children inherit this process's environment.
func quietGit() {
	_ = os.Setenv("GIT_TERMINAL_PROMPT", "0")
	if os.Getenv("GIT_SSH_COMMAND") == "" && os.Getenv("GIT_SSH") == "" {
		_ = os.Setenv("GIT_SSH_COMMAND", "ssh -o BatchMode=yes")
	}
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
		jsonWriter = protocol.NewWriter(os.Stdout, protocolFlag(flagEvents, "events"))
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
		// Flag groups (MarkFlagsMutuallyExclusive and friends).
		"if any flags in the group",
		"at least one of the flags in the group",
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

// usageError is a command-line mistake the command itself found (a missing
// or conflicting flag): code "usage" in JSON mode, its message otherwise.
func usageError(msg string) error {
	return &protocol.Error{Code: protocol.CodeUsage, Message: msg}
}
