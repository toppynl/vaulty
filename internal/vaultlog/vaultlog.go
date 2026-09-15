// Package vaultlog parses and appends entries in a vault's append-only
// log.md convention: "## [YYYY-MM-DD] <op> | <title>" headings, each
// followed by an optional free-form body. `log append` always adds at the
// end of the file, but the real vault's log.md is not strictly
// chronological throughout (entries added by hand or other tooling can be
// out of date order), so file order must never be assumed to equal
// chronological order — see FindOutOfOrder and DESIGN.md §16.3.
package vaultlog

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"time"
)

// DateFormat is the entry heading's date layout.
const DateFormat = "2006-01-02"

// Entry is one well-formed "## [DATE] op | title" heading plus its body.
type Entry struct {
	Line  int    // 1-based line of the heading
	Date  string // YYYY-MM-DD
	Op    string
	Title string
	Body  string // trimmed; "" when the entry has no body lines
}

// Malformed is a "## " heading line that did not parse as a well-formed
// Entry heading.
type Malformed struct {
	Line   int
	Text   string
	Reason string
}

// Parse scans src for entries. It never errors and never stops: every
// "## " line that isn't a valid entry heading is reported in malformed
// instead of aborting the scan, so one bad heading never hides the rest of
// the file (DESIGN.md §16.2 — "log last must not crash on ~6 malformed
// headings in the real vault").
func Parse(src []byte) (entries []Entry, malformed []Malformed) {
	lines := strings.Split(string(src), "\n")

	type head struct {
		line   int // 1-based
		ok     bool
		entry  Entry
		reason string
		text   string
	}
	var heads []head
	for i, raw := range lines {
		if !strings.HasPrefix(raw, "## ") {
			continue
		}
		n := i + 1
		e, reason, ok := parseHeading(raw)
		if ok {
			e.Line = n
			heads = append(heads, head{line: n, ok: true, entry: e})
		} else {
			heads = append(heads, head{line: n, ok: false, reason: reason, text: raw})
		}
	}

	for i, h := range heads {
		bodyStart := h.line // 0-based index of the line right after the heading
		bodyEnd := len(lines)
		if i+1 < len(heads) {
			bodyEnd = heads[i+1].line - 1
		}
		body := strings.TrimSpace(strings.Join(lines[bodyStart:bodyEnd], "\n"))
		if h.ok {
			e := h.entry
			e.Body = body
			entries = append(entries, e)
		} else {
			reason := h.reason
			malformed = append(malformed, Malformed{Line: h.line, Text: h.text, Reason: reason})
		}
	}
	return entries, malformed
}

// parseHeading parses one "## [DATE] op | title" line.
func parseHeading(line string) (Entry, string, bool) {
	rest := strings.TrimPrefix(line, "## ")
	if !strings.HasPrefix(rest, "[") {
		return Entry{}, "missing [DATE]", false
	}
	closeIdx := strings.Index(rest, "]")
	if closeIdx < 0 {
		return Entry{}, "missing closing ]", false
	}
	date := rest[1:closeIdx]
	if _, err := time.Parse(DateFormat, date); err != nil {
		return Entry{}, fmt.Sprintf("invalid date %q", date), false
	}
	after := rest[closeIdx+1:]
	if !strings.HasPrefix(after, " ") {
		return Entry{}, "missing space after [DATE]", false
	}
	after = after[1:]
	sep := strings.Index(after, " | ")
	if sep < 0 {
		return Entry{}, "missing \" | \" between op and title", false
	}
	op := strings.TrimSpace(after[:sep])
	title := strings.TrimSpace(after[sep+3:])
	if op == "" {
		return Entry{}, "empty op", false
	}
	if title == "" {
		return Entry{}, "empty title", false
	}
	return Entry{Date: date, Op: op, Title: title}, "", true
}

