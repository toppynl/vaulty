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
	)
	return cmd
}

// ---- lint -------------------------------------------------------------

type lintOpts struct {
	hook          bool   // read Claude Code PostToolUse JSON from stdin
	changed       string // git ref; lint files changed vs ref (per-file mode)
	strict        bool   // warnings also fail
	warnings      bool   // print warnings in human mode
	writeBaseline bool   // recompute and write the ratchet baseline (DESIGN.md §6.1a)
	acceptGrowth  bool   // with --write-baseline: allow raising a page's baselined debt
	checkBaseline bool   // read-only: refuse if the on-disk baseline grew vs HEAD (pre-commit backstop)
	staged        bool   // with --check-baseline: compare the staged (index) baseline, not the working copy
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
	cmd.Flags().BoolVar(&o.warnings, "warnings", false, "also print warnings in human mode")
	cmd.Flags().BoolVar(&o.writeBaseline, "write-baseline", false, "recompute the TL006/TL008/PG002 ratchet baseline over the whole vault and write it, then exit (shrink only by default; see --accept-growth)")
	cmd.Flags().BoolVar(&o.acceptGrowth, "accept-growth", false, "with --write-baseline, also accept pages whose debt grew (a human decision — never run by an agent)")
	cmd.Flags().BoolVar(&o.checkBaseline, "check-baseline", false, "read-only: fail if the on-disk ratchet baseline is higher than the one committed at HEAD (pre-commit backstop; never writes)")
	cmd.Flags().BoolVar(&o.staged, "staged", false, "with --check-baseline: compare the staged (git index) baseline against HEAD instead of the working copy (use in a pre-commit hook)")
	return cmd
}

// ---- read -------------------------------------------------------------

type readOpts struct {
	timeline    bool
	since       string
	last        int
	frontmatter bool
	headings    bool
	section     string
	maxBytes    int
	maxBytesSet bool
}

func (a *app) newTimelineReadCmd() *cobra.Command {
	var o readOpts
	cmd := &cobra.Command{
		Use:   "read <page>",
		Short: "Print compiled truth (default), Timeline entries, headings or one section of a page",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			o.maxBytesSet = cmd.Flags().Changed("max-bytes")
			return a.runTimelineRead(o, args[0])
		},
	}
	cmd.Flags().BoolVar(&o.timeline, "timeline", false, "print Timeline entries instead of compiled truth")
	cmd.Flags().StringVar(&o.since, "since", "", "only entries overlapping YYYY-MM-DD or later (implies --timeline)")
	cmd.Flags().IntVar(&o.last, "last", 0, "only the last N entries (implies --timeline)")
	cmd.Flags().BoolVar(&o.frontmatter, "frontmatter", false, "also print the frontmatter block first")
	cmd.Flags().BoolVar(&o.headings, "headings", false, "list section headings with line number, line count and byte count instead of printing content")
	cmd.Flags().StringVar(&o.section, "section", "", "print only this section (a heading's text, from the heading to the next heading of equal-or-higher level or EOF)")
	cmd.Flags().IntVar(&o.maxBytes, "max-bytes", 0, "truncate the printed content to N bytes, with a marker noting how much was cut")
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
