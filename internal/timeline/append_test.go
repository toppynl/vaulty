package timeline

import (
	"strings"
	"testing"

	"github.com/toppynl/vaulty/internal/config"
	"github.com/toppynl/vaulty/internal/doc"
)

func mustAppend(t *testing.T, src, entry string, opt AppendOptions) (*AppendResult, *Page) {
	t.Helper()
	cfg := config.Default()
	d := doc.Parse("x.md", []byte(src))
	p := Parse(d, cfg.Timeline)
	res, err := Append(p, entry, opt, cfg)
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	return res, p
}

func TestValidateEntryRejectsPartialDate(t *testing.T) {
	if _, _, err := ValidateEntry("- **2026-08** | Peep — x."); err == nil {
		t.Fatal("expected error for partial date")
	}
}

func TestValidateEntryPrependsDash(t *testing.T) {
	lines, date, err := ValidateEntry("**2026-08-01** | Peep — x.")
	if err != nil {
		t.Fatal(err)
	}
	if lines[0] != "- **2026-08-01** | Peep — x." {
		t.Errorf("lines[0] = %q", lines[0])
	}
	if date.Key != "2026-08-01" {
		t.Errorf("date.Key = %q", date.Key)
	}
}

func TestValidateEntryRejectsBadShape(t *testing.T) {
	if _, _, err := ValidateEntry("- **2026-08-01** | no dash"); err == nil {
		t.Fatal("expected error for missing em dash")
	}
}

func TestAppendNewSectionNoDivider(t *testing.T) {
	src := "---\ntitle: X\n---\n\n# X\n\nbody\n"
	res, _ := mustAppend(t, src, "- **2026-09-15** | Peep — a.", AppendOptions{Today: "2026-09-15"})
	if res.Position != PosNewSection || !res.CreatedSection {
		t.Fatalf("position=%s createdSection=%v", res.Position, res.CreatedSection)
	}
	want := "---\ntitle: X\n---\n\n# X\n\nbody\n\n---\n\n## Timeline\n\n- **2026-09-15** | Peep — a.\n"
	if string(res.New) != want {
		t.Errorf("New =\n%q\nwant\n%q", res.New, want)
	}
}

func TestAppendNewSectionExistingDivider(t *testing.T) {
	src := "---\ntitle: X\n---\n\n# X\n\nbody\n\n---\n"
	res, _ := mustAppend(t, src, "- **2026-09-15** | Peep — a.", AppendOptions{Today: "2026-09-15"})
	want := "---\ntitle: X\n---\n\n# X\n\nbody\n\n---\n\n## Timeline\n\n- **2026-09-15** | Peep — a.\n"
	if string(res.New) != want {
		t.Errorf("New =\n%q\nwant\n%q", res.New, want)
	}
}

func TestAppendEmptyBlock(t *testing.T) {
	src := "---\ntitle: X\n---\n\nbody\n\n---\n\n## Timeline\n"
	res, _ := mustAppend(t, src, "- **2026-09-15** | Peep — a.", AppendOptions{Today: "2026-09-15"})
	want := "---\ntitle: X\n---\n\nbody\n\n---\n\n## Timeline\n\n- **2026-09-15** | Peep — a.\n"
	if string(res.New) != want {
		t.Errorf("New =\n%q\nwant\n%q", res.New, want)
	}
}

func TestAppendPositionEndStartMiddle(t *testing.T) {
	src := "---\ntitle: X\n---\n\nbody\n\n---\n\n## Timeline\n\n" +
		"- **2026-08-01** | Peep — a.\n" +
		"- **2026-08-03** | Peep — c.\n"

	resEnd, _ := mustAppend(t, src, "- **2026-08-05** | Peep — end.", AppendOptions{Today: "2026-09-15"})
	if resEnd.Position != PosEnd {
		t.Errorf("end: position = %s", resEnd.Position)
	}

	resStart, _ := mustAppend(t, src, "- **2026-07-01** | Peep — start.", AppendOptions{Today: "2026-09-15"})
	if resStart.Position != PosStart {
		t.Errorf("start: position = %s", resStart.Position)
	}

	resMiddle, _ := mustAppend(t, src, "- **2026-08-02** | Peep — middle.", AppendOptions{Today: "2026-09-15"})
	if resMiddle.Position != PosMiddle {
		t.Errorf("middle: position = %s", resMiddle.Position)
	}
	if !strings.Contains(string(resMiddle.New), "a.\n- **2026-08-02** | Peep — middle.\n- **2026-08-03**") {
		t.Errorf("middle insertion not between a and c:\n%s", resMiddle.New)
	}
}

