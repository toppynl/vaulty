package section

import (
	"strings"
	"testing"

	"github.com/toppynl/vaulty/internal/config"
	"github.com/toppynl/vaulty/internal/doc"
	"github.com/toppynl/vaulty/internal/timeline"
)

func parse(t *testing.T, src string) (*timeline.Page, []doc.Heading) {
	t.Helper()
	d := doc.Parse("x.md", []byte(src))
	return timeline.Parse(d, config.Default().Timeline), doc.Headings(d)
}

// A level-1 heading's span runs over a "## Timeline" with no divider above
// it; Region must clamp it to compiled truth and mark the Timeline heading
// unwritable.
func TestRegionClampsToCompiledTruth(t *testing.T) {
	src := "# Top\n\nintro\n\n## Timeline\n\n- **2026-09-01** | a — b.\n"
	p, hs := parse(t, src)
	span, writable := Region(p.Doc, p, hs[0])
	if !writable || Text(p.Doc, span) != "# Top\n\nintro" {
		t.Errorf("Top: writable=%v text=%q", writable, Text(p.Doc, span))
	}
	if _, writable := Region(p.Doc, p, hs[1]); writable {
		t.Error("Timeline heading reported writable")
	}
}

// trimBlankLines must match the split-on-newline trim read has always used,
// or hashes printed before and after this package would disagree.
func TestTrimBlankLinesMatchesSplitTrim(t *testing.T) {
	for _, s := range []string{"", "\n", "\n\n", "a", "a\n", "\n \na\r\n\nb \n\t\n", "  \n x\n"} {
		span := trimBlankLines([]byte(s), 0, len(s))
		lines := strings.Split(s, "\n")
		i, j := 0, len(lines)
		for i < j && strings.TrimSpace(lines[i]) == "" {
			i++
		}
		for j > i && strings.TrimSpace(lines[j-1]) == "" {
			j--
		}
		if got, want := s[span.Start:span.End], strings.Join(lines[i:j], "\n"); got != want {
			t.Errorf("%q: got %q, want %q", s, got, want)
		}
	}
}
