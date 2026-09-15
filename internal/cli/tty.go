package cli

import (
	"io"
	"os"

	"golang.org/x/term"
)

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
//
// Injectable two ways: callers pass a.stdin, so tests exercise the refusal
// path by construction (a bytes.Buffer never reports true); and
// VAULTY_STDIN_TTY=1/0 forces the answer for golden cases that need to
// exercise the --accept-growth logic itself (not the terminal gate) without
// an actual terminal — set only by tests, never meant for real use.
func stdinIsTTY(r io.Reader) bool {
	switch os.Getenv("VAULTY_STDIN_TTY") {
	case "1":
		return true
	case "0":
		return false
	}

	fd, ok := r.(interface{ Fd() uintptr })
	if !ok {
		return false
	}
	return term.IsTerminal(int(fd.Fd()))
}
