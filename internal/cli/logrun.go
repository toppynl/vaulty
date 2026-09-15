package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/toppynl/vaulty/internal/name"
	"github.com/toppynl/vaulty/internal/vaultlog"
)

func (a *app) runLogAppend(o logAppendOpts, op, title string) error {
	v, err := a.openVault()
	if err != nil {
		return err
	}
	rel := v.Config.Log.Path
	full := filepath.Join(v.Root, filepath.FromSlash(rel))

	date := o.date
	if date == "" {
		date = a.today()
	} else if _, derr := parseLogDate(date); derr != nil {
		return &ExitError{Code: ExitUsage, Err: derr}
	}

	op = strings.TrimSpace(op)
	title = strings.TrimSpace(title)

	if err := vaultlog.ValidateField("op", op); err != nil {
		return &ExitError{Code: ExitRefused, Err: err}
	}
	if err := vaultlog.ValidateField("title", title); err != nil {
		return &ExitError{Code: ExitRefused, Err: err}
	}
	if o.body != "" {
		if err := vaultlog.ValidateField("body", o.body); err != nil {
			return &ExitError{Code: ExitRefused, Err: err}
		}
	}

	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return &ExitError{Code: ExitIO, Err: err}
	}

	line, err := appendLogEntry(full, date, op, title, o.body)
	if err != nil {
		return &ExitError{Code: ExitIO, Err: err}
	}

	if a.flags.json {
		return a.writeJSON(struct {
			Path  string `json:"path"`
			Line  int    `json:"line"`
			Date  string `json:"date"`
			Op    string `json:"op"`
			Title string `json:"title"`
		}{rel, line, date, op, title})
	}
	fmt.Fprintf(a.stdout, "appended %s:%d\n", rel, line)
	return nil
}

// appendLogEntry adds one entry to full under an exclusive lock, writing
// only the new bytes (via WriteAt at the file's current end) — an
// existing entry's bytes are never read back and rewritten, so a crash
// mid-write can only corrupt the entry being added, never history already
// on disk. The lock also serializes concurrent appenders (two processes
// racing to append would otherwise both compute the same end offset and
// clobber each other) and, because nothing is renamed over the path, a
// log.md that's a symlink stays a symlink (DESIGN.md §17.2).
func appendLogEntry(full, date, op, title, body string) (line int, err error) {
	f, err := os.OpenFile(full, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return 0, err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)

	orig, err := readAllAt(f)
	if err != nil {
		return 0, err
	}

	sep := vaultlog.SeparatorFor(orig)
	suffix := []byte(sep + vaultlog.Format(date, op, title, body))
	if _, err := f.WriteAt(suffix, int64(len(orig))); err != nil {
		return 0, err
	}

	// The new heading's line number: every '\n' already in orig, plus every
	// '\n' in the separator that precedes the heading, plus one (1-based).
	// Cheaper than re-reading/re-parsing the file after the write, and
	// exact — see the derivation in DESIGN.md §17.2.
	line = bytes.Count(orig, []byte("\n")) + strings.Count(sep, "\n") + 1
	return line, nil
}

// readAllAt reads f's entire current content via ReadAt (not Read), so it
// doesn't disturb f's file offset — appendLogEntry only ever positions via
// WriteAt, never Seek, keeping the "one exclusive lock, one read, one
// targeted write" sequence simple to reason about.
func readAllAt(f *os.File) ([]byte, error) {
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := info.Size()
	if size == 0 {
		return nil, nil
	}
	buf := make([]byte, size)
	if _, err := f.ReadAt(buf, 0); err != nil {
		return nil, err
	}
	return buf, nil
}

