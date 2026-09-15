package timeline

import (
	"testing"

	"github.com/toppynl/vaulty/internal/config"
	"github.com/toppynl/vaulty/internal/diag"
	"github.com/toppynl/vaulty/internal/doc"
)

func parseSrc(t *testing.T, src string) *Page {
	t.Helper()
	d := doc.Parse("x.md", []byte(src))
	return Parse(d, config.Default().Timeline)
}

func countCode(diags []diag.Diag, code diag.Code) int {
	n := 0
	for _, d := range diags {
		if d.Code == code {
			n++
		}
	}
	return n
}

const simplePage = "---\ntitle: X\n---\n\n# X\n\nbody\n\n---\n\n## Timeline\n\n" +
	"- **2026-08-01** | Peep — kickoff.\n" +
	"- **2026-08-03** | Peep — followup.\n"

func TestParseSimpleBlock(t *testing.T) {
	p := parseSrc(t, simplePage)
	if len(p.Blocks) != 1 {
		t.Fatalf("blocks = %d, want 1", len(p.Blocks))
	}
	b := p.Blocks[0]
	if !b.Sortable() {
		t.Fatalf("expected Sortable, diags=%v", b.Diags)
	}
	if len(b.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(b.Entries))
	}
	if b.DividerLine == 0 {
		t.Error("expected a divider line")
	}
	if hasCode(p.AllDiags(), diag.TL006EntryFormat) {
		t.Error("well-formed entries should not trigger TL006")
	}
}

func TestParseMonthOnlyAndRangeForms(t *testing.T) {
	src := "---\ntitle: X\n---\n\nbody\n\n---\n\n## Timeline\n\n" +
		"- **2026-07** | Peep — month only.\n" +
		"- **2026-08-2x** | Peep — decade.\n" +
		"- **2026-09-10/12** | Peep — day range.\n"
	p := parseSrc(t, src)
	b := p.Blocks[0]
	if len(b.Entries) != 3 {
		t.Fatalf("entries = %d, want 3", len(b.Entries))
	}
	if countCode(b.Diags, diag.TL008PartialDate) != 3 {
		t.Errorf("TL008 count = %d, want 3", countCode(b.Diags, diag.TL008PartialDate))
	}
}

func TestParseUnparseableAttachAfterEntries(t *testing.T) {
	src := "---\ntitle: X\n---\n\nbody\n\n---\n\n## Timeline\n\n" +
		"- **2026-08-01** | Peep — kickoff.\n" +
		"- **juli/augustus 2026** | Peep — vague.\n"
	p := parseSrc(t, src)
	b := p.Blocks[0]
	if len(b.Entries) != 1 {
		t.Fatalf("entries = %d, want 1 (unparseable date attaches to prev)", len(b.Entries))
	}
	if !b.Sortable() {
		t.Error("attaching an unparseable-date line to an existing entry must not block")
	}
	if countCode(b.Diags, diag.TL007LooseLine) != 1 {
		t.Errorf("TL007 count = %d, want 1", countCode(b.Diags, diag.TL007LooseLine))
	}
}

func TestParseUnparseableFirstEntryBlocks(t *testing.T) {
	src := "---\ntitle: X\n---\n\nbody\n\n---\n\n## Timeline\n\n" +
		"- **juli/augustus 2026** | Peep — vague.\n"
	p := parseSrc(t, src)
	b := p.Blocks[0]
	if b.Sortable() {
		t.Error("unparseable first entry must block")
	}
	if len(b.Entries) != 0 {
		t.Errorf("entries = %d, want 0", len(b.Entries))
	}
	if len(b.Preamble) == 0 {
		t.Error("expected the line in Preamble")
	}
}

func TestParseSkipCasesForbiddenAndLoose(t *testing.T) {
	src := "---\ntitle: X\n---\n\nbody\n\n---\n\n## Timeline\n\n" +
		"- **2026-08-01** | Peep — kickoff.\n" +
		"unindented continuation\n" +
		"```\n" +
		"- **2026-08-03** | Peep — next.\n"
	p := parseSrc(t, src)
	b := p.Blocks[0]
	if b.Sortable() {
		t.Error("a code fence inside the block must block (TL004)")
	}
	if !hasCode(b.Diags, diag.TL004Forbidden) {
		t.Error("expected TL004 for the code fence")
	}
	if !hasCode(b.Diags, diag.TL007LooseLine) {
		t.Error("expected TL007 for the unindented continuation")
	}
	// Nothing dropped: the loose line and the forbidden fence both attach
	// to the entry above them; the well-formed entry line after the fence
	// still starts a fresh entry (entry-start detection never depends on
	// what came before).
	if len(b.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(b.Entries))
	}
	if len(b.Entries[0].Lines) != 3 {
		t.Errorf("first entry Lines = %v, want 3 (itself + 2 attached lines)", b.Entries[0].Lines)
	}
}

func TestParseWhitespacePreservation(t *testing.T) {
	src := "---\ntitle: X\n---\n\nbody\n\n---\n\n## Timeline\n\n" +
		"- **2026-08-01** | Peep — kickoff; long entries\n" +
		"  wrap onto indented continuation lines.\n" +
		"\n" +
		"- **2026-08-03** | Peep — next.\n"
	p := parseSrc(t, src)
	b := p.Blocks[0]
	if !b.Sortable() {
		t.Fatalf("expected Sortable, diags=%v", b.Diags)
	}
	if len(b.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(b.Entries))
	}
	if len(b.Entries[0].Lines) != 2 {
		t.Errorf("first entry Lines = %v, want 2 lines", b.Entries[0].Lines)
	}
	if len(b.Gaps) != 1 || b.Gaps[0] != 1 {
		t.Errorf("gaps = %v, want [1]", b.Gaps)
	}
	round := SerializeBody(b.Entries, b.Gaps, b.LeadingBlanks, b.TrailingBlanks)
	orig := string(p.Doc.Src[b.Body.Start:b.Body.End])
	if round != orig {
		t.Errorf("round-trip mismatch:\n got %q\nwant %q", round, orig)
	}
}

