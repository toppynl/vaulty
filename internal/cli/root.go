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
	// Reserved for later units (DESIGN.md §3.1): index, lint, migrate, dream.
	return root
}