// ValidateField rejects newlines (CR or LF); an empty value; for op, any
// "|" at all (not just the " | " separator sequence — even a bare pipe,
// tolerated when reading a legacy entry §16.1, would make an op this
// command itself just wrote ambiguous with the separator on every future
// parse); and for body, a line that (trimmed) starts with "#" — such a
// line reads as its own "## [...] ..." heading to Parse once written,
// silently injecting a second, forged log entry into the file rather than
// staying inside the one it was meant to be a body line of (DESIGN.md
// §16.2).
func ValidateField(name, value string) error {
	if strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf("%s must not contain a newline", name)
	}
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s must not be empty", name)
	}
	if name == "op" && strings.Contains(value, "|") {
		return fmt.Errorf("op must not contain \"|\"")
	}
	if name == "body" {
		for _, line := range strings.Split(value, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				return fmt.Errorf("body must not contain a line starting with \"#\" (would be read as a heading)")
			}
		}
	}
	return nil
}

// Format renders one entry heading (+ optional body) exactly as it will be
// written to log.md. date must already be a validated YYYY-MM-DD string.
func Format(date, op, title, body string) string {
	s := fmt.Sprintf("## [%s] %s | %s\n", date, op, title)
	if body != "" {
		s += body + "\n"
	}
	return s
}

// AppendSuffix returns exactly the bytes to write after src's own bytes —
// never src itself — to append one entry: a separator (0, 1 or 2 newlines,
// whatever src's current ending needs to reach a blank line above the new
// heading) plus Format(date, op, title, body). Callers that hold src open
// for append (e.g. via O_APPEND, or an explicit WriteAt at len(src)) write
// only this suffix, so an existing entry's bytes are never rewritten —
// matching the append-only guarantee already stated for Timeline blocks
// (§8.4: "existing bytes are never changed, only added to").
func AppendSuffix(src []byte, date, op, title, body string) []byte {
	return []byte(SeparatorFor(src) + Format(date, op, title, body))
}

// SeparatorFor is the run of newlines (0, 1 or 2) AppendSuffix needs to
// prepend to reach a blank line above the new heading, given src's current
// ending. Exported so a caller writing only the append delta (never
// re-reading/rewriting src) can work out where its new heading's line
// number falls without re-parsing the whole file.
func SeparatorFor(src []byte) string {
	if len(src) == 0 {
		return ""
	}
	switch {
	case bytes.HasSuffix(src, []byte("\n\n")):
		return ""
	case bytes.HasSuffix(src, []byte("\n")):
		return "\n"
	default:
		return "\n\n"
	}
}

// Append returns the new full file content with entry appended at the end,
// separated from any existing content by exactly one blank line. A
// convenience wrapper around AppendSuffix for callers (tests, and anything
// working off the full in-memory content already) that don't need the
// append-only write AppendSuffix exists for.
func Append(src []byte, date, op, title, body string) []byte {
	suffix := AppendSuffix(src, date, op, title, body)
	out := make([]byte, 0, len(src)+len(suffix))
	out = append(out, src...)
	out = append(out, suffix...)
	return out
}

// OutOfOrder flags a well-formed entry whose date is earlier than an
// entry that precedes it in the file — a warning, not a format defect
// (Malformed above): the heading itself parses fine, but file order
// (which `log last` used to assume was chronological order) doesn't match
// date order at this point. Real-vault log.md history is not strictly
// chronological (entries added by hand or other tooling), so this is
// expected to fire occasionally; it's surfaced for `log lint` to report,
// not treated as corruption.
type OutOfOrder struct {
	Line     int // the out-of-order entry's heading line
	Date     string
	PrevLine int // the earlier-in-file entry it's out of order with
	PrevDate string
}

// FindOutOfOrder scans well-formed entries (as returned by Parse, in file
// order) and reports every point where a date goes backwards.
func FindOutOfOrder(entries []Entry) []OutOfOrder {
	var out []OutOfOrder
	for i := 1; i < len(entries); i++ {
		if entries[i].Date < entries[i-1].Date {
			out = append(out, OutOfOrder{
				Line: entries[i].Line, Date: entries[i].Date,
				PrevLine: entries[i-1].Line, PrevDate: entries[i-1].Date,
			})
		}
	}
	return out
}

// SortByDate stable-sorts entries by Date ascending, preserving file order
// among entries sharing a date. `log last` uses this before taking the
// tail-N, since file order alone cannot be trusted to be chronological
// (OutOfOrder above).
func SortByDate(entries []Entry) []Entry {
	out := make([]Entry, len(entries))
	copy(out, entries)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out
}