func (a *app) runLogLast(o logLastOpts) error {
	v, err := a.openVault()
	if err != nil {
		return err
	}
	if o.n < 0 {
		return &ExitError{Code: ExitUsage, Err: fmt.Errorf("--n must be >= 0")}
	}
	var since string
	if o.since != "" {
		since, err = normalizeSince(o.since)
		if err != nil {
			return &ExitError{Code: ExitUsage, Err: err}
		}
	}

	rel := v.Config.Log.Path
	full := filepath.Join(v.Root, filepath.FromSlash(rel))
	src, err := os.ReadFile(full)
	if err != nil {
		if os.IsNotExist(err) {
			src = nil
		} else {
			return &ExitError{Code: ExitIO, Err: err}
		}
	}

	entries, malformed := vaultlog.Parse(src)
	for _, m := range malformed {
		fmt.Fprintf(a.stderr, "%s: %s:%d: malformed log entry, skipped: %s\n", name.Binary, rel, m.Line, m.Reason)
	}

	var filtered []vaultlog.Entry
	for _, e := range entries {
		if o.op != "" && e.Op != o.op {
			continue
		}
		if since != "" && e.Date < since {
			continue
		}
		filtered = append(filtered, e)
	}
	// File order is not reliably chronological (the real vault's log.md has
	// entries added out of date order by hand/other tooling) — sort by
	// date before taking the tail, so "last N" means "N most recent by
	// date", stably keeping file order among entries sharing a date
	// (DESIGN.md §17.3).
	filtered = vaultlog.SortByDate(filtered)
	if o.n > 0 && len(filtered) > o.n {
		filtered = filtered[len(filtered)-o.n:]
	}

	if a.flags.json {
		type entryJSON struct {
			Line  int    `json:"line"`
			Date  string `json:"date"`
			Op    string `json:"op"`
			Title string `json:"title"`
			Body  string `json:"body,omitempty"`
		}
		out := struct {
			Path           string      `json:"path"`
			Entries        []entryJSON `json:"entries"`
			MalformedCount int         `json:"malformed_count"`
		}{Path: rel, MalformedCount: len(malformed)}
		for _, e := range filtered {
			out.Entries = append(out.Entries, entryJSON{Line: e.Line, Date: e.Date, Op: e.Op, Title: e.Title, Body: e.Body})
		}
		if out.Entries == nil {
			out.Entries = []entryJSON{}
		}
		return a.writeJSON(out)
	}

	for i, e := range filtered {
		if i > 0 {
			fmt.Fprintln(a.stdout)
		}
		a.stdout.Write([]byte(vaultlog.Format(e.Date, e.Op, e.Title, e.Body)))
	}
	return nil
}

func (a *app) runLogLint() error {
	v, err := a.openVault()
	if err != nil {
		return err
	}
	rel := v.Config.Log.Path
	full := filepath.Join(v.Root, filepath.FromSlash(rel))
	src, err := os.ReadFile(full)
	if err != nil {
		if os.IsNotExist(err) {
			src = nil
		} else {
			return &ExitError{Code: ExitIO, Err: err}
		}
	}
	entries, malformed := vaultlog.Parse(src)
	outOfOrder := vaultlog.FindOutOfOrder(entries)

	if a.flags.json {
		type malformedJSON struct {
			Line   int    `json:"line"`
			Reason string `json:"reason"`
			Text   string `json:"text"`
		}
		type outOfOrderJSON struct {
			Line     int    `json:"line"`
			Date     string `json:"date"`
			PrevLine int    `json:"prev_line"`
			PrevDate string `json:"prev_date"`
		}
		out := struct {
			Path       string           `json:"path"`
			Malformed  []malformedJSON  `json:"malformed"`
			OutOfOrder []outOfOrderJSON `json:"out_of_order"`
		}{Path: rel}
		for _, m := range malformed {
			out.Malformed = append(out.Malformed, malformedJSON{Line: m.Line, Reason: m.Reason, Text: m.Text})
		}
		if out.Malformed == nil {
			out.Malformed = []malformedJSON{}
		}
		for _, w := range outOfOrder {
			out.OutOfOrder = append(out.OutOfOrder, outOfOrderJSON{Line: w.Line, Date: w.Date, PrevLine: w.PrevLine, PrevDate: w.PrevDate})
		}
		if out.OutOfOrder == nil {
			out.OutOfOrder = []outOfOrderJSON{}
		}
		if err := a.writeJSON(out); err != nil {
			return err
		}
	} else {
		for _, m := range malformed {
			fmt.Fprintf(a.stdout, "%s:%d: malformed: %s: %s\n", rel, m.Line, m.Reason, m.Text)
		}
		for _, w := range outOfOrder {
			fmt.Fprintf(a.stdout, "%s:%d: warning out-of-order: entry dated %s appears after %s (line %d)\n", rel, w.Line, w.Date, w.PrevDate, w.PrevLine)
		}
	}
	// Out-of-order is a warning only — the real vault's log.md has ~41 of
	// them in its legitimate append-only history (entries added by hand or
	// other tooling, not a defect), so it must never fail the exit code on
	// its own or `log lint` would be permanently red there. Only a
	// malformed heading (a real format defect) fails the command.
	if len(malformed) > 0 {
		return &ExitError{Code: ExitFindings, Err: nil}
	}
	return nil
}

// parseLogDate validates --date as a full YYYY-MM-DD calendar date.
func parseLogDate(s string) (string, error) {
	if _, err := time.Parse(vaultlog.DateFormat, s); err != nil {
		return "", fmt.Errorf("--date: invalid date %q (want YYYY-MM-DD)", s)
	}
	return s, nil
}
