package cli

import (
	"github.com/spf13/cobra"
)

func (a *app) newTimelineCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "timeline",
		Short: "Read, append to and lint '## Timeline' sections",
	}
	cmd.AddCommand(
		a.newTimelineLintCmd(),
		a.newTimelineReadCmd(),
		a.newTimelineAppendCmd(),
		a.newTimelineDumpCmd(),
	)
	return cmd
}

// ---- lint -------------------------------------------------------------

type lintOpts struct {
	hook    bool   // read Claude Code PostToolUse JSON from stdin
	changed string // git ref; lint files changed vs ref (per-file mode)
	strict  bool   // warnings also fail
}

func (a *app) newTimelineLintCmd() *cobra.Command {
	var o lintOpts
	cmd := &cobra.Command{
		Use:   "lint [paths...]",
		Short: "Check Timeline format and page hygiene (exit 1 on errors)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runTimelineLint(o, args)
		},
	}
	cmd.Flags().BoolVar(&o.hook, "hook", false, "Claude Code PostToolUse mode: read hook JSON on stdin, exit 2 with findings on stderr")
	cmd.Flags().StringVar(&o.changed, "changed", "", "lint .md files changed vs this git ref (default ref: main)")
	cmd.Flags().Lookup("changed").NoOptDefVal = "main"
	cmd.Flags().BoolVar(&o.strict, "strict", false, "treat warnings as errors")
	return cmd
}

func (a *app) runTimelineLint(o lintOpts, args []string) error {
	// TODO(step 3): DESIGN.md §6.
	return &ExitError{Code: ExitUsage, Err: ErrNotImplemented}
}

// ---- read -------------------------------------------------------------

type readOpts struct {
	timeline    bool
	since       string
	last        int
	frontmatter bool
}

func (a *app) newTimelineReadCmd() *cobra.Command {
	var o readOpts
	cmd := &cobra.Command{
		Use:   "read <page>",
		Short: "Print compiled truth (default) or Timeline entries of a page",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runTimelineRead(o, args[0])
		},
	}
	cmd.Flags().BoolVar(&o.timeline, "timeline", false, "print Timeline entries instead of compiled truth")
	cmd.Flags().StringVar(&o.since, "since", "", "only entries overlapping YYYY-MM-DD or later (implies --timeline)")
	cmd.Flags().IntVar(&o.last, "last", 0, "only the last N entries (implies --timeline)")
	cmd.Flags().BoolVar(&o.frontmatter, "frontmatter", false, "also print the frontmatter block first")
	return cmd
}

func (a *app) runTimelineRead(o readOpts, page string) error {
	// TODO(step 4): DESIGN.md §7.
	return &ExitError{Code: ExitUsage, Err: ErrNotImplemented}
}

// ---- append -----------------------------------------------------------

type appendOpts struct {
	touch  bool
	dryRun bool
}

func (a *app) newTimelineAppendCmd() *cobra.Command {
	var o appendOpts
	cmd := &cobra.Command{
		Use:   `append <page> "<entry>"`,
		Short: "Insert a Timeline entry by date (creates divider + section if missing)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runTimelineAppend(o, args[0], args[1])
		},
	}
	cmd.Flags().BoolVar(&o.touch, "touch", false, "also set frontmatter updated: to today")
	cmd.Flags().BoolVar(&o.dryRun, "dry-run", false, "print the resulting Timeline block, write nothing")
	return cmd
}

func (a *app) runTimelineAppend(o appendOpts, page, entry string) error {
	// TODO(step 4): DESIGN.md §8.
	return &ExitError{Code: ExitUsage, Err: ErrNotImplemented}
}

// ---- dump (hidden; parity harness) ------------------------------------

func (a *app) newTimelineDumpCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "dump [paths...]",
		Short:  "Emit parser state as JSON lines for the Node parity harness",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// TODO(step 2): DESIGN.md §10.3 — schema must match scripts/parity/oracle-dump.mjs.
			return &ExitError{Code: ExitUsage, Err: ErrNotImplemented}
		},
	}
	return cmd
}
