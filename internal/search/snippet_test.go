package search

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/blevesearch/bleve/v2/analysis"

	"github.com/toppynl/vaulty/internal/config"
)

// TestSnippetCutMultibyte: a long multibyte line with source **bold** is cut
// to at most 160 bytes, never mid-rune, with the match highlighted once and
// the source bold markers gone (DESIGN.md §19.4).
func TestSnippetCutMultibyte(t *testing.T) {
	im, err := newMapping([]string{"standard"})
	if err != nil {
		t.Fatal(err)
	}
	filler := strings.Repeat("überall ßtraße café ", 10) // 2-byte runes throughout
	src := "# Page\n\n" + filler + "the **leverancier** signed " + filler + "\n"
	p := parsePage("wiki/p.md", []byte(src), "", config.Default().Timeline)

	q, err := ParseQuery("leverancier")
	if err != nil {
		t.Fatal(err)
	}
	if err := q.resolve(im.AnalyzerNamed(rawAnalyzer)); err != nil {
		t.Fatal(err)
	}
	h := newHighlighter([]analysis.Analyzer{im.AnalyzerNamed("standard")}, im.AnalyzerNamed(rawAnalyzer), q.Clauses)
	got := h.snippets(p, false)
	if len(got) != 1 {
		t.Fatalf("snippets = %+v, want one", got)
	}
	s := got[0]
	if s.Line != 3 {
		t.Errorf("line = %d, want 3", s.Line)
	}
	if len(s.Text) > maxSnippetBytes || !utf8.ValidString(s.Text) {
		t.Errorf("snippet %d bytes, valid UTF-8 %v: %q", len(s.Text), utf8.ValidString(s.Text), s.Text)
	}
	if strings.Count(s.Text, "**leverancier**") != 1 || strings.Contains(s.Text, "****") {
		t.Errorf("want exactly one clean highlight, got %q", s.Text)
	}
	if !strings.HasPrefix(s.Text, ellipsis) || !strings.HasSuffix(s.Text, ellipsis) {
		t.Errorf("want ellipses at both cuts, got %q", s.Text)
	}
}
