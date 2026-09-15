package doc

import (
	"strings"
	"testing"
)

func TestHeadings(t *testing.T) {
	src := "---\ntitle: X\n---\n\n# Top\n\nintro\n\n## Alpha\n\nalpha body\n\n### Alpha Sub\n\nsub body\n\n## Beta\n\nbeta body\n"
	d := Parse("x.md", []byte(src))
	hs := Headings(d)

	want := []struct {
		line  int
		level int
		text  string
	}{
		{5, 1, "Top"},
		{9, 2, "Alpha"},
		{13, 3, "Alpha Sub"},
		{17, 2, "Beta"},
	}
	if len(hs) != len(want) {
		t.Fatalf("got %d headings, want %d: %+v", len(hs), len(want), hs)
	}
	for i, w := range want {
		if hs[i].Line != w.line || hs[i].Level != w.level || hs[i].Text != w.text {
			t.Errorf("heading %d: got %+v, want line=%d level=%d text=%q", i, hs[i], w.line, w.level, w.text)
		}
	}

	// "Alpha"'s section runs up to (not including) "## Beta" — its own
	// subheading "### Alpha Sub" is nested inside it.
	alpha := hs[1]
	beta := hs[3]
	if alpha.Span.End != beta.Span.Start {
		t.Errorf("Alpha span end = %d, want %d (Beta's start)", alpha.Span.End, beta.Span.Start)
	}
	// "Beta" runs to EOF.
	if beta.Span.End != len(src) {
		t.Errorf("Beta span end = %d, want EOF %d", beta.Span.End, len(src))
	}
}

func TestHeadingsSkipsFencedCode(t *testing.T) {
	src := "# Top\n\n```\n## not a heading\n```\n\n## Real\n"
	d := Parse("x.md", []byte(src))
	hs := Headings(d)
	if len(hs) != 2 {
		t.Fatalf("got %d headings, want 2: %+v", len(hs), hs)
	}
	if hs[1].Text != "Real" {
		t.Errorf("hs[1].Text = %q, want Real", hs[1].Text)
	}
}

func TestHeadingsClosingHashesAndTrailingHash(t *testing.T) {
	src := "## Closed ##\n\nbody\n\n## C#\n\nbody\n"
	d := Parse("x.md", []byte(src))
	hs := Headings(d)
	if len(hs) != 2 {
		t.Fatalf("got %d headings: %+v", len(hs), hs)
	}
	if hs[0].Text != "Closed" {
		t.Errorf("hs[0].Text = %q, want Closed", hs[0].Text)
	}
	if hs[1].Text != "C#" {
		t.Errorf("hs[1].Text = %q, want C#", hs[1].Text)
	}
}

func TestHeadingsNoFrontmatterNoHeadings(t *testing.T) {
	d := Parse("x.md", []byte("just text\nno headings here\n"))
	if hs := Headings(d); len(hs) != 0 {
		t.Errorf("got %d headings, want 0: %+v", len(hs), hs)
	}
}

func TestHeadingsStopAtDivider(t *testing.T) {
	src := "# Top\n\n## Related\n\nsome related text\n\n---\n\n## Timeline\n\n- entry\n"
	d := Parse("x.md", []byte(src))
	hs := Headings(d)
	if len(hs) != 3 {
		t.Fatalf("got %d headings, want 3: %+v", len(hs), hs)
	}
	related := hs[1]
	dividerStart := strings.Index(src, "---")
	if related.Span.End != dividerStart {
		t.Errorf("Related span end = %d, want %d (start of ---)", related.Span.End, dividerStart)
	}
	if got := string(d.Src[related.Span.Start:related.Span.End]); strings.Contains(got, "---") {
		t.Errorf("Related section contains the divider: %q", got)
	}
	// "Top" (level 1) is also closed by the divider: no later heading has
	// level <= 1, so without the divider rule it would run to EOF.
	top := hs[0]
	if top.Span.End != dividerStart {
		t.Errorf("Top span end = %d, want %d (start of ---)", top.Span.End, dividerStart)
	}
}

func TestHeadingsDividerInFencedCodeIgnored(t *testing.T) {
	src := "## Section\n\n```\n---\n```\n\nafter the fence\n"
	d := Parse("x.md", []byte(src))
	hs := Headings(d)
	if len(hs) != 1 {
		t.Fatalf("got %d headings, want 1: %+v", len(hs), hs)
	}
	if hs[0].Span.End != len(src) {
		t.Errorf("Section span end = %d, want EOF %d (the '---' inside the fence must not count)", hs[0].Span.End, len(src))
	}
}

func TestHeadingLinesAndBytes(t *testing.T) {
	src := "# Top\n\nline2\nline3\n"
	d := Parse("x.md", []byte(src))
	hs := Headings(d)
	if got := hs[0].Bytes(); got != len(src) {
		t.Errorf("Bytes() = %d, want %d", got, len(src))
	}
	if got := hs[0].Lines(d); got != 4 {
		t.Errorf("Lines() = %d, want 4", got)
	}
}
