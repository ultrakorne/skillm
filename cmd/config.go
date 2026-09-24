package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/ultrakorne/skillm/internal/config"
	"github.com/ultrakorne/skillm/internal/protocol"
	"github.com/ultrakorne/skillm/internal/ui"
)

func init() {
	rootCmd.AddCommand(newConfigCmd())
}

func newConfigCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "config",
		Short: "Read or change skillm's settings in config.toml",
		Long: "config reads and changes the settings a GUI offers, stored in " +
			"~/.skillm/config.toml:\n\n" +
			"  refresh.enabled         the scheduled update check is on (true/false; default true)\n" +
			"  refresh.interval_hours  hours between scheduled checks (1-720; default 24)\n\n" +
			"Agents are changed with `skillm agent`, not here.",
	}
	c.AddCommand(newConfigGetCmd(), newConfigSetCmd())
	return c
}

func newConfigGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get [key]",
		Short: "Print a setting, or every setting",
		Long: "config get prints the effective value of a setting — the default when " +
			"config.toml does not set it — or `key = value` for every setting when no key " +
			"is given. With --json the data always holds every setting; a key, when given, " +
			"is only checked.",
		Args: cobra.MaximumNArgs(1),
		// Settings live in config.toml alone; a GUI can read and change them
		// before it can tell the user git is missing.
		Annotations: map[string]string{annotationJSON: "true", annotationSkipGitCheck: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigGet(args)
		},
	}
}

func newConfigSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Change a setting",
		Long: "config set stores a setting in config.toml. Like `skillm agent`, it rewrites " +
			"the whole file: the agents and settings are kept, but comments and keys skillm " +
			"does not know are not. An unknown key (code unknown_key) or a value the key " +
			"does not accept (code invalid_value) changes nothing. With --json the data " +
			"holds every setting afterwards.",
		Args: cobra.ExactArgs(2),
		// Settings live in config.toml alone; a GUI can read and change them
		// before it can tell the user git is missing.
		Annotations: map[string]string{annotationJSON: "true", annotationSkipGitCheck: "true"},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigSet(cmd.Context(), args[0], args[1])
		},
	}
}

// runConfigGet prints one setting's value, or every setting. It only reads
// config.toml and takes no lock.
func runConfigGet(args []string) error {
	opts, err := coreOptions(false)
	if err != nil {
		return err
	}
	cfg, err := config.Load(opts.Home)
	if err != nil {
		return err
	}
	if len(args) == 1 {
		v, err := cfg.Get(args[0])
		if err != nil {
			return err
		}
		if !flagJSON {
			fmt.Fprintln(os.Stdout, v)
			return nil
		}
	}
	if flagJSON {
		return jsonOut().Result(protocol.NewConfigData(cfg))
	}
	for _, k := range config.Keys() {
		v, _ := cfg.Get(k)
		fmt.Fprintf(os.Stdout, "%s = %v\n", k, v)
	}
	return nil
}

// runConfigSet stores one setting under Home's lock: load, change, save.
func runConfigSet(ctx context.Context, key, value string) error {
	opts, err := coreOptions(false)
	if err != nil {
		return err
	}
	// Refuse a bad key or value before waiting for the lock.
	if err := config.Default().Set(key, value); err != nil {
		return err
	}
	unlock, err := lockHome(ctx, opts.Home, "skillm config set")
	if err != nil {
		return err
	}
	defer unlock()
	cfg, err := config.Load(opts.Home)
	if err != nil {
		return err
	}
	if err := cfg.Set(key, value); err != nil {
		return err
	}
	if err := config.Save(opts.Home, cfg); err != nil {
		return err
	}
	if flagJSON {
		return jsonOut().Result(protocol.NewConfigData(cfg))
	}
	v, _ := cfg.Get(key)
	ui.Successf("%s = %v", key, v)
	return nil
}
