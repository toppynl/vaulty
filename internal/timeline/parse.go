// Package timeline parses, sorts, serializes and appends '## Timeline'
// blocks. Behaviour is pinned to the Node oracle scripts/lib/timeline.mjs
// in the me vault (DESIGN.md §5), but Parse never stops: every problem
// becomes a diag.Diag and no line is ever dropped.
package timeline

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/toppynl/vaulty/internal/config"
	"github.com/toppynl/vaulty/internal/diag"
	"github.com/toppynl/vaulty/internal/doc"
)

// Entry is one `- **date** ...` line plus the lines attached to it.
type Entry struct {
	Line     int      `json:"line"`     // 1-based line of the entry start
	Date     Date     `json:"date"`     // parsed date token
	Lines    []string `json:"lines"`    // entry line + attached lines incl. interior blanks, verbatim
	Attached []int    `json:"attached"` // 1-based line numbers of attached non-blank lines
}

// Block is one '## Timeline' section. Body uses oracle semantics: from the
// line after the heading up to the next '#'/'##' heading line or EOF.
type Block struct {
	HeadingLine     int         `json:"heading_line"`
	Heading         doc.Span    `json:"heading"`           // heading line incl. newline
	Body            doc.Span    `json:"body"`              // bodyStart..bodyEnd
	DividerLine     int         `json:"divider_line"`      // divider directly above heading (blank lines only between); 0 = none
	NextHeadingLine int         `json:"next_heading_line"` // heading that ends the block; 0 = EOF
	LeadingBlanks   int         `json:"leading_blanks"`
	TrailingBlanks  int         `json:"trailing_blanks"`
	Preamble        []string    `json:"preamble"` // non-entry lines before the first entry, verbatim (blocking)
	Entries         []Entry     `json:"entries"`
	Gaps            []int       `json:"gaps"` // len(Entries)-1 blank-line counts between entries
	Diags           []diag.Diag `json:"diags"`
}

// Sortable is true when no Blocking diag exists (== oracle parseBlock ok:true).
func (b *Block) Sortable() bool {
	for _, d := range b.Diags {
		if d.Blocking {
			return false
		}
	}
	return true
}

// Page is a parsed document: frontmatter, compiled truth and Timeline blocks.
type Page struct {
	Doc           *doc.Doc
	CompiledTruth doc.Span    // Body.Start .. divider line start (or first heading, or EOF)
	Blocks        []Block     // every heading line equal to cfg.Heading
	Diags         []diag.Diag // page-level: FM001, TL001, TL002, TL003
}

var reNextHeading = regexp.MustCompile(`^#{1,2} `)

// headingRegexp builds the block-start regexp for the configured heading
// text (DESIGN.md §5.2).
func headingRegexp(heading string) *regexp.Regexp {
	return regexp.MustCompile("^" + regexp.QuoteMeta(heading) + `[ \t]*\r?$`)
}

// Parse builds a Page. Never fails; problems are Diags.
func Parse(d *doc.Doc, cfg config.Timeline) *Page {
	p := &Page{Doc: d}

	if d.FMUnclosed {
		p.Diags = append(p.Diags, diag.Diag{
			Code: diag.FM001UnterminatedFrontmatter, Severity: diag.Warning,
			Line: 1, Message: "frontmatter opened but never closed",
		})
	}

	headingRe := headingRegexp(cfg.Heading)

	var blocks []Block
	for n := 1; n <= d.NumLines(); n++ {
		if !headingRe.MatchString(d.Line(n)) {
			continue
		}
		blocks = append(blocks, parseOneBlock(d, cfg, headingRe, n))
	}
	p.Blocks = blocks

	if len(blocks) > 1 {
		for i := 1; i < len(blocks); i++ {
			p.Diags = append(p.Diags, diag.Diag{
				Code: diag.TL001MultipleTimelines, Severity: diag.Error,
				Line: blocks[i].HeadingLine, Message: "more than one '## Timeline' block",
			})
		}
	}
	for i := range blocks {
		if blocks[i].DividerLine == 0 {
			p.Diags = append(p.Diags, diag.Diag{
				Code: diag.TL002MissingDivider, Severity: diag.Error,
				Line: blocks[i].HeadingLine, Message: "Timeline heading without a divider directly above it",
			})
		}
		if nl := blocks[i].NextHeadingLine; nl != 0 && !headingRe.MatchString(d.Line(nl)) {
			p.Diags = append(p.Diags, diag.Diag{
				Code: diag.TL003ContentAfter, Severity: diag.Error,
				Line: nl, Message: "content after the Timeline heading; Timeline must be the last section",
			})
		}
	}

	x := len(d.Src)
	if len(blocks) > 0 {
		b0 := blocks[0]
		if b0.DividerLine != 0 {
			x = d.LineStarts[b0.DividerLine-1]
		} else {
			x = b0.Heading.Start
		}
	}
	p.CompiledTruth = doc.Span{Start: d.Body.Start, End: x}

	return p
}

