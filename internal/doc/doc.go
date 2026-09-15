// Package doc holds the raw bytes of one markdown file plus a line index and
// the frontmatter span. It never normalizes anything (DESIGN.md §5.1).
package doc

import "bytes"

// Span is a half-open byte range [Start, End) into Doc.Src.
type Span struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

func (s Span) Len() int { return s.End - s.Start }

type Doc struct {
	Path        string // vault-relative, slash-separated
	Src         []byte
	LineStarts  []int // byte offset of each line; LineStarts[0] == 0
	Frontmatter Span  // both '---' lines incl. trailing newline; {0,0} if none
	HasFM       bool
	FMUnclosed  bool // starts with '---' but no closing delimiter (FM001)
	Body        Span // Frontmatter.End .. len(Src)
}

// Parse indexes src. Frontmatter = file starts with "---\n" (or "---\r\n")
// and a later line equal to "---" (trailing spaces/\r allowed) closes it.
func Parse(path string, src []byte) *Doc {
	d := &Doc{Path: path, Src: src}
	d.LineStarts = append(d.LineStarts, 0)
	for i, c := range src {
		if c == '\n' && i+1 < len(src) {
			d.LineStarts = append(d.LineStarts, i+1)
		}
	}
	d.Body = Span{0, len(src)}
	if isDelim(d.lineBytes(0)) && len(d.LineStarts) > 1 {
		for n := 1; n < len(d.LineStarts); n++ {
			if isDelim(d.lineBytes(n)) {
				end := len(src)
				if n+1 < len(d.LineStarts) {
					end = d.LineStarts[n+1]
				}
				d.Frontmatter = Span{0, end}
				d.HasFM = true
				d.Body = Span{end, len(src)}
				return d
			}
		}
		d.FMUnclosed = true
	}
	return d
}

func isDelim(line []byte) bool {
	return string(bytes.TrimRight(line, " \t\r")) == "---"
}

// lineBytes returns line n (0-based) without its trailing '\n'.
func (d *Doc) lineBytes(n int) []byte {
	start := d.LineStarts[n]
	end := len(d.Src)
	if n+1 < len(d.LineStarts) {
		end = d.LineStarts[n+1] - 1
	} else if end > start && d.Src[end-1] == '\n' {
		end--
	}
	return d.Src[start:end]
}

// NumLines is the number of lines (a trailing '\n' does not start a new line).
func (d *Doc) NumLines() int { return len(d.LineStarts) }

// Line returns 1-based line n without its newline.
func (d *Doc) Line(n int) string { return string(d.lineBytes(n - 1)) }

// LineOf returns the 1-based line containing byte offset off.
func (d *Doc) LineOf(off int) int {
	lo, hi := 0, len(d.LineStarts)-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if d.LineStarts[mid] <= off {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo + 1
}
