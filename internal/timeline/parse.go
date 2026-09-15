// Package timeline parses, sorts, serializes and appends '## Timeline'
// blocks. Behaviour is pinned to the Node oracle scripts/lib/timeline.mjs
// in the me vault (DESIGN.md §5), but Parse never stops: every problem
// becomes a diag.Diag and no line is ever dropped.
package timeline

import (
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

// Parse builds a Page. Never fails; problems are Diags.
func Parse(d *doc.Doc, cfg config.Timeline) *Page {
	// TODO(step 2): DESIGN.md §5.2–5.4.
	return &Page{Doc: d, CompiledTruth: d.Body}
}

// AllDiags returns page + block diags in line order.
func (p *Page) AllDiags() []diag.Diag {
	out := append([]diag.Diag(nil), p.Diags...)
	for i := range p.Blocks {
		out = append(out, p.Blocks[i].Diags...)
	}
	// TODO(step 2): sort by Line, then Code.
	return out
}