func TestParseSafetyCheckTL005OutOfOrder(t *testing.T) {
	src := "---\ntitle: X\n---\n\nbody\n\n---\n\n## Timeline\n\n" +
		"- **2026-08-03** | Peep — later first.\n" +
		"- **2026-08-01** | Peep — earlier second.\n"
	p := parseSrc(t, src)
	b := p.Blocks[0]
	if !hasCode(b.Diags, diag.TL005NotAscending) {
		t.Error("expected TL005 for out-of-order entries")
	}
	if !b.Sortable() {
		t.Error("TL005 is not Blocking")
	}
}

func TestParseSameDateTieBreak(t *testing.T) {
	// Mixed-run tie-break: same-date entries around a descending context
	// keep the oracle's flip semantics (see sort_test.go for the sort
	// itself); here we only check parsing keeps all same-date entries.
	src := "---\ntitle: X\n---\n\nbody\n\n---\n\n## Timeline\n\n" +
		"- **2026-08-01** | Peep — a.\n" +
		"- **2026-08-01** | Peep — b.\n" +
		"- **2026-08-02** | Peep — c.\n"
	p := parseSrc(t, src)
	b := p.Blocks[0]
	if len(b.Entries) != 3 {
		t.Fatalf("entries = %d, want 3", len(b.Entries))
	}
}

func TestParseNoEntries(t *testing.T) {
	src := "---\ntitle: X\n---\n\nbody\n\n---\n\n## Timeline\n"
	p := parseSrc(t, src)
	b := p.Blocks[0]
	if b.Sortable() {
		t.Error("empty Timeline block must not be Sortable")
	}
	if !hasCode(b.Diags, diag.TL009EmptyTimeline) {
		t.Error("expected TL009")
	}
}

func TestParseInvalidDate(t *testing.T) {
	src := "---\ntitle: X\n---\n\nbody\n\n---\n\n## Timeline\n\n" +
		"- **2026-02-30** | Peep — impossible day.\n"
	p := parseSrc(t, src)
	b := p.Blocks[0]
	if !hasCode(b.Diags, diag.TL010InvalidDate) {
		t.Error("expected TL010 for 2026-02-30")
	}
}

func TestParseMultipleBlocks(t *testing.T) {
	src := "---\ntitle: X\n---\n\nbody\n\n---\n\n## Timeline\n\n" +
		"- **2026-08-01** | Peep — a.\n\n" +
		"---\n\n## Timeline\n\n" +
		"- **2026-08-02** | Peep — b.\n"
	p := parseSrc(t, src)
	if len(p.Blocks) != 2 {
		t.Fatalf("blocks = %d, want 2", len(p.Blocks))
	}
	if !hasCode(p.Diags, diag.TL001MultipleTimelines) {
		t.Error("expected TL001")
	}
}

func TestParseMissingDivider(t *testing.T) {
	src := "---\ntitle: X\n---\n\nbody\n\n## Timeline\n\n" +
		"- **2026-08-01** | Peep — a.\n"
	p := parseSrc(t, src)
	if !hasCode(p.Diags, diag.TL002MissingDivider) {
		t.Error("expected TL002")
	}
}

func TestParseContentAfterTimeline(t *testing.T) {
	src := "---\ntitle: X\n---\n\nbody\n\n---\n\n## Timeline\n\n" +
		"- **2026-08-01** | Peep — a.\n\n" +
		"## Another section\n\nmore text\n"
	p := parseSrc(t, src)
	if !hasCode(p.Diags, diag.TL003ContentAfter) {
		t.Error("expected TL003")
	}
}

func TestParseUnclosedFrontmatterFM001(t *testing.T) {
	src := "---\ntitle: X\nno closing\n\n## Timeline\n\n- **2026-08-01** | Peep — a.\n"
	p := parseSrc(t, src)
	if !hasCode(p.Diags, diag.FM001UnterminatedFrontmatter) {
		t.Error("expected FM001")
	}
}

func TestParseEntryFormatTL006(t *testing.T) {
	src := "---\ntitle: X\n---\n\nbody\n\n---\n\n## Timeline\n\n" +
		"- **2026-08-01** | no em dash here\n"
	p := parseSrc(t, src)
	b := p.Blocks[0]
	if !hasCode(b.Diags, diag.TL006EntryFormat) {
		t.Error("expected TL006 for a line without ' — '")
	}
}

func TestRoundTripSerializeAllTestPages(t *testing.T) {
	pages := []string{simplePage}
	for _, src := range pages {
		p := parseSrc(t, src)
		for _, b := range p.Blocks {
			if !b.Sortable() || len(b.Preamble) > 0 {
				continue
			}
			got := SerializeBody(b.Entries, b.Gaps, b.LeadingBlanks, b.TrailingBlanks)
			want := string(p.Doc.Src[b.Body.Start:b.Body.End])
			if got != want {
				t.Errorf("round-trip mismatch:\n got %q\nwant %q", got, want)
			}
		}
	}
}