func parseOneBlock(d *doc.Doc, cfg config.Timeline, headingRe *regexp.Regexp, n int) Block {
	b := Block{HeadingLine: n}

	headStart := d.LineStarts[n-1]
	headEnd := len(d.Src)
	if n < d.NumLines() {
		headEnd = d.LineStarts[n]
	}
	b.Heading = doc.Span{Start: headStart, End: headEnd}

	bodyStart := headEnd
	bodyEnd := len(d.Src)
	nextLine := 0
	for m := n + 1; m <= d.NumLines(); m++ {
		if reNextHeading.MatchString(d.Line(m)) {
			bodyEnd = d.LineStarts[m-1]
			nextLine = m
			break
		}
	}
	b.Body = doc.Span{Start: bodyStart, End: bodyEnd}
	b.NextHeadingLine = nextLine

	// Divider: walk up skipping blank lines; first non-blank line, if it
	// equals the divider and lies after the frontmatter, is DividerLine.
	for m := n - 1; m >= 1; m-- {
		line := d.Line(m)
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.TrimRight(line, " \t\r") == cfg.Divider && d.LineStarts[m-1] >= d.Body.Start {
			b.DividerLine = m
		}
		break
	}

	entries, gaps, leading, trailing, preamble, diags := parseBody(n, d.Src[bodyStart:bodyEnd])
	b.Entries = entries
	b.Gaps = gaps
	b.LeadingBlanks = leading
	b.TrailingBlanks = trailing
	b.Preamble = preamble
	b.Diags = diags
	return b
}

var (
	reForbiddenDivider = regexp.MustCompile(`^---\s*$`)
	reForbiddenSub     = regexp.MustCompile(`^#{3,} `)
	reForbiddenFence   = regexp.MustCompile("^```")
	reEntryStart       = regexp.MustCompile(`^- \*\*([^*]+)\*\*`)
	reEntryToken       = regexp.MustCompile(`^- \*\*[^*]+\*\*`)
	reEntryShape       = regexp.MustCompile(`^ \| (\S.*?) — (\S.*)$`)
)

type bodyParser struct {
	entries  []Entry
	gaps     []int
	pending  int
	preamble []string
	diags    []diag.Diag
}

// attachRaw appends line to the previous entry (flushing pending blanks
// first) without any format diagnostic. Shared by attach and the
// unparseable-date case, which reports its own TL007 instead.
func (s *bodyParser) attachRaw(line string, lineNo int) {
	prev := &s.entries[len(s.entries)-1]
	for k := 0; k < s.pending; k++ {
		prev.Lines = append(prev.Lines, "")
	}
	s.pending = 0
	prev.Lines = append(prev.Lines, line)
	prev.Attached = append(prev.Attached, lineNo)
}

func (s *bodyParser) attach(line string, lineNo int) {
	s.attachRaw(line, lineNo)
	if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
		s.diags = append(s.diags, diag.Diag{
			Code: diag.TL007LooseLine, Severity: diag.Error, Line: lineNo,
			Message: "loose line: continuation lines must be indented",
		})
	}
}

// handleOther implements §5.4(d): any non-blank, non-entry, non-forbidden
// line. With no entries yet it becomes preamble content (Blocking); with
// entries, it is attached to the previous one.
func (s *bodyParser) handleOther(line string, lineNo int) {
	if len(s.entries) == 0 {
		s.preamble = append(s.preamble, line)
		s.diags = append(s.diags, diag.Diag{
			Code: diag.TL007LooseLine, Severity: diag.Error, Line: lineNo,
			Message: "content before first entry", Blocking: true,
		})
		return
	}
	s.attach(line, lineNo)
}

