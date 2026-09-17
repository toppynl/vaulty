package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func (a *app) newTimelineCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "timeline",
		Short: "Append entries to '## Timeline' sections",
		// A parent without RunE makes cobra print help and exit 0 for an
		// unknown subcommand ("timeline lint"), so a stale hook or script
		// would silently pass. Refuse it as a usage error instead.
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return &ExitError{Code: ExitUsage, Err: fmt.Errorf("unknown command %q for %q", args[0], cmd.CommandPath())}
			}
			return cmd.Help()
		},
	}
	cmd.AddCommand(
		a.newTimelineAppendCmd(),
	)
	return cmd
}

// ---- append -----------------------------------------------------------

type appendOpts struct {
	touch  bool
	dryRun bool
}

func (a *app) newTimelineAppendCmd() *cobra.Command {
	var o appendOpts
	cmd := &cobra.Command{
		Use:   `append <page> "<entry>" [--touch] [--dry-run]`,
		Short: "Insert a Timeline entry by date (creates divider + section if missing)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runTimelineAppend(o, args[0], args[1])
		},
	}
	cmd.Flags().BoolVar(&o.touch, "touch", false, "also set frontmatter updated: to today")
	cmd.Flags().BoolVar(&o.dryRun, "dry-run", false, "print the resulting Timeline block, write nothing")
	// The entry argument conventionally starts with "- **DATE**...", which
	// pflag would otherwise try to parse as a shorthand-flag cluster no
	// matter where flags are allowed to appear. Stop flag scanning at the
	// first positional so the entry is never misread as a flag; Execute
	// (root.go, normalizeAppendArgs) compensates by moving --touch/
	// --dry-run in front of the positionals before cobra ever parses,
	// so both "append <page> \"<entry>\" --touch" and "append --touch
	// <page> \"<entry>\"" work (DESIGN.md §8.1).
	cmd.Flags().SetInterspersed(false)
	return cmd
}
