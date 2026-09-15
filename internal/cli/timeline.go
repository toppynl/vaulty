package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"

	"github.com/toppynl/vaulty/internal/doc"
	"github.com/toppynl/vaulty/internal/timeline"
	"github.com/toppynl/vaulty/internal/vault"
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
	hook          bool   // read Claude Code PostToolUse JSON from stdin
	changed       string // git ref; lint files changed vs ref (per-file mode)
	strict        bool   // warnings also fail
	warnings      bool   // print warnings in human mode
	writeBaseline bool   // recompute and write the ratchet baseline (DESIGN.md §6.1a)
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
	cmd.Flags().BoolVar(&o.writeBaseline, "write-baseline", false, "recompute the TL006/TL008/PG002 ratchet baseline over the whole vault and write it, then exit")
	return cmd
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
	// The entry argument starts with "- **DATE**...", which pflag would
	// otherwise try to parse as a shorthand-flag cluster. Stop flag
	// scanning at the first positional so flags must precede <page>
	// "<entry>", never follow it.
	cmd.Flags().SetInterspersed(false)
	return cmd
}

// ---- dump (hidden; parity harness) ------------------------------------

func (a *app) newTimelineDumpCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "dump [paths...]",
		Short:  "Emit parser state as JSON lines for the Node parity harness",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.runTimelineDump(args)
		},
	}
	return cmd
}

func (a *app) runTimelineDump(args []string) error {
	v, err := a.openVault()
	if err != nil {
		return err
	}
	files, err := dumpFiles(v, args)
	if err != nil {
		return &ExitError{Code: ExitUsage, Err: err}
	}
	enc := json.NewEncoder(a.stdout)
	for _, rel := range files {
		full := filepath.Join(v.Root, filepath.FromSlash(rel))
		src, err := os.ReadFile(full)
		if err != nil {
			return &ExitError{Code: ExitIO, Err: err}
		}
		d := doc.Parse(rel, src)
		dump := timeline.DumpFile(rel, d, v.Config.Timeline)
		if dump == nil {
			continue
		}
		if err := enc.Encode(dump); err != nil {
			return &ExitError{Code: ExitIO, Err: err}
		}
	}
	return nil
}

// dumpFiles resolves dump's [paths...] to a sorted list of vault-relative
// .md files: default dirs when no args, else each arg (file or directory).
func dumpFiles(v *vault.Vault, args []string) ([]string, error) {
	if len(args) == 0 {
		return v.Walk()
	}
	var out []string
	for _, a := range args {
		abs, err := filepath.Abs(a)
		if err != nil {
			return nil, err
		}
		st, err := os.Stat(abs)
		if err != nil {
			return nil, err
		}
		if st.IsDir() {
			rel, err := filepath.Rel(v.Root, abs)
			if err != nil {
				return nil, err
			}
			files, err := v.Walk(filepath.ToSlash(rel))
			if err != nil {
				return nil, err
			}
			out = append(out, files...)
			continue
		}
		rel, err := filepath.Rel(v.Root, abs)
		if err != nil {
			return nil, err
		}
		out = append(out, filepath.ToSlash(rel))
	}
	sort.Strings(out)
	return out, nil
}
