package doc

import "testing"

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
