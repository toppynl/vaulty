package search

import (
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/blevesearch/bleve/v2/analysis"
)

// Snippet is one matching source line.
type Snippet struct {
	Line int    `json:"line"`
	Text string `json:"text"`
}

const (
	maxSnippets     = 2   // per hit
	maxSnippetBytes = 160 // per snippet, markup and ellipses included
	snippetLead     = 40  // bytes of context kept before the first match when a line is cut
	ellipsis        = "…"
)

// highlighter finds the query's clauses in single source lines, using the
// same analyzers the index uses, so a stemmed hit ("leveringen" ->
// "levering") highlights the word that actually matched.
type highlighter struct {
	analyzers []analysis.Analyzer // configured analyzers, then raw last
	raw       analysis.Analyzer
	clauses   []Clause
	// wordTerms[i][a]: set of analyzed terms of word clause i under analyzer a.
	wordTerms [][]map[string]bool
}

func newHighlighter(analyzers []analysis.Analyzer, raw analysis.Analyzer, clauses []Clause) *highlighter {
	all := append(append([]analysis.Analyzer{}, analyzers...), raw)
	h := &highlighter{analyzers: all, raw: raw, clauses: clauses}
	h.wordTerms = make([][]map[string]bool, len(clauses))
	for i, c := range clauses {
		if c.Kind != kindWord {
			continue
		}
		h.wordTerms[i] = make([]map[string]bool, len(all))
		for a, an := range all {
			set := map[string]bool{}
			for _, t := range analyzeTerms(an, c.Text) {
				set[t] = true
			}
			h.wordTerms[i][a] = set
		}
	}
	return h
}

type byteRange struct{ start, end int }

// match returns the number of distinct clauses found in line and the byte
// ranges to highlight.
func (h *highlighter) match(line string) (int, []byteRange) {
	src := []byte(line)
	streams := make([]analysis.TokenStream, len(h.analyzers))
	for a, an := range h.analyzers {
		streams[a] = an.Analyze(src)
	}
	rawStream := streams[len(streams)-1]

	var ranges []byteRange
	matched := 0
	for i, c := range h.clauses {
		found := false
		switch c.Kind {
		case kindWord:
			for a, ts := range streams {
				for _, t := range ts {
					if h.wordTerms[i][a][string(t.Term)] {
						found = true
						ranges = append(ranges, byteRange{t.Start, t.End})
					}
				}
			}
		case kindPhrase:
			n := len(c.terms)
			for s := 0; s+n <= len(rawStream); s++ {
				ok := true
				for k := 0; k < n; k++ {
					if string(rawStream[s+k].Term) != c.terms[k] {
						ok = false
						break
					}
				}
				if ok {
					found = true
					ranges = append(ranges, byteRange{rawStream[s].Start, rawStream[s+n-1].End})
				}
			}
		case kindFuzzy, kindPrefix:
			for _, t := range rawStream {
				term := string(t.Term)
				for _, qt := range c.terms {
					hit := false
					if c.Kind == kindPrefix {
						hit = strings.HasPrefix(term, qt)
					} else {
						hit = levenshteinWithin(term, qt, c.Fuzz)
					}
					if hit {
						found = true
						ranges = append(ranges, byteRange{t.Start, t.End})
						break
					}
				}
			}
		}
		if found {
			matched++
		}
	}
	return matched, mergeRanges(ranges)
}

func mergeRanges(rs []byteRange) []byteRange {
	if len(rs) == 0 {
		return nil
	}
	sort.Slice(rs, func(i, j int) bool {
		if rs[i].start != rs[j].start {
			return rs[i].start < rs[j].start
		}
		return rs[i].end > rs[j].end
	})
	out := []byteRange{rs[0]}
	for _, r := range rs[1:] {
		last := &out[len(out)-1]
		if r.start <= last.end {
			if r.end > last.end {
				last.end = r.end
			}
			continue
		}
		out = append(out, r)
	}
	return out
}

// snippets picks up to maxSnippets lines from the given 1-based inclusive
// line ranges: most distinct clauses matched first, earliest line on ties,
// then printed in line order.
func (h *highlighter) snippets(p *parsedPage, withTimeline bool) []Snippet {
	type cand struct {
		line   int
		score  int
		text   string
		ranges []byteRange
	}
	var cands []cand
	scan := func(first, last int) {
		for n := first; n <= last; n++ {
			// Source bold markers are dropped so they can't collide with
			// the "**" highlight markup.
			text := strings.ReplaceAll(p.doc.Line(n), "**", "")
			if strings.TrimSpace(text) == "" {
				continue
			}
			score, ranges := h.match(text)
			if score == 0 {
				continue
			}
			cands = append(cands, cand{line: n, score: score, text: text, ranges: ranges})
		}
	}
	scan(p.truthFirst, p.truthLast)
	if withTimeline {
		scan(p.timelineFirst, p.timelineLast)
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].score != cands[j].score {
			return cands[i].score > cands[j].score
		}
		return cands[i].line < cands[j].line
	})
	if len(cands) > maxSnippets {
		cands = cands[:maxSnippets]
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].line < cands[j].line })
	out := []Snippet{}
	for _, c := range cands {
		out = append(out, Snippet{Line: c.line, Text: renderSnippet(c.text, c.ranges, maxSnippetBytes)})
	}
	return out
}

// renderSnippet trims line, wraps each range in "**", and — when the result
// would exceed max bytes — cuts a window starting a little before the first
// match, marking cuts with "…". Never splits a UTF-8 rune or a "**" pair.
func renderSnippet(line string, ranges []byteRange, max int) string {
	lo := len(line) - len(strings.TrimLeft(line, " \t"))
	hi := len(strings.TrimRight(line, " \t\r"))
	if hi <= lo {
		return ""
	}
	var rs []byteRange
	for _, r := range ranges {
		if r.start < lo {
			r.start = lo
		}
		if r.end > hi {
			r.end = hi
		}
		if r.end > r.start {
			rs = append(rs, r)
		}
	}

	full := (hi - lo) + 4*len(rs)
	start := lo
	budget := max
	prefix := ""
	if full > max {
		if len(rs) > 0 && rs[0].start-snippetLead > lo {
			start = rs[0].start - snippetLead
			for start < hi && !utf8.RuneStart(line[start]) {
				start++
			}
			prefix = ellipsis
		}
		budget = max - len(prefix) - len(ellipsis)
	}

	var b strings.Builder
	b.WriteString(prefix)
	used := 0
	pos := start
	ri := 0
	for ri < len(rs) && rs[ri].end <= start {
		ri++
	}
	for pos < hi {
		if ri < len(rs) && rs[ri].start <= pos {
			r := rs[ri]
			if r.start < pos {
				r.start = pos
			}
			need := r.end - r.start + 4
			if used+need <= budget {
				b.WriteString("**")
				b.WriteString(line[r.start:r.end])
				b.WriteString("**")
				used += need
				pos = r.end
				ri++
				continue
			}
			ri++ // doesn't fit highlighted: fall through as plain text
		}
		_, w := utf8.DecodeRuneInString(line[pos:])
		if used+w > budget {
			break
		}
		b.WriteString(line[pos : pos+w])
		used += w
		pos += w
	}
	if pos < hi {
		b.WriteString(ellipsis)
	}
	return b.String()
}

// levenshteinWithin reports whether the rune edit distance between a and b
// is at most k.
func levenshteinWithin(a, b string, k int) bool {
	ra, rb := []rune(a), []rune(b)
	if d := len(ra) - len(rb); d > k || -d > k {
		return false
	}
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)] <= k
}
