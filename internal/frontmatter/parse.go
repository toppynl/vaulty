// Package frontmatter reads and edits a page's flat YAML frontmatter line by
// line. It never decodes and re-encodes the whole block (that would reformat
// quotes, flow style, indentation and key order on every write): it maps
// top-level keys to line ranges, recognizes the few value shapes it can edit
// safely, and splices new lines in. yaml.v3 is only used to cross-check what
// the line parser saw and to verify the result (Verify).
package frontmatter

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Shape is how a top-level key's value is written.
type Shape int

const (
	ShapeUnsupported Shape = iota // anything the editor must not touch
	ShapeScalar                   // `key: value`, `key: "value"`, `key: 'value'`, bare `key:`
	ShapeFlow                     // `key: [a, "b"]` on one line
	ShapeBlock                    // `key:` followed by `- item` lines
)

// Item is one scalar list element.
type Item struct {
	Line    int    // block only: index into Block.Lines
	Prefix  string // block only: indent + dash + spacing before the value
	Raw     string // source text of the value (quotes included)
	Quote   byte   // 0, '"' or '\''
	Decoded string // yaml.v3-decoded value, stringified
}

// Entry is one top-level key and the lines that belong to it.
type Entry struct {
	Key        string // "" for a top-level line that isn't a simple key
	Start, End int    // [Start, End) into Block.Lines
	Shape      Shape

	// Scalar.
	Sep     string // whitespace between ':' and the value
	Raw     string // value source text (quotes included)
	Quote   byte
	Comment string // trailing " # ..." incl. leading whitespace
	Empty   bool   // bare `key:` (null)
	Decoded string

	// Flow and block lists.
	Items []Item
	// Flow only: the line is FlowHead + items joined by FlowSep + FlowTail.
	FlowHead, FlowSep, FlowTail string
}

// Block is the frontmatter text between the two '---' lines, as lines
// without their '\n'.
type Block struct {
	Lines   []string
	Entries []*Entry
}

var reKeyLine = regexp.MustCompile(`^([A-Za-z0-9_-]+):(?:([ \t]+)(.*))?$`)

// ValidKey reports whether k is a simple top-level identifier the editor
// accepts.
var ValidKey = regexp.MustCompile(`^[A-Za-z0-9_-]+$`).MatchString

// Parse splits inner (the frontmatter without delimiters) into entries.
func Parse(inner string) *Block {
	b := &Block{}
	if inner != "" {
		b.Lines = strings.Split(strings.TrimSuffix(inner, "\n"), "\n")
	}
	var cur *Entry
	for i, raw := range b.Lines {
		l := strings.TrimRight(raw, "\r")
		switch {
		case isBlankOrComment(l):
			// Attached to cur only if a continuation line follows (below).
		case isContinuation(l):
			if cur != nil {
				cur.End = i + 1
			}
		default:
			cur = &Entry{Start: i, End: i + 1}
			if m := reKeyLine.FindStringSubmatch(l); m != nil {
				cur.Key = m[1]
			}
			b.Entries = append(b.Entries, cur)
		}
	}
	for _, e := range b.Entries {
		b.classify(e)
	}
	return b
}

// Text re-joins the lines into frontmatter text ("" or ending in '\n').
func (b *Block) Text() string {
	if len(b.Lines) == 0 {
		return ""
	}
	return strings.Join(b.Lines, "\n") + "\n"
}

// Find returns the first entry for key, or nil.
func (b *Block) Find(key string) *Entry {
	for _, e := range b.Entries {
		if e.Key == key {
			return e
		}
	}
	return nil
}

func isBlankOrComment(l string) bool {
	t := strings.TrimSpace(l)
	return t == "" || strings.HasPrefix(t, "#")
}

// isContinuation: indented lines, and column-0 "- item" lines (a block
// sequence may sit at the key's own indentation).
func isContinuation(l string) bool {
	return strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t") || l == "-" || strings.HasPrefix(l, "- ")
}