// parseBody implements DESIGN.md §5.4 over one block's body text.
func parseBody(headingLine int, body []byte) (entries []Entry, gaps []int, leading, trailing int, preamble []string, diags []diag.Diag) {
	lines := strings.Split(string(body), "\n")
	n := len(lines)
	st := &bodyParser{}

	i := 0
	for i < n && strings.TrimSpace(lines[i]) == "" {
		leading++
		i++
	}

	for i < n {
		line := lines[i]
		lineNo := headingLine + 1 + i

		if strings.TrimSpace(line) == "" {
			blanks := 0
			for i < n && strings.TrimSpace(lines[i]) == "" {
				blanks++
				i++
			}
			if i >= n {
				trailing = blanks
				break
			}
			if len(st.entries) == 0 {
				for k := 0; k < blanks; k++ {
					st.preamble = append(st.preamble, "")
				}
			} else {
				st.pending += blanks
			}
			continue
		}

		if reForbiddenDivider.MatchString(line) || reForbiddenSub.MatchString(line) || reForbiddenFence.MatchString(line) {
			st.diags = append(st.diags, diag.Diag{
				Code: diag.TL004Forbidden, Severity: diag.Error, Line: lineNo,
				Message:  "forbidden line (divider, sub-heading or code fence) inside a Timeline block",
				Blocking: true,
			})
			if len(st.entries) == 0 {
				st.preamble = append(st.preamble, line)
			} else {
				st.attach(line, lineNo)
			}
			i++
			continue
		}

		if m := reEntryStart.FindStringSubmatch(line); m != nil {
			date, ok := ParseDate(m[1])
			if ok {
				if len(st.entries) > 0 {
					st.gaps = append(st.gaps, st.pending)
					st.pending = 0
				}
				st.entries = append(st.entries, Entry{Line: lineNo, Date: date, Lines: []string{line}})
				if date.Precision != PrecDay {
					st.diags = append(st.diags, diag.Diag{
						Code: diag.TL008PartialDate, Severity: diag.Warning, Line: lineNo,
						Message: "partial date",
					})
				}
				if !date.Valid() {
					st.diags = append(st.diags, diag.Diag{
						Code: diag.TL010InvalidDate, Severity: diag.Error, Line: lineNo,
						Message: "impossible date",
					})
				}
			} else if len(st.entries) == 0 {
				st.preamble = append(st.preamble, line)
				st.diags = append(st.diags, diag.Diag{
					Code: diag.TL007LooseLine, Severity: diag.Error, Line: lineNo,
					Message: "unparseable date", Blocking: true,
				})
			} else {
				st.attachRaw(line, lineNo)
				st.diags = append(st.diags, diag.Diag{
					Code: diag.TL007LooseLine, Severity: diag.Error, Line: lineNo,
					Message: "unparseable date",
				})
			}
			i++
			continue
		}

		st.handleOther(line, lineNo)
		i++
	}

	if len(st.entries) == 0 {
		st.diags = append(st.diags, diag.Diag{
			Code: diag.TL009EmptyTimeline, Severity: diag.Warning, Line: headingLine,
			Message: "Timeline heading with no entries", Blocking: true,
		})
	}
	for i := 1; i < len(st.entries); i++ {
		if st.entries[i].Date.Key < st.entries[i-1].Date.Key {
			st.diags = append(st.diags, diag.Diag{
				Code: diag.TL005NotAscending, Severity: diag.Error, Line: st.entries[i].Line,
				Message: fmt.Sprintf("out of order: %s after %s", st.entries[i].Date.Key, st.entries[i-1].Date.Key),
			})
		}
	}
	for i := range st.entries {
		if !entryShapeOK(joinedText(st.entries[i])) {
			st.diags = append(st.diags, diag.Diag{
				Code: diag.TL006EntryFormat, Severity: diag.Warning, Line: st.entries[i].Line,
				Message: "entry must read '- **YYYY-MM-DD** | source — what'",
			})
		}
	}

	return st.entries, st.gaps, leading, trailing, st.preamble, st.diags
}

func joinedText(e Entry) string {
	parts := []string{e.Lines[0]}
	for _, l := range e.Lines[1:] {
		if strings.TrimSpace(l) == "" {
			continue
		}
		parts = append(parts, strings.TrimSpace(l))
	}
	return strings.Join(parts, " ")
}

func entryShapeOK(joined string) bool {
	loc := reEntryToken.FindStringIndex(joined)
	if loc == nil || loc[0] != 0 {
		return false
	}
	rest := joined[loc[1]:]
	m := reEntryShape.FindStringSubmatch(rest)
	if m == nil {
		return false
	}
	if strings.HasPrefix(strings.TrimSpace(m[1]), "—") {
		return false
	}
	return true
}

// AllDiags returns page + block diags sorted by (Line, Code), Path set.
func (p *Page) AllDiags() []diag.Diag {
	out := append([]diag.Diag(nil), p.Diags...)
	for i := range p.Blocks {
		out = append(out, p.Blocks[i].Diags...)
	}
	for i := range out {
		out[i].Path = p.Doc.Path
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		return out[i].Code < out[j].Code
	})
	return out
}
