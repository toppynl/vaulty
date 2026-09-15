package cli

import (
	"io"

	"golang.org/x/term"
)

// stdinTTYOverride lets golden tests force stdinIsTTY's answer without a
// real terminal attached to the test process. It is deliberately left
// unset here in production code: only golden_test.go (a _test.go file,
// compiled into the binary exclusively by `go test`) ever assigns it, by
// reading the golden case's own `env` file — VAULTY_STDIN_TTY is no longer
// consulted by the production binary at all, so no environment variable
// can influence the real TTY gate.
var stdinTTYOverride *bool

// stdinIsTTY reports whether r is an interactive terminal, used to gate
// `--accept-growth` (DESIGN.md §6.1a): that flag is a deliberate,
// human-typed decision, never something a script or an agent-invoked
// subprocess can trigger just because it names a plain flag that
// `Bash(vaulty:*)` already allows. Anything without a file descriptor
// (e.g. the bytes.Buffer the golden test harness feeds as stdin), or whose
// descriptor is a pipe, redirected file, or another non-terminal character
// device (notably `/dev/null`, which *is* a character device but is not a
// terminal — a plain os.ModeCharDevice check would wrongly accept it), is
// treated as non-interactive. term.IsTerminal does the real ioctl-based
// check (TCGETS/TIOCGETA) instead of guessing from the file mode.
func stdinIsTTY(r io.Reader) bool {
	if stdinTTYOverride != nil {
		return *stdinTTYOverride
	}

	fd, ok := r.(interface{ Fd() uintptr })
	if !ok {
		return false
	}
	return term.IsTerminal(int(fd.Fd()))
}
