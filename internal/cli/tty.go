package cli

import (
	"io"
	"os"
)

// stdinIsTTY reports whether r is an interactive terminal, used to gate
// `--accept-growth` (DESIGN.md §6.1a): that flag is a deliberate,
// human-typed decision, never something a script or an agent-invoked
// subprocess can trigger just because it names a plain flag that
// `Bash(vaulty:*)` already allows. Anything without a file descriptor
// (e.g. the bytes.Buffer the golden test harness feeds as stdin) or whose
// descriptor is not a character device is treated as non-interactive.
//
// Injectable via the reader itself: callers pass a.stdin, so tests exercise
// the real code path by construction (a bytes.Buffer never reports true)
// without needing a separate mock hook.
func stdinIsTTY(r io.Reader) bool {
	fd, ok := r.(interface{ Fd() uintptr })
	if !ok {
		return false
	}
	f := os.NewFile(fd.Fd(), "stdin")
	if f == nil {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
