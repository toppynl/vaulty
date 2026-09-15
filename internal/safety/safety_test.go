package safety

import (
	"testing"

	"github.com/toppynl/vaulty/internal/config"
)

// A valid append: inserting one entry at the end of an existing block.
const validOrig = "---\ntitle: X\nupdated: 2026-01-01\n---\n\nbody\n\n---\n\n## Timeline\n\n" +
	"- **2026-08-01** | Peep — a.\n"

const validNext = "---\ntitle: X\nupdated: 2026-01-01\n---\n\nbody\n\n---\n\n## Timeline\n\n" +
	"- **2026-08-01** | Peep — a.\n" +
	"- **2026-08-03** | Peep — b.\n"

func validExpect() Expect {
	return Expect{
		RegionStart:  len(validOrig) - len("- **2026-08-01** | Peep — a.\n"),
		RegionEnd:    len(validOrig),
		NewRegionEnd: len(validNext),
		Added:        []string{"- **2026-08-03** | Peep — b."},
	}
}

func TestVerifyValidAppend(t *testing.T) {
	if err := Verify([]byte(validOrig), []byte(validNext), validExpect(), config.Default()); err != nil {
		t.Fatalf("expected valid append to pass: %v", err)
	}
}

func TestVerifyRefusesDroppedLine(t *testing.T) {
	// Drop the existing entry line from `next`.
	bad := "---\ntitle: X\nupdated: 2026-01-01\n---\n\nbody\n\n---\n\n## Timeline\n\n" +
		"- **2026-08-03** | Peep — b.\n"
	e := validExpect()
	e.NewRegionEnd = len(bad)
	if err := Verify([]byte(validOrig), []byte(bad), e, config.Default()); err == nil {
		t.Fatal("expected refusal: dropped a line")
	}
}

func TestVerifyRefusesByteChangeBeforeRegion(t *testing.T) {
	bad := "---\ntitle: X\nupdated: 2026-01-01\n---\n\nBODY CHANGED\n\n---\n\n## Timeline\n\n" +
		"- **2026-08-01** | Peep — a.\n" +
		"- **2026-08-03** | Peep — b.\n"
	e := validExpect()
	e.RegionStart += len("BODY CHANGED") - len("body")
	e.RegionEnd += len("BODY CHANGED") - len("body")
	e.NewRegionEnd = len(bad)
	if err := Verify([]byte(validOrig), []byte(bad), e, config.Default()); err == nil {
		t.Fatal("expected refusal: bytes before region changed")
	}
}

func TestVerifyRefusesByteChangeAfterRegion(t *testing.T) {
	origWithTail := validOrig + "\nafter\n"
	nextWithTail := validNext + "\nAFTER CHANGED\n"
	e := validExpect()
	e.RegionEnd = len(validOrig)
	e.NewRegionEnd = len(validNext)
	if err := Verify([]byte(origWithTail), []byte(nextWithTail), e, config.Default()); err == nil {
		t.Fatal("expected refusal: bytes after region changed")
	}
}

func TestVerifyRefusesFrontmatterChangeWithoutFlag(t *testing.T) {
	bad := "---\ntitle: X\nupdated: 2026-09-15\n---\n\nbody\n\n---\n\n## Timeline\n\n" +
		"- **2026-08-01** | Peep — a.\n" +
		"- **2026-08-03** | Peep — b.\n"
	e := validExpect()
	if err := Verify([]byte(validOrig), []byte(bad), e, config.Default()); err == nil {
		t.Fatal("expected refusal: frontmatter changed without AllowUpdatedLine")
	}
}

func TestVerifyAllowsFrontmatterChangeWithFlag(t *testing.T) {
	bad := "---\ntitle: X\nupdated: 2026-09-15\n---\n\nbody\n\n---\n\n## Timeline\n\n" +
		"- **2026-08-01** | Peep — a.\n" +
		"- **2026-08-03** | Peep — b.\n"
	e := validExpect()
	e.AllowUpdatedLine = true
	if err := Verify([]byte(validOrig), []byte(bad), e, config.Default()); err != nil {
		t.Fatalf("expected the single updated: line change to pass: %v", err)
	}
}

func TestVerifyRefusesSecondFrontmatterLineChanged(t *testing.T) {
	bad := "---\ntitle: Y\nupdated: 2026-09-15\n---\n\nbody\n\n---\n\n## Timeline\n\n" +
		"- **2026-08-01** | Peep — a.\n" +
		"- **2026-08-03** | Peep — b.\n"
	e := validExpect()
	e.AllowUpdatedLine = true
	if err := Verify([]byte(validOrig), []byte(bad), e, config.Default()); err == nil {
		t.Fatal("expected refusal: a second frontmatter line (title) also changed")
	}
}

func TestVerifyRefusesUnsortedResult(t *testing.T) {
	bad := "---\ntitle: X\nupdated: 2026-01-01\n---\n\nbody\n\n---\n\n## Timeline\n\n" +
		"- **2026-08-03** | Peep — b.\n" +
		"- **2026-08-01** | Peep — a.\n"
	e := validExpect()
	e.NewRegionEnd = len(bad)
	if err := Verify([]byte(validOrig), []byte(bad), e, config.Default()); err == nil {
		t.Fatal("expected refusal: reparsed result is not ascending")
	}
}