func TestAppendSameDateGoesAfterExisting(t *testing.T) {
	src := "---\ntitle: X\n---\n\nbody\n\n---\n\n## Timeline\n\n" +
		"- **2026-08-01** | Peep — a.\n" +
		"- **2026-08-01** | Peep — b.\n"
	res, _ := mustAppend(t, src, "- **2026-08-01** | Peep — c.", AppendOptions{Today: "2026-09-15"})
	if res.Position != PosEnd {
		t.Errorf("position = %s, want end (same-date goes after existing)", res.Position)
	}
	if !strings.HasSuffix(strings.TrimRight(string(res.New), "\n"), "c.") {
		t.Errorf("New does not end with the new same-date entry:\n%s", res.New)
	}
}

func TestAppendGapStyleZeroOneAuto(t *testing.T) {
	srcGap0 := "---\ntitle: X\n---\n\nbody\n\n---\n\n## Timeline\n\n" +
		"- **2026-08-01** | Peep — a.\n" +
		"- **2026-08-02** | Peep — b.\n"
	res0, _ := mustAppend(t, srcGap0, "- **2026-08-03** | Peep — c.", AppendOptions{Today: "2026-09-15"})
	if strings.Contains(string(res0.New), "b.\n\n- **2026-08-03**") {
		t.Errorf("expected gap 0 (auto from existing gap-0 block):\n%s", res0.New)
	}

	srcGap1 := "---\ntitle: X\n---\n\nbody\n\n---\n\n## Timeline\n\n" +
		"- **2026-08-01** | Peep — a.\n\n" +
		"- **2026-08-02** | Peep — b.\n"
	res1, _ := mustAppend(t, srcGap1, "- **2026-08-03** | Peep — c.", AppendOptions{Today: "2026-09-15"})
	if !strings.Contains(string(res1.New), "b.\n\n- **2026-08-03**") {
		t.Errorf("expected gap 1 (auto from existing gap-1 block):\n%s", res1.New)
	}
}

func TestAppendDuplicate(t *testing.T) {
	src := "---\ntitle: X\n---\n\nbody\n\n---\n\n## Timeline\n\n" +
		"- **2026-08-01** | Peep — a.\n"
	res, _ := mustAppend(t, src, "- **2026-08-01** | Peep — a.", AppendOptions{Today: "2026-09-15"})
	if !res.AlreadyPresent {
		t.Fatal("expected AlreadyPresent")
	}
}

func TestAppendTouch(t *testing.T) {
	src := "---\ntitle: X\nupdated: 2026-01-01\n---\n\nbody\n\n---\n\n## Timeline\n\n" +
		"- **2026-08-01** | Peep — a.\n"
	res, _ := mustAppend(t, src, "- **2026-08-03** | Peep — b.", AppendOptions{Touch: true, Today: "2026-09-15"})
	if !res.Touched {
		t.Fatal("expected Touched")
	}
	if !strings.Contains(string(res.New), "updated: 2026-09-15") {
		t.Errorf("frontmatter not touched:\n%s", res.New)
	}
}

// TestAppendTouchFutureDate: --touch always sets updated: to today, even
// overriding an existing future date (Peep's decision, 2026-09-15: "zet op
// vandaag" — DESIGN.md §8.5).
func TestAppendTouchFutureDate(t *testing.T) {
	src := "---\ntitle: X\nupdated: 2026-09-20\n---\n\nbody\n\n---\n\n## Timeline\n\n" +
		"- **2026-08-01** | Peep — a.\n"
	res, _ := mustAppend(t, src, "- **2026-08-03** | Peep — b.", AppendOptions{Touch: true, Today: "2026-09-15"})
	if !res.Touched {
		t.Fatal("expected Touched: today must override a future updated: date")
	}
	if !strings.Contains(string(res.New), "updated: 2026-09-15\n") {
		t.Errorf("updated: not set to today:\n%s", res.New)
	}
}

