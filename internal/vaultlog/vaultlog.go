// Package vaultlog parses and appends entries in a vault's append-only
// log.md convention: "## [YYYY-MM-DD] <op> | <title>" headings, each
// followed by an optional free-form body, newest entry at the bottom
// (DESIGN.md §16).
package vaultlog

import (
	"fmt"
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

// ValidateField rejects newlines (CR or LF) and, for op only, the literal
// " | " separator sequence, which would corrupt the heading format on
// round-trip parsing (DESIGN.md §16.1).
func ValidateField(name, value string) error {
	if strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf("%s must not contain a newline", name)
	}
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s must not be empty", name)
	}
	if name == "op" && strings.Contains(value, " | ") {
		return fmt.Errorf("op must not contain \" | \"")
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

// Append returns the new file content with entry appended at the end
// (newest entry at the bottom), separated from any existing content by
// exactly one blank line.
func Append(src []byte, date, op, title, body string) []byte {
	entry := Format(date, op, title, body)
	if len(src) == 0 {
		return []byte(entry)
	}
	s := string(src)
	if !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	if !strings.HasSuffix(s, "\n\n") {
		s += "\n"
	}
	return []byte(s + entry)
}
