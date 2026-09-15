package doc

import "testing"

func TestParseFrontmatter(t *testing.T) {
	src := "---\ntitle: X\nupdated: 2026-01-01\n---\n\n# X\nbody\n"
	d := Parse("x.md", []byte(src))
	if !d.HasFM {
		t.Fatal("expected frontmatter")
	}
	if d.FMUnclosed {
		t.Fatal("should not be unclosed")
	}
	wantFM := "---\ntitle: X\nupdated: 2026-01-01\n---\n"
	if got := string(d.Src[d.Frontmatter.Start:d.Frontmatter.End]); got != wantFM {
		t.Errorf("frontmatter span = %q, want %q", got, wantFM)
	}
	wantBody := "\n# X\nbody\n"
	if got := string(d.Src[d.Body.Start:d.Body.End]); got != wantBody {
		t.Errorf("body span = %q, want %q", got, wantBody)
	}
}

func TestParseNoFrontmatter(t *testing.T) {
	src := "# X\nno frontmatter here\n"
	d := Parse("x.md", []byte(src))
	if d.HasFM || d.FMUnclosed {
		t.Fatal("should have no frontmatter")
	}
	if string(d.Src[d.Body.Start:d.Body.End]) != src {
		t.Error("body should be the whole file")
	}
}

func TestParseUnclosedFrontmatter(t *testing.T) {
	src := "---\ntitle: X\nno closing delimiter\n"
	d := Parse("x.md", []byte(src))
	if d.HasFM {
		t.Fatal("should not report HasFM without a closing delimiter")
	}
	if !d.FMUnclosed {
		t.Fatal("expected FMUnclosed")
	}
}

func TestParseCRLF(t *testing.T) {
	src := "---\r\ntitle: X\r\n---\r\n\r\nbody\r\n"
	d := Parse("x.md", []byte(src))
	if !d.HasFM {
		t.Fatal("expected frontmatter with CRLF delimiters")
	}
	wantFM := "---\r\ntitle: X\r\n---\r\n"
	if got := string(d.Src[d.Frontmatter.Start:d.Frontmatter.End]); got != wantFM {
		t.Errorf("frontmatter span = %q, want %q", got, wantFM)
	}
}

func TestParseNoTrailingNewline(t *testing.T) {
	src := "---\ntitle: X\n---\nbody, no trailing newline"
	d := Parse("x.md", []byte(src))
	if !d.HasFM {
		t.Fatal("expected frontmatter")
	}
	if got := string(d.Src[d.Body.Start:d.Body.End]); got != "body, no trailing newline" {
		t.Errorf("body = %q", got)
	}
	if d.NumLines() != 4 {
		t.Errorf("NumLines() = %d, want 4", d.NumLines())
	}
	if d.Line(4) != "body, no trailing newline" {
		t.Errorf("Line(4) = %q", d.Line(4))
	}
}

func TestLineOf(t *testing.T) {
	src := "aaa\nbb\ncccc\n"
	d := Parse("x.md", []byte(src))
	// line 1: "aaa\n" -> offsets 0..3
	// line 2: "bb\n"  -> offsets 4..6
	// line 3: "cccc\n" -> offsets 7..11
	cases := []struct {
		off  int
		want int
	}{
		{0, 1}, {2, 1}, {3, 1}, {4, 2}, {6, 2}, {7, 3}, {11, 3},
	}
	for _, c := range cases {
		if got := d.LineOf(c.off); got != c.want {
			t.Errorf("LineOf(%d) = %d, want %d", c.off, got, c.want)
		}
	}
}

func TestParseEmptyFile(t *testing.T) {
	d := Parse("x.md", []byte(""))
	if d.HasFM || d.FMUnclosed {
		t.Fatal("empty file should have no frontmatter")
	}
	if d.NumLines() != 1 {
		t.Errorf("NumLines() = %d, want 1", d.NumLines())
	}
}

func TestParseFrontmatterDashesOnlyFile(t *testing.T) {
	// A lone "---" line with nothing after it: too short to be an opened,
	// unclosed frontmatter block (there's no second line to search for a
	// closing delimiter on).
	d := Parse("x.md", []byte("---\n"))
	if d.FMUnclosed {
		t.Fatal("did not expect FMUnclosed for a single-line file")
	}
}