// TestAppendTouchAlreadyToday: no-op when updated: already reads today.
func TestAppendTouchAlreadyToday(t *testing.T) {
	src := "---\ntitle: X\nupdated: 2026-09-15\n---\n\nbody\n\n---\n\n## Timeline\n\n" +
		"- **2026-08-01** | Peep — a.\n"
	res, _ := mustAppend(t, src, "- **2026-08-03** | Peep — b.", AppendOptions{Touch: true, Today: "2026-09-15"})
	if res.Touched {
		t.Fatal("should not touch when updated: already reads today")
	}
}

func TestAppendTouchMissingKey(t *testing.T) {
	src := "---\ntitle: X\n---\n\nbody\n\n---\n\n## Timeline\n\n" +
		"- **2026-08-01** | Peep — a.\n"
	res, _ := mustAppend(t, src, "- **2026-08-03** | Peep — b.", AppendOptions{Touch: true, Today: "2026-09-15"})
	if !res.Touched {
		t.Fatal("expected Touched when key is missing")
	}
	if !strings.Contains(string(res.New), "\nupdated: 2026-09-15\n---\n") {
		t.Errorf("key not inserted before closing ---:\n%s", res.New)
	}
}

// TestAppendSameDateMiddleReportsCorrectLine is a regression test for the
// review finding that locateNewEntryLine matched the new entry by date key,
// which finds the FIRST entry with that key — the pre-existing one — when
// the new entry shares its date with an entry already in the middle of the
// block. Same-date entries insert after the existing ones (DESIGN.md §8.4),
// so the new entry's line must be one past the existing same-date entry's
// line, not equal to it.
func TestAppendSameDateMiddleReportsCorrectLine(t *testing.T) {
	src := "---\ntitle: X\n---\n\nbody\n\n---\n\n## Timeline\n\n" +
		"- **2026-01-01** | Bron A — eerste.\n" +
		"- **2026-01-05** | Bron B — bestaand.\n" +
		"- **2026-01-10** | Bron C — laatste.\n"
	res, _ := mustAppend(t, src, "- **2026-01-05** | Bron D — nieuw.", AppendOptions{Today: "2026-09-15"})

	if res.Position != PosMiddle {
		t.Fatalf("Position = %s, want middle", res.Position)
	}
	lines := strings.Split(string(res.New), "\n")
	if res.Line < 1 || res.Line > len(lines) {
		t.Fatalf("Line %d out of range (%d lines)", res.Line, len(lines))
	}
	got := lines[res.Line-1]
	want := "- **2026-01-05** | Bron D — nieuw."
	if got != want {
		t.Errorf("reported line %d reads %q, want %q", res.Line, got, want)
	}
}

func TestAppendRefusalsUnsortedAndMultipleBlocks(t *testing.T) {
	unsorted := "---\ntitle: X\n---\n\nbody\n\n---\n\n## Timeline\n\n" +
		"- **2026-08-03** | Peep — a.\n" +
		"- **2026-08-01** | Peep — b.\n"
	if _, err := Append(parseFor(t, unsorted), "- **2026-08-05** | Peep — c.", AppendOptions{Today: "2026-09-15"}, config.Default()); err == nil {
		t.Error("expected refusal on unsorted block")
	}

	multi := "---\ntitle: X\n---\n\nbody\n\n---\n\n## Timeline\n\n" +
		"- **2026-08-01** | Peep — a.\n\n" +
		"---\n\n## Timeline\n\n" +
		"- **2026-08-02** | Peep — b.\n"
	if _, err := Append(parseFor(t, multi), "- **2026-08-05** | Peep — c.", AppendOptions{Today: "2026-09-15"}, config.Default()); err == nil {
		t.Error("expected refusal on multiple blocks")
	}
}

func TestAppendFutureDateWarnsButStillAppends(t *testing.T) {
	src := "---\ntitle: X\n---\n\nbody\n\n---\n\n## Timeline\n\n" +
		"- **2026-08-01** | Peep — a.\n"
	res, _ := mustAppend(t, src, "- **2026-12-25** | Peep — future.", AppendOptions{Today: "2026-09-15"})
	if !res.FutureDate {
		t.Error("expected FutureDate flag")
	}
	if res.AlreadyPresent {
		t.Error("should still append despite future date")
	}
}

func parseFor(t *testing.T, src string) *Page {
	t.Helper()
	d := doc.Parse("x.md", []byte(src))
	return Parse(d, config.Default().Timeline)
}
