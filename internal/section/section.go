// Package section locates a heading's section region inside a page and
// builds section-level edits (replace, append, insert-after) for
// `vaulty write`. `vaulty read --section` uses the same Region/Hash, so a
// hash printed by read is exactly what write checks with --if-hash.
package section

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/toppynl/vaulty/internal/doc"
	"github.com/toppynl/vaulty/internal/timeline"
)

// ErrRefused wraps every reason an edit is declined (exit 3).
var ErrRefused = errors.New("refused")

// Region is a heading's section as read and written: the heading's span
// (heading line to the next heading of equal-or-higher level, a standalone
// "---" or EOF), clamped to the page's compiled-truth end when the heading
// starts inside compiled truth — so the last compiled-truth section never
// swallows the Timeline divider/block — with blank leading/trailing lines
// trimmed. Writable is false for a heading at or after the compiled-truth
// end (the Timeline heading, or anything below the divider).
func Region(d *doc.Doc, p *timeline.Page, h doc.Heading) (span doc.Span, writable bool) {
	start, end := h.Span.Start, h.Span.End
	writable = start < p.CompiledTruth.End
	if writable && end > p.CompiledTruth.End {
		end = p.CompiledTruth.End
	}
	return trimBlankLines(d.Src, start, end), writable
}

// Text is the region's bytes as a string.
func Text(d *doc.Doc, span doc.Span) string {
	return string(d.Src[span.Start:span.End])
}

// Hash is the first 12 hex chars of sha256(text): short enough to pass on a
// command line, long enough that an accidental match is not a concern.
func Hash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])[:12]
}

// trimBlankLines narrows [start, end) to drop whitespace-only lines at
// either edge, byte-for-byte what splitting on "\n" and dropping blank edge
// lines gives: the result ends at the last non-blank line's end, before its
// "\n" (a trailing "\r" stays, as it does in the split form).
func trimBlankLines(src []byte, start, end int) doc.Span {
	s := start
	for s < end {
		nl := lineEnd(src, s, end)
		if strings.TrimSpace(string(src[s:nl])) != "" {
			break
		}
		if nl == end {
			return doc.Span{Start: end, End: end}
		}
		s = nl + 1
	}
	e := end
	for e > s {
		ls := s
		for i := e - 1; i >= s; i-- {
			if src[i] == '\n' {
				ls = i + 1
				break
			}
		}
		if strings.TrimSpace(string(src[ls:e])) != "" {
			break
		}
		if ls == s {
			return doc.Span{Start: s, End: s}
		}
		e = ls - 1
	}
	return doc.Span{Start: s, End: e}
}

// lineEnd is the offset of the "\n" ending the line at off, or end.
func lineEnd(src []byte, off, end int) int {
	for i := off; i < end; i++ {
		if src[i] == '\n' {
			return i
		}
	}
	return end
}

// Mode is which kind of section edit Build makes.
type Mode string

const (
	ModeReplace Mode = "replace"
	ModeAppend  Mode = "append"
	ModeAfter   Mode = "after"
)

// Edit is a computed section edit; nothing is written. RegionStart/End are
// offsets into the original source; NewRegion is the exact bytes that take
// their place. HeadingOffset is where the written section's heading starts
// in the edited source, and SectionText is what Region must read back there.
type Edit struct {
	Mode          Mode
	New           []byte
	RegionStart   int
	RegionEnd     int
	NewRegion     []byte
	HeadingOffset int
	Heading       string
	SectionText   string
}

// Build computes the edit of mode at heading h (already matched uniquely by
// the caller) with content (stdin, blank edge lines already trimmed, never
// empty). Every validation failure wraps ErrRefused.
func Build(p *timeline.Page, h doc.Heading, mode Mode, content string) (*Edit, error) {
	d := p.Doc
	hs := doc.Headings(d)
	region, writable := Region(d, p, h)
	if !writable {
		return nil, fmt.Errorf("%w: %q is not in compiled truth; use `vaulty timeline append` for Timeline entries", ErrRefused, h.Text)
	}
	own := contentHeadings(content)
	src := d.Src

	e := &Edit{Mode: mode}
	switch mode {
	case ModeReplace:
		if err := checkLeadHeading(own, h.Level, h.Level, "replacement"); err != nil {
			return nil, err
		}
		if err := checkDuplicateHeadings(own, hs, region); err != nil {
			return nil, err
		}
		e.RegionStart, e.RegionEnd = region.Start, region.End
		e.NewRegion = []byte(content)
		e.HeadingOffset = region.Start
		e.Heading = own[0].Text
		e.SectionText = content
	case ModeAppend:
		for _, ch := range own {
			if ch.Level <= h.Level {
				return nil, fmt.Errorf("%w: appended content has heading %q (level %d); only headings deeper than level %d stay inside the section", ErrRefused, ch.Text, ch.Level, h.Level)
			}
		}
		if err := checkDuplicateHeadings(own, hs, doc.Span{}); err != nil {
			return nil, err
		}
		sep := appendSeparator(Text(d, region), content)
		e.RegionStart, e.RegionEnd = region.End, region.End
		e.NewRegion = []byte(sep + content)
		e.HeadingOffset = region.Start
		e.Heading = h.Text
		e.SectionText = Text(d, region) + sep + content
	case ModeAfter:
		if err := checkLeadHeading(own, h.Level, 6, "new section"); err != nil {
			return nil, err
		}
		if err := checkDuplicateHeadings(own, hs, doc.Span{}); err != nil {
			return nil, err
		}
		e.RegionStart, e.RegionEnd = region.End, region.End
		e.NewRegion = []byte("\n\n" + content)
		e.HeadingOffset = region.End + 2
		e.Heading = own[0].Text
		e.SectionText = content
	default:
		return nil, fmt.Errorf("unknown mode %q", mode)
	}

	next := make([]byte, 0, len(src)-(e.RegionEnd-e.RegionStart)+len(e.NewRegion))
	next = append(next, src[:e.RegionStart]...)
	next = append(next, e.NewRegion...)
	next = append(next, src[e.RegionEnd:]...)
	e.New = next
	return e, nil
}

