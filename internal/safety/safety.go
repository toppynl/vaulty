// Package safety verifies a proposed rewrite before any write (DESIGN.md §8.6).
// Every command that writes a vault file must call Verify and refuse on error.
package safety

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/toppynl/vaulty/internal/config"
	"github.com/toppynl/vaulty/internal/diag"
	"github.com/toppynl/vaulty/internal/doc"
	"github.com/toppynl/vaulty/internal/timeline"
)

// Expect describes the only change a write is allowed to make.
type Expect struct {
	// Region in the original that may change, and where it ends in the new
	// content. For a created section: RegionStart = RegionEnd = len(orig).
	RegionStart, RegionEnd, NewRegionEnd int
	// Non-blank lines the new content gains (multiset). Nothing may be lost.
	Added []string
	// Frontmatter may differ only in the updated-key line.
	AllowUpdatedLine bool
	// Migrations (reorder only) also require equal blank-line counts.
	PreserveBlankCount bool
}

func refused(check string, format string, args ...any) error {
	return fmt.Errorf("safety check failed: %s: %s", check, fmt.Sprintf(format, args...))
}

// Verify returns nil when next is orig with exactly the expected change.
func Verify(orig, next []byte, e Expect, cfg *config.Config) error {
	origDoc := doc.Parse("", orig)
	nextDoc := doc.Parse("", next)

	origFM := orig[origDoc.Frontmatter.Start:origDoc.Frontmatter.End]
	nextFM := next[nextDoc.Frontmatter.Start:nextDoc.Frontmatter.End]

	// 1. Frontmatter.
	if e.AllowUpdatedLine {
		if err := verifyUpdatedLine(string(origFM), string(nextFM), cfg.Frontmatter.UpdatedKey); err != nil {
			return refused("frontmatter", "%v", err)
		}
	} else if !bytes.Equal(origFM, nextFM) {
		return refused("frontmatter", "changed")
	}
	delta := len(nextFM) - len(origFM)

	// 2. Before the region.
	if e.RegionStart < origDoc.Frontmatter.End || e.RegionStart > len(orig) {
		return refused("region", "RegionStart %d out of range", e.RegionStart)
	}
	beforeOrig := orig[origDoc.Frontmatter.End:e.RegionStart]
	beforeNextEnd := e.RegionStart + delta
	if beforeNextEnd < nextDoc.Frontmatter.End || beforeNextEnd > len(next) {
		return refused("before-region", "computed bound %d out of range", beforeNextEnd)
	}
	beforeNext := next[nextDoc.Frontmatter.End:beforeNextEnd]
	if !bytes.Equal(beforeOrig, beforeNext) {
		return refused("before-region", "bytes before the region changed")
	}

	// 3. After the region. NewRegionEnd is already an offset into `next`.
	if e.RegionEnd < e.RegionStart || e.RegionEnd > len(orig) {
		return refused("region", "RegionEnd %d out of range", e.RegionEnd)
	}
	if e.NewRegionEnd < beforeNextEnd || e.NewRegionEnd > len(next) {
		return refused("region", "NewRegionEnd %d out of range", e.NewRegionEnd)
	}
	afterOrig := orig[e.RegionEnd:]
	afterNext := next[e.NewRegionEnd:]
	if !bytes.Equal(afterOrig, afterNext) {
		return refused("after-region", "bytes after the region changed")
	}

	// 4. Non-blank line multiset: orig - {old KEY line} + {new KEY line} + Added.
	expected := newMultiset(nonBlankLines(orig))
	if e.AllowUpdatedLine {
		oldLine, oldOK := findKeyLine(string(origFM), cfg.Frontmatter.UpdatedKey)
		newLine, newOK := findKeyLine(string(nextFM), cfg.Frontmatter.UpdatedKey)
		if oldLine != newLine {
			if oldOK {
				expected.remove(oldLine)
			}
			if newOK {
				expected.add(newLine)
			}
		}
	}
	for _, l := range e.Added {
		if strings.TrimSpace(l) != "" {
			expected.add(l)
		}
	}
	actual := newMultiset(nonBlankLines(next))
	if !expected.equal(actual) {
		return refused("non-blank-lines", "multiset changed (expected orig - old key line + new key line + Added)")
	}

	// 5. Blank-line count (migrations only).
	if e.PreserveBlankCount {
		if blankCount(orig) != blankCount(next) {
			return refused("blank-count", "blank-line count changed")
		}
	}

	// 6. Reparse next: exactly one block, Sortable, Ascending, no TL001-003.
	page := timeline.Parse(nextDoc, cfg.Timeline)
	if len(page.Blocks) != 1 {
		return refused("reparse", "expected exactly one Timeline block, got %d", len(page.Blocks))
	}
	b := &page.Blocks[0]
	if !b.Sortable() {
		return refused("reparse", "resulting block is not Sortable")
	}
	if timeline.ClassifyOrder(b.Entries) != timeline.Ascending {
		return refused("reparse", "resulting block is not ascending")
	}
	for _, d := range page.Diags {
		if d.Code == diag.TL001MultipleTimelines || d.Code == diag.TL002MissingDivider || d.Code == diag.TL003ContentAfter {
			return refused("reparse", "%s: %s", d.Code, d.Message)
		}
	}

	return nil
}

// verifyUpdatedLine checks that origFM and nextFM are identical once every
// line starting with "KEY:" is removed from each side (at most one such
// line per side) — covering a changed value, a newly inserted key line, and
// the "already newer, unchanged" no-op case uniformly.
func verifyUpdatedLine(origFM, nextFM, key string) error {
	prefix := key + ":"
	origRest, origKeyCount := stripKeyLines(origFM, prefix)
	nextRest, nextKeyCount := stripKeyLines(nextFM, prefix)
	if origRest != nextRest {
		return fmt.Errorf("frontmatter differs outside the %s line", key)
	}
	if origKeyCount > 1 || nextKeyCount > 1 {
		return fmt.Errorf("more than one %s line", key)
	}
	return nil
}

func stripKeyLines(fm, prefix string) (string, int) {
	var kept []string
	count := 0
	for _, l := range strings.Split(fm, "\n") {
		if strings.HasPrefix(l, prefix) {
			count++
			continue
		}
		kept = append(kept, l)
	}
	return strings.Join(kept, "\n"), count
}

func findKeyLine(fm, key string) (string, bool) {
	prefix := key + ":"
	for _, l := range strings.Split(fm, "\n") {
		if strings.HasPrefix(l, prefix) {
			return l, true
		}
	}
	return "", false
}

func nonBlankLines(src []byte) []string {
	var out []string
	for _, l := range strings.Split(string(src), "\n") {
		if strings.TrimRight(l, "\r") != "" {
			out = append(out, l)
		}
	}
	return out
}

func blankCount(src []byte) int {
	n := 0
	for _, l := range strings.Split(string(src), "\n") {
		if strings.TrimRight(l, "\r") == "" {
			n++
		}
	}
	return n
}

type multiset map[string]int

func newMultiset(lines []string) multiset {
	m := multiset{}
	for _, l := range lines {
		m[l]++
	}
	return m
}

func (m multiset) add(s string)    { m[s]++ }
func (m multiset) remove(s string) { m[s]-- }

func (m multiset) equal(o multiset) bool {
	for k, v := range m {
		if v != 0 && o[k] != v {
			return false
		}
	}
	for k, v := range o {
		if v != 0 && m[k] != v {
			return false
		}
	}
	return true
}
