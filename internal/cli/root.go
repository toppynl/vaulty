// Package cli wires the cobra command tree. Commands stay thin: they resolve
// the vault, call into internal packages, and render human or --json output.
package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

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
	if t := os.Getenv(name.EnvToday); t != "" {
		if _, err := time.Parse("2006-01-02", t); err != nil {
			fmt.Fprintf(stderr, "%s: $%s: invalid date %q (want YYYY-MM-DD)\n", name.Binary, name.EnvToday, t)
			return ExitUsage
		}
	}
	if err := checkFlagTypos(args); err != nil {
		fmt.Fprintln(stderr, name.Binary+":", err)
		return ExitUsage
	}
	args = normalizeAppendArgs(args)
	args = normalizeSearchArgs(args)
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

// isAppendBoolFlag reports whether a is one of appendBoolFlags, bare
// ("--touch") or with an explicit value ("--touch=true", "--dry-run=false")
// — pflag accepts both forms for a bool flag, and normalizeAppendArgs must
// float either one ahead of the positionals the same way.
func isAppendBoolFlag(a string) bool {
	if appendBoolFlags[a] {
		return true
	}
	name, _, hasEq := strings.Cut(a, "=")
	return hasEq && appendBoolFlags[name]
}

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
//
// It also drops the first bare "--" it finds among the positionals. cobra's
// arg-count check (Args: ExactArgs(2)) counts "--" itself as a third
// positional once SetInterspersed(false) is in effect, so "append <page>
// -- \"--literal entry\"" would otherwise fail with "accepts 2 arg(s),
// received 3" even though a literal entry starting with "--" already
// parses fine without the separator (SetInterspersed(false) stops flag
// scanning at the first positional regardless). Supporting the separator
// anyway matches the getopt/git convention users reach for instinctively.
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
	droppedSeparator := false
	for _, a := range args[idx:] {
		if !sawSeparator && a == "--" {
			sawSeparator = true
			if !droppedSeparator {
				droppedSeparator = true
				continue
			}
			rest = append(rest, a)
			continue
		}
		if !sawSeparator && isAppendBoolFlag(a) {
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

// searchValueFlags are the long flags (global or `search`'s own) that take a
// separate value argument, so normalizeSearchArgs never mistakes that value
// for a query term.
var searchValueFlags = map[string]bool{"--vault": true, "--only": true, "--type": true, "--where": true, "--limit": true}

// normalizeSearchArgs lets `search` take negated query terms as ordinary
// arguments ("vaulty search delivery -hookdeck"): every argument after the
// `search` subcommand that starts with a single "-" (and is not "-h") is a
// query term, not a shorthand flag — `search` defines no shorthand flags —
// so those terms are moved after a "--" separator, in their original
// relative order with the other positionals. A no-op when args has no
// `search` subcommand (only the global --vault/--json may precede it) or
// already contains "--".
func normalizeSearchArgs(args []string) []string {
	i := 0
	for i < len(args) {
		a := args[i]
		switch {
		case a == "--vault":
			i += 2
			continue
		case strings.HasPrefix(a, "--vault=") || strings.HasPrefix(a, "--json"):
			i++
			continue
		}
		break
	}
	if i >= len(args) || args[i] != "search" {
		return args
	}
	rest := args[i+1:]
	var flags, terms []string
	for j := 0; j < len(rest); j++ {
		a := rest[j]
		if a == "--" {
			return args
		}
		if strings.HasPrefix(a, "--") {
			flags = append(flags, a)
			if searchValueFlags[a] && j+1 < len(rest) {
				flags = append(flags, rest[j+1])
				j++
			}
			continue
		}
		if a == "-h" {
			flags = append(flags, a)
			continue
		}
		terms = append(terms, a)
	}
	out := make([]string, 0, len(args)+1)
	out = append(out, args[:i+1]...)
	out = append(out, flags...)
	out = append(out, "--")
	return append(out, terms...)
}

// commandLongFlags is the long-flag vocabulary of `find` and `search`'s own
// flags (plus the global --vault/--json, since a single-dash typo of those
// is exactly the same mistake), used only by checkFlagTypos to spot a
// mistyped "-word" — never to parse or validate flags for real; cobra/pflag
// still do that.
var commandLongFlags = map[string]map[string]bool{
	"find": {
		"limit": true, "type": true, "body": true, "only": true, "json": true, "vault": true,
	},
	"search": {
		"only": true, "type": true, "where": true, "limit": true, "timeline": true,
		"no-cache": true, "rebuild": true, "stats": true, "json": true, "vault": true,
	},
}

// checkFlagTypos scans args for a `find`/`search` invocation and, within its
// own arguments (after the subcommand name, up to a literal "--" separator
// or the end of args), reports an error for any "-word" token (a single
// dash) whose word, case-insensitively, exactly matches one of that
// command's own long flag names.
//
// This exists because `search` defines no shorthand flags at all (DESIGN.md
// §19.1): every argument starting with a single "-" that isn't a recognized
// flag is silently treated as a *negated query term* by normalizeSearchArgs,
// so "vaulty search -limit 5" ran (a search for "5" excluding pages
// containing "limit") with no error, the wrong query, and nothing on stderr
// to say so. `find` never disguises the token as a positional — it reaches
// pflag's own shorthand-flag scan, which already errors — but with a cryptic
// "unknown shorthand flag" message; this check gives it the same clear one
// pre-emptively, before normalizeAppendArgs/normalizeSearchArgs or cobra see
// the args at all.
//
// A caller who means the literal word still has a way through: everything
// after a literal "--" is never scanned here, or by pflag's flag parsing
// (e.g. `vaulty search -- -limit`, `vaulty find -- -limit`). To search for
// it as a literal positive term rather than search's negation (§19.1), quote
// it as a phrase so the leading "-" is no longer the first character the
// query parser sees: `vaulty search '"-limit"'`.
func checkFlagTypos(args []string) error {
	i := 0
	for i < len(args) {
		a := args[i]
		switch {
		case a == "--vault":
			i += 2
			continue
		case strings.HasPrefix(a, "--vault=") || strings.HasPrefix(a, "--json"):
			i++
			continue
		}
		break
	}
	if i >= len(args) {
		return nil
	}
	sub := args[i]
	longFlags, ok := commandLongFlags[sub]
	if !ok {
		return nil
	}
	for _, a := range args[i+1:] {
		if a == "--" {
			break
		}
		if a == "-h" || !strings.HasPrefix(a, "-") || strings.HasPrefix(a, "--") {
			continue
		}
		word, _, _ := strings.Cut(strings.TrimPrefix(a, "-"), "=")
		if !longFlags[strings.ToLower(word)] {
			continue
		}
		escape := fmt.Sprintf("put it after \"--\" (e.g. %s -- %s)", sub, a)
		if sub == "search" {
			escape += fmt.Sprintf(", or quote it as a phrase to search for it as a term (e.g. %s '\"%s\"')", sub, a)
		}
		return fmt.Errorf("%s: %q looks like a typo for %q (%s has no shorthand flags). To use it literally, %s.",
			sub, a, "--"+word, sub, escape)
	}
	return nil
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
	root.AddCommand(a.newLogCmd())
	root.AddCommand(a.newFindCmd())
	root.AddCommand(a.newSearchCmd())
	root.AddCommand(a.newConfigCmd())
	root.AddCommand(a.newVersionCmd())
	// Reserved for later units (DESIGN.md §3.1): index, lint, migrate, dream, backlinks.
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
