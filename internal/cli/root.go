// Package cli wires the cobra command tree. Commands stay thin: they resolve
// the vault, call into internal packages, and render human or --json output.
package cli

import (
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/toppynl/vaulty/internal/name"
)

// Exit codes (DESIGN.md §3.4). Hook mode maps findings to ExitHookFindings.
const (
	ExitOK           = 0 // success, no error-severity findings
	ExitFindings     = 1 // lint: at least one error-severity finding
	ExitUsage        = 2 // bad flags/args, page not found/ambiguous, invalid config
	ExitRefused      = 3 // write refused: validation, unsafe page state, safety check
	ExitIO           = 4 // read/write failure
	ExitHookFindings = 2 // lint --hook only: Claude Code feeds stderr back on exit 2
)

// ExitError carries a process exit code. Err == nil means "exit silently"
// (e.g. lint findings were already printed).
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("exit %d", e.Code)
	}
	return e.Err.Error()
}

func (e *ExitError) Unwrap() error { return e.Err }

// ErrNotImplemented marks skeleton stubs; removed as steps land.
var ErrNotImplemented = errors.New("not implemented yet")

type globalFlags struct {
	vaultDir string
	json     bool
}

type app struct {
	flags   globalFlags
	stdin   io.Reader
	stdout  io.Writer
	stderr  io.Writer
	version string
}

// Execute runs the CLI and returns the process exit code. All I/O goes
// through the given streams so golden tests can drive it in-process.
func Execute(version string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	args = normalizeAppendArgs(args)
	a := &app{stdin: stdin, stdout: stdout, stderr: stderr, version: version}
	root := a.newRoot()
	root.SetArgs(args)
	root.SetIn(stdin)
	root.SetOut(stdout)
	root.SetErr(stderr)

	err := root.Execute()
	if err == nil {
		return ExitOK
	}
	var ee *ExitError
	if errors.As(err, &ee) {
		if ee.Err != nil {
			fmt.Fprintln(stderr, name.Binary+":", ee.Err)
		}
		return ee.Code
	}
	// cobra flag/arg errors
	fmt.Fprintln(stderr, name.Binary+":", err)
	return ExitUsage
}

// appendBoolFlags are "timeline append"'s long boolean flags.
// normalizeAppendArgs floats them ahead of the two positionals
// (<page> "<entry>") regardless of where the caller put them, since the
// entry conventionally starts with "- " and `append`'s Args parsing
// (SetInterspersed(false), timeline.go) requires flags to precede every
// positional to avoid pflag misreading that entry as a flag cluster.
var appendBoolFlags = map[string]bool{"--touch": true, "--dry-run": true}

// normalizeAppendArgs finds a "timeline append" invocation in args and
// reorders the args after it so appendBoolFlags come first, in their
// original relative order, followed by every other token (the positionals,
// and anything at all once a literal "--" separator is seen — that always
// ends reordering, exactly like it ends flag scanning in getopt/git) in
// their original relative order. This lets "append <page> \"<entry>\"
// --touch" and "append --touch <page> \"<entry>\"" both work (DESIGN.md
// §8.1) without loosening the entry-vs-flag disambiguation SetInterspersed
// gives every other flag/positional in the CLI. A no-op when args does not
// contain a "timeline" "append" pair.
func normalizeAppendArgs(args []string) []string {
	idx := -1
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "timeline" && args[i+1] == "append" {
			idx = i + 2
			break
		}
	}
	if idx < 0 {
		return args
	}

	var flags, rest []string
	sawSeparator := false
	for _, a := range args[idx:] {
		if !sawSeparator && a == "--" {
			sawSeparator = true
			rest = append(rest, a)
			continue
		}
		if !sawSeparator && appendBoolFlags[a] {
			flags = append(flags, a)
			continue
		}
		rest = append(rest, a)
	}

	out := make([]string, 0, len(args))
	out = append(out, args[:idx]...)
	out = append(out, flags...)
	out = append(out, rest...)
	return out
}

func (a *app) newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           name.Binary,
		Short:         "Tools for LLM-maintained markdown knowledge vaults",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       a.version,
	}
	root.PersistentFlags().StringVar(&a.flags.vaultDir, "vault", "",
		"vault root (default: $"+name.EnvRoot+", else nearest ancestor with "+name.ConfigFile+", else git root)")
	root.PersistentFlags().BoolVar(&a.flags.json, "json", false, "machine-readable JSON output")

	root.AddCommand(a.newTimelineCmd())
	root.AddCommand(a.newConfigCmd())
	root.AddCommand(a.newVersionCmd())
	// Reserved for later units (DESIGN.md §3.1): index, lint, migrate, dream.
	return root
}

func (a *app) newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the vaulty version",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintln(a.stdout, a.version)
			return nil
		},
	}
}

func (a *app) newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Inspect vault configuration"}
	cmd.AddCommand(a.newConfigPrintCmd())
	return cmd
}

func (a *app) newConfigPrintCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "print",
		Short: "Print the effective config, vault root and config path",
		RunE: func(cmd *cobra.Command, args []string) error {
			v, err := a.openVault()
			if err != nil {
				return err
			}
			if a.flags.json {
				return a.writeJSON(struct {
					Root       string      `json:"root"`
					ConfigPath string      `json:"config_path"`
					Config     interface{} `json:"config"`
				}{v.Root, v.ConfigPath, v.Config})
			}
			fmt.Fprintln(a.stdout, "root:", v.Root)
			cfgPath := v.ConfigPath
			if cfgPath == "" {
				cfgPath = "(none, using defaults)"
			}
			fmt.Fprintln(a.stdout, "config:", cfgPath)
			return nil
		},
	}
}