var reBlockItem = regexp.MustCompile(`^([ \t]*-[ \t]+)(.*)$`)

func (b *Block) classify(e *Entry) {
	e.Shape = ShapeUnsupported
	if e.Key == "" {
		return
	}
	m := reKeyLine.FindStringSubmatch(strings.TrimRight(b.Lines[e.Start], "\r"))
	sep, rest := m[2], strings.TrimRight(m[3], " \t")

	var cont []int // non-blank, non-comment continuation lines
	for i := e.Start + 1; i < e.End; i++ {
		if !isBlankOrComment(b.Lines[i]) {
			cont = append(cont, i)
		}
	}

	switch {
	case rest == "" && len(cont) == 0:
		e.Shape, e.Empty, e.Sep = ShapeScalar, true, sep
	case rest == "":
		indent := ""
		for n, i := range cont {
			im := reBlockItem.FindStringSubmatch(strings.TrimRight(b.Lines[i], "\r"))
			if im == nil {
				return
			}
			ind := im[1][:strings.Index(im[1], "-")]
			if n == 0 {
				indent = ind
			} else if ind != indent {
				return
			}
			// A trailing comment stays in place: item lines are only ever
			// inserted or deleted, never rewritten.
			raw, q, _, ok := scanScalar(im[2])
			if !ok || raw == "" {
				return
			}
			e.Items = append(e.Items, Item{Line: i, Prefix: im[1], Raw: raw, Quote: q})
		}
		e.Shape = ShapeBlock
	case len(cont) > 0:
		return // multi-line plain/quoted scalar, nested map, ...
	case rest[0] == '[':
		if !b.parseFlow(e, sep, rest) {
			return
		}
		e.Shape = ShapeFlow
	case strings.ContainsRune("{|>&*!%@`#?", rune(rest[0])):
		return
	default:
		raw, q, comment, ok := scanScalar(rest)
		if !ok {
			return
		}
		e.Shape, e.Sep, e.Raw, e.Quote, e.Comment = ShapeScalar, sep, raw, q, comment
	}

	if !b.crossCheck(e) {
		e.Shape = ShapeUnsupported
	}
}

// scanScalar reads one scalar token (plain, "double" or 'single' quoted)
// from s, returning its source text and an optional trailing comment.
func scanScalar(s string) (raw string, quote byte, comment string, ok bool) {
	if s == "" {
		return "", 0, "", true
	}
	switch s[0] {
	case '"', '\'':
		end := scanQuoted(s)
		if end < 0 {
			return "", 0, "", false
		}
		tail := s[end:]
		if strings.TrimSpace(tail) != "" {
			t := strings.TrimLeft(tail, " \t")
			if !strings.HasPrefix(t, "#") || len(t) == len(tail) {
				return "", 0, "", false
			}
		}
		return s[:end], s[0], tail, true
	case '[', '{', '|', '>', '&', '*', '!', '%', '@', '`', '#', '?':
		return "", 0, "", false
	}
	for i := 1; i < len(s); i++ {
		if s[i] == '#' && (s[i-1] == ' ' || s[i-1] == '\t') {
			v := strings.TrimRight(s[:i], " \t")
			return v, 0, s[len(v):], true
		}
	}
	return s, 0, "", true
}

// scanQuoted returns the index just past the closing quote of the quoted
// scalar starting at s[0], or -1 if it isn't closed on this line.
func scanQuoted(s string) int {
	q := s[0]
	for i := 1; i < len(s); i++ {
		switch {
		case q == '"' && s[i] == '\\':
			i++
		case s[i] == q && q == '\'' && i+1 < len(s) && s[i+1] == '\'':
			i++
		case s[i] == q:
			return i + 1
		}
	}
	return -1
}