// checkLeadHeading requires content to open with an ATX heading whose level
// is within [minLevel, maxLevel], and no later heading at or above that
// level (which would end the section early and swallow what follows).
func checkLeadHeading(own []doc.Heading, minLevel, maxLevel int, what string) error {
	if len(own) == 0 || own[0].Line != 1 {
		return fmt.Errorf("%w: %s must start with a heading line", ErrRefused, what)
	}
	lead := own[0]
	if lead.Level < minLevel || lead.Level > maxLevel {
		if minLevel == maxLevel {
			return fmt.Errorf("%w: %s heading must be level %d, got level %d", ErrRefused, what, minLevel, lead.Level)
		}
		return fmt.Errorf("%w: %s heading must be level %d or deeper, got level %d (a shallower heading would swallow the sections after it)", ErrRefused, what, minLevel, lead.Level)
	}
	for _, ch := range own[1:] {
		if ch.Level <= lead.Level {
			return fmt.Errorf("%w: %s has heading %q (level %d) at or above its own level %d", ErrRefused, what, ch.Text, ch.Level, lead.Level)
		}
	}
	return nil
}

// duplicateOutside finds a heading with text outside skip (an empty skip
// excludes nothing).
func duplicateOutside(hs []doc.Heading, text string, skip doc.Span) (doc.Heading, bool) {
	for _, h := range hs {
		if h.Span.Start >= skip.Start && h.Span.Start < skip.End {
			continue
		}
		if h.Text == text {
			return h, true
		}
	}
	return doc.Heading{}, false
}

// checkDuplicateHeadings refuses when any heading in own (not just the lead
// heading) duplicates the text of a heading elsewhere on the page (skip
// excludes the region being replaced, so replacing a section with itself
// isn't a duplicate) or of another heading within own itself — a duplicate
// anywhere in the result is just as unrecoverable as a duplicate lead
// heading.
func checkDuplicateHeadings(own []doc.Heading, hs []doc.Heading, skip doc.Span) error {
	seen := map[string]doc.Heading{}
	for _, ch := range own {
		if prior, ok := seen[ch.Text]; ok {
			return fmt.Errorf("%w: heading %q appears more than once in the new content (lines %d and %d)", ErrRefused, ch.Text, prior.Line, ch.Line)
		}
		seen[ch.Text] = ch
		if dup, ok := duplicateOutside(hs, ch.Text, skip); ok {
			return fmt.Errorf("%w: heading %q already exists on line %d", ErrRefused, ch.Text, dup.Line)
		}
	}
	return nil
}

// listItemRe matches a list item line: optional indent, then a "-"/"*"/"+"
// or numbered ("1." / "1)") marker, then a space.
var listItemRe = regexp.MustCompile(`^\s*([-*+]|[0-9]+[.)])\s`)

// appendSeparator is the join between an appended section's existing region
// and the new content: a single "\n" when both the region's last non-blank
// line and the content's first line are list items (so the append stays a
// tight list), else the usual blank-line "\n\n".
func appendSeparator(region, content string) string {
	if isListItem(lastNonBlankLine(region)) && isListItem(firstLine(content)) {
		return "\n"
	}
	return "\n\n"
}

func isListItem(line string) bool {
	return listItemRe.MatchString(line)
}

func lastNonBlankLine(s string) string {
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			return lines[i]
		}
	}
	return ""
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// contentHeadings scans content as a page body: never as frontmatter, even
// when it opens with "---", so a heading can't hide behind it.
func contentHeadings(content string) []doc.Heading {
	cd := doc.Parse("", []byte(content))
	cd.Frontmatter, cd.HasFM, cd.FMUnclosed = doc.Span{}, false, false
	cd.Body = doc.Span{Start: 0, End: len(cd.Src)}
	return doc.Headings(cd)
}