// parseFlow reads a single-line flow sequence of scalars.
func (b *Block) parseFlow(e *Entry, sep, rest string) bool {
	line := strings.TrimRight(b.Lines[e.Start], "\r")
	open := len(e.Key) + 1 + len(sep)
	i := 1
	for i < len(rest) && (rest[i] == ' ' || rest[i] == '\t') {
		i++
	}
	head := line[:open+i]
	var itemEnds, itemStarts []int
	closeAt := -1
	for closeAt < 0 {
		for i < len(rest) && (rest[i] == ' ' || rest[i] == '\t') {
			i++
		}
		if i >= len(rest) {
			return false
		}
		if rest[i] == ']' {
			closeAt = i
			break
		}
		start := i
		switch rest[i] {
		case '"', '\'':
			n := scanQuoted(rest[i:])
			if n < 0 {
				return false
			}
			i += n
		default:
			for i < len(rest) && rest[i] != ',' && rest[i] != ']' {
				if strings.ContainsRune("[{}#", rune(rest[i])) {
					return false
				}
				i++
			}
		}
		raw := strings.TrimRight(rest[start:i], " \t")
		if raw == "" {
			return false
		}
		var q byte
		if raw[0] == '"' || raw[0] == '\'' {
			q = raw[0]
		} else if strings.ContainsAny(raw[:1], "&*!|>%@`?") {
			return false
		}
		e.Items = append(e.Items, Item{Raw: raw, Quote: q})
		itemStarts = append(itemStarts, start)
		itemEnds = append(itemEnds, start+len(raw))
		for i < len(rest) && (rest[i] == ' ' || rest[i] == '\t') {
			i++
		}
		if i >= len(rest) {
			return false
		}
		switch rest[i] {
		case ',':
			i++
		case ']':
			closeAt = i
		default:
			return false
		}
	}
	tail := rest[closeAt+1:]
	if t := strings.TrimLeft(tail, " \t"); t != "" && (!strings.HasPrefix(t, "#") || len(t) == len(tail)) {
		return false
	}
	e.FlowSep = ", "
	if len(e.Items) >= 2 {
		e.FlowSep = rest[itemEnds[0]:itemStarts[1]]
	}
	padR := ""
	if len(e.Items) > 0 {
		padR = rest[itemEnds[len(e.Items)-1]:closeAt]
		padR = padR[strings.LastIndex(padR, ",")+1:] // a trailing comma is dropped on rewrite
	}
	e.FlowHead = head
	e.FlowTail = padR + rest[closeAt:]
	return true
}

// crossCheck decodes the entry's own lines with yaml.v3 and requires the
// result to match the shape the line parser inferred; it also fills the
// decoded values used for comparisons.
func (b *Block) crossCheck(e *Entry) bool {
	var m map[string]any
	if err := yaml.Unmarshal([]byte(strings.Join(b.Lines[e.Start:e.End], "\n")), &m); err != nil {
		return false
	}
	v, ok := m[e.Key]
	if !ok || len(m) != 1 {
		return false
	}
	switch e.Shape {
	case ShapeScalar:
		if e.Empty {
			return v == nil
		}
		s, ok := Stringify(v)
		if !ok {
			return false
		}
		e.Decoded = s
		return true
	default:
		list, ok := v.([]any)
		if !ok || len(list) != len(e.Items) {
			return false
		}
		for i, it := range list {
			s, ok := Stringify(it)
			if !ok || it == nil {
				return false
			}
			e.Items[i].Decoded = s
		}
		return true
	}
}

// Stringify renders a decoded scalar as a string; ok is false for lists and
// maps. Dates render the way page frontmatter values do (YYYY-MM-DD, or
// RFC 3339 with a time of day).
func Stringify(v any) (string, bool) {
	switch t := v.(type) {
	case nil:
		return "", true
	case string:
		return t, true
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64), true
	case time.Time:
		if t.Hour() == 0 && t.Minute() == 0 && t.Second() == 0 && t.Nanosecond() == 0 {
			return t.Format("2006-01-02"), true
		}
		return t.Format(time.RFC3339), true
	case []any, map[string]any, map[any]any:
		return "", false
	default:
		return fmt.Sprint(t), true
	}
}
