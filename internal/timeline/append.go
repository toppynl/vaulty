package timeline

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/toppynl/vaulty/internal/config"
	"github.com/toppynl/vaulty/internal/diag"
	"github.com/toppynl/vaulty/internal/doc"
)

// Position reports where Append put the entry.
type Position string

const (
	PosEnd        Position = "end"
	PosMiddle     Position = "middle"
	PosStart      Position = "start"
	PosNewSection Position = "new-section"
)

type AppendOptions struct {
	Touch bool   // set frontmatter updated: to Today
	Today string // YYYY-MM-DD; caller resolves $VAULT_TODAY / local date
}

type AppendResult struct {
	Line           int      `json:"line"` // 1-based line of the new entry in New
	Position       Position `json:"position"`
	AlreadyPresent bool     `json:"already_present"`
	CreatedSection bool     `json:"created_section"`
	Touched        bool     `json:"touched"` // updated: actually changed
	FutureDate     bool     `json:"-"`       // caller prints a stderr warning
	New            []byte   `json:"-"`

	// Inputs for the caller's mandatory safety.Verify(orig, New, ...) call
	// before writing (DESIGN.md §8.6). timeline never imports safety —
	// safety imports timeline (to reparse) — so the caller (cli) builds the
	// safety.Expect from these.
	RegionStart, RegionEnd, NewRegionEnd int
	Added                                []string
	AllowUpdatedLine                     bool

	// AddedFirstLine is the new entry's own first line, exactly as written
	// (before any --touch edit, which never touches the body). The caller
	// feeds it to safety.Expect.NewFirstLineText alongside Line, for safety
	// check 6 (DESIGN.md §8.6).
	AddedFirstLine string `json:"-"`
}

// ErrRefused wraps every reason Append declines to write (exit 3).
var ErrRefused = errors.New("refused")

var reAppendFirstLine = regexp.MustCompile(`^- \*\*(\d{4}-\d{2}-\d{2})\*\* \| `)

// ValidateEntry normalizes and validates user input (DESIGN.md §8.1).
// Returns the entry's lines as they will be written.
func ValidateEntry(input string) (lines []string, date Date, err error) {
	trimmed := strings.TrimRight(input, " \t\r\n")
	if strings.HasPrefix(trimmed, "**") {
		trimmed = "- " + trimmed
	}
	if trimmed == "" {
		return nil, Date{}, fmt.Errorf("%w: empty entry", ErrRefused)
	}
	raw := strings.Split(trimmed, "\n")

	m := reAppendFirstLine.FindStringSubmatch(raw[0])
	if m == nil {
		return nil, Date{}, fmt.Errorf("%w: append needs a full date", ErrRefused)
	}
	if _, err := time.Parse("2006-01-02", m[1]); err != nil {
		return nil, Date{}, fmt.Errorf("%w: append needs a full date", ErrRefused)
	}
	d, ok := ParseDate(m[1])
	if !ok || d.Precision != PrecDay {
		return nil, Date{}, fmt.Errorf("%w: append needs a full date", ErrRefused)
	}

	for i, l := range raw[1:] {
		lineNo := i + 2
		if strings.TrimSpace(l) == "" {
			return nil, Date{}, fmt.Errorf("%w: line %d: continuation lines must not be blank", ErrRefused, lineNo)
		}
		if !strings.HasPrefix(l, " ") && !strings.HasPrefix(l, "\t") {
			return nil, Date{}, fmt.Errorf("%w: line %d: continuation lines must be indented", ErrRefused, lineNo)
		}
		if reEntryStart.MatchString(l) {
			return nil, Date{}, fmt.Errorf("%w: line %d: looks like a new entry, not a continuation", ErrRefused, lineNo)
		}
		if reForbiddenDivider.MatchString(l) || reForbiddenSub.MatchString(l) || reForbiddenFence.MatchString(l) {
			return nil, Date{}, fmt.Errorf("%w: line %d: forbidden line inside an entry", ErrRefused, lineNo)
		}
	}

	joined := joinRawLines(raw)
	if !entryShapeOK(joined) {
		return nil, Date{}, fmt.Errorf("%w: entry must read '- **YYYY-MM-DD** | source — what'", ErrRefused)
	}

	return raw, d, nil
}

func joinRawLines(lines []string) string {
	parts := []string{lines[0]}
	for _, l := range lines[1:] {
		parts = append(parts, strings.TrimSpace(l))
	}
	return strings.Join(parts, " ")
}

// Append computes the new file content; it does not write (DESIGN.md §8.2–8.6).
// The caller runs safety.Verify on (p.Doc.Src, res.New) before writing.
func Append(p *Page, entry string, opt AppendOptions, cfg *config.Config) (*AppendResult, error) {
	lines, date, err := ValidateEntry(entry)
	if err != nil {
		return nil, err
	}

	block, err := checkAppendPreconditions(p, opt)
	if err != nil {
		return nil, err
	}

	if block != nil {
		for _, e := range block.Entries {
			if linesEqualTrimmed(e.Lines, lines) {
				return &AppendResult{Line: e.Line, AlreadyPresent: true}, nil
			}
		}
	}

	var (
		body                                 []byte
		regionStart, regionEnd, newRegionEnd int
		position                             Position
		createdSection                       bool
		extraAdded                           []string
		idx                                  int // position of the new entry among the reparsed block's Entries
	)

	switch {
	case block == nil:
		body, extraAdded = buildNewSection(p.Doc, cfg.Timeline.Heading, cfg.Timeline.Divider, lines)
		position = PosNewSection
		createdSection = true
		regionStart, regionEnd = len(p.Doc.Src), len(p.Doc.Src)
		idx = 0
	case len(block.Entries) == 0:
		body = buildEmptyBlockReplace(p.Doc, block, lines)
		position = PosEnd
		regionStart, regionEnd = block.Body.Start, block.Body.End
		idx = 0
	default:
		var pos Position
		body, pos, idx = insertIntoBlockBytes(p.Doc, block, cfg.Timeline, Entry{Date: date, Lines: lines})
		position = pos
		regionStart, regionEnd = block.Body.Start, block.Body.End
	}
	// Suffix bytes (src[RegionEnd:]) are always carried through unchanged,
	// so the region's end in the new content is always derivable this way,
	// regardless of which case built `body`.
	newRegionEnd = len(body) - len(p.Doc.Src) + regionEnd

	touched := false
	final := body
	if opt.Touch {
		var err error
		final, touched, err = Touch(body, p.Doc, cfg.Frontmatter.UpdatedKey, opt.Today)
		if err != nil {
			return nil, fmt.Errorf("%w: touch: %v", ErrRefused, err)
		}
		if touched {
			delta := len(final) - len(body)
			newRegionEnd += delta
		}
	}

	res := &AppendResult{
		Position:         position,
		CreatedSection:   createdSection,
		Touched:          touched,
		New:              final,
		FutureDate:       opt.Today != "" && date.From > opt.Today,
		RegionStart:      regionStart,
		RegionEnd:        regionEnd,
		NewRegionEnd:     newRegionEnd,
		Added:            append(append([]string(nil), extraAdded...), lines...),
		AllowUpdatedLine: opt.Touch,
		AddedFirstLine:   lines[0],
	}

	// Line number: reparse the final content and locate the new entry by
	// its position index in the block, never by content or date-key
	// matching — a same-date insertion in the middle of the block would
	// otherwise match the wrong (pre-existing) entry with an equal key.
	line, err := locateNewEntryLine(final, cfg, idx)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRefused, err)
	}
	res.Line = line

	return res, nil
}

// checkAppendPreconditions implements DESIGN.md §8.2. Returns the sole
// Timeline block (nil if the page has none).
func checkAppendPreconditions(p *Page, opt AppendOptions) (*Block, error) {
	if len(p.Blocks) > 1 {
		return nil, fmt.Errorf("%w: more than one Timeline block", ErrRefused)
	}
	if opt.Touch && p.Doc.FMUnclosed {
		return nil, fmt.Errorf("%w: frontmatter opened but never closed", ErrRefused)
	}
	if len(p.Blocks) == 0 {
		return nil, nil
	}
	block := &p.Blocks[0]
	if block.DividerLine == 0 {
		return nil, fmt.Errorf("%w: Timeline heading without a divider directly above it", ErrRefused)
	}
	for _, d := range p.Diags {
		if d.Code == diag.TL003ContentAfter {
			return nil, fmt.Errorf("%w: content after the Timeline heading", ErrRefused)
		}
	}

	isEmptyOK := len(block.Entries) == 0 && isBlankPreamble(block.Preamble) && !hasCode(block.Diags, diag.TL004Forbidden)
	if !block.Sortable() && !isEmptyOK {
		return nil, fmt.Errorf("%w: block is not safely appendable", ErrRefused)
	}
	if len(block.Entries) > 0 {
		if ClassifyOrder(block.Entries) != Ascending {
			return nil, fmt.Errorf("%w: block is not ascending", ErrRefused)
		}
		round := SerializeBody(block.Entries, block.Gaps, block.LeadingBlanks, block.TrailingBlanks)
		if round != string(p.Doc.Src[block.Body.Start:block.Body.End]) {
			return nil, fmt.Errorf("%w: round-trip mismatch (parser bug)", ErrRefused)
		}
	}
	return block, nil
}

func isBlankPreamble(preamble []string) bool {
	for _, l := range preamble {
		if strings.TrimSpace(l) != "" {
			return false
		}
	}
	return true
}

func hasCode(diags []diag.Diag, code diag.Code) bool {
	for _, d := range diags {
		if d.Code == code {
			return true
		}
	}
	return false
}

func linesEqualTrimmed(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if strings.TrimRight(a[i], " \t\r") != strings.TrimRight(b[i], " \t\r") {
			return false
		}
	}
	return true
}

// buildNewSection implements DESIGN.md §8.4 "No block (new section)".
// addedNonBlank lists every brand-new non-blank line this insertion adds
// beyond the entry's own lines (the divider when inserted, and the
// heading) — needed by the caller to build a correct safety.Expect.Added.
func buildNewSection(d *doc.Doc, heading, divider string, lines []string) (out []byte, addedNonBlank []string) {
	s := append([]byte(nil), d.Src...)
	if len(s) > 0 && s[len(s)-1] != '\n' {
		s = append(s, '\n')
	}
	body := string(s[d.Frontmatter.End:])
	last := lastNonBlankLine(body)
	entryText := strings.Join(lines, "\n")

	if !endsWithBlankLineOrEmpty(s) {
		s = append(s, '\n')
	}
	if last == divider {
		s = append(s, []byte(heading+"\n\n"+entryText+"\n")...)
		addedNonBlank = []string{heading}
	} else {
		s = append(s, []byte(divider+"\n\n"+heading+"\n\n"+entryText+"\n")...)
		addedNonBlank = []string{divider, heading}
	}
	return s, addedNonBlank
}

func lastNonBlankLine(s string) string {
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		t := strings.TrimRight(lines[i], " \t\r")
		if t != "" {
			return t
		}
	}
	return ""
}

func endsWithBlankLineOrEmpty(s []byte) bool {
	return len(s) == 0 || (len(s) >= 2 && s[len(s)-1] == '\n' && s[len(s)-2] == '\n')
}

// buildEmptyBlockReplace implements DESIGN.md §8.4 "Empty block".
func buildEmptyBlockReplace(d *doc.Doc, block *Block, lines []string) []byte {
	prefix := "\n"
	if block.Heading.End == len(d.Src) && (len(d.Src) == 0 || d.Src[len(d.Src)-1] != '\n') {
		// The heading had no trailing newline at all (EOF, no '\n'):
		// supply the one that would normally separate it from the body.
		prefix = "\n\n"
	}
	newBody := prefix + strings.Join(lines, "\n") + "\n"
	out := append(append([]byte(nil), d.Src[:block.Body.Start]...), []byte(newBody)...)
	out = append(out, d.Src[block.Body.End:]...)
	return out
}

// gapStyle resolves entry_gap: "0"/"1" literal, or "auto" — the most
// frequent value in gaps, ties won by the value occurring last.
func gapStyle(cfgGap string, gaps []int) int {
	switch cfgGap {
	case "0":
		return 0
	case "1":
		return 1
	}
	if len(gaps) == 0 {
		return 0
	}
	freq := map[int]int{}
	lastIdx := map[int]int{}
	for i, g := range gaps {
		freq[g]++
		lastIdx[g] = i
	}
	best := gaps[0]
	for v, f := range freq {
		if f > freq[best] || (f == freq[best] && lastIdx[v] > lastIdx[best]) {
			best = v
		}
	}
	return best
}

// insertIntoBlockBytes implements DESIGN.md §8.4 "Block with entries". The
// returned idx is the new entry's 0-based position among the resulting
// entries (== j).
func insertIntoBlockBytes(d *doc.Doc, block *Block, cfg config.Timeline, newEntry Entry) ([]byte, Position, int) {
	entries := block.Entries
	gaps := block.Gaps
	n := len(entries)
	j := n
	for i, e := range entries {
		if e.Date.Key > newEntry.Date.Key {
			j = i
			break
		}
	}

	style := gapStyle(cfg.EntryGap, gaps)
	var newGaps []int
	switch {
	case j == n:
		newGaps = append(append([]int(nil), gaps...), style)
	case j == 0:
		newGaps = append([]int{style}, append([]int(nil), gaps...)...)
	default:
		g := gaps[j-1]
		newGaps = append(append([]int(nil), gaps[:j-1]...), g, g)
		newGaps = append(newGaps, gaps[j:]...)
	}

	trailing := block.TrailingBlanks
	if block.NextHeadingLine == 0 && trailing == 0 {
		trailing = 1
	}

	newEntries := append(append([]Entry(nil), entries[:j]...), newEntry)
	newEntries = append(newEntries, entries[j:]...)

	newBody := SerializeBody(newEntries, newGaps, block.LeadingBlanks, trailing)
	out := append(append([]byte(nil), d.Src[:block.Body.Start]...), []byte(newBody)...)
	out = append(out, d.Src[block.Body.End:]...)

	var pos Position
	switch {
	case j == 0:
		pos = PosStart
	case j == n:
		pos = PosEnd
	default:
		pos = PosMiddle
	}
	return out, pos, j
}

var reUpdatedLine = regexp.MustCompile(`^([ \t]*)(["']?)(\d{4}-\d{2}-\d{2})?(["']?)(.*)$`)

// Touch implements DESIGN.md §8.5: set the frontmatter KEY line to today
// (inserting it before the closing '---' when missing). src is the file
// content (with frontmatter) after a body-only edit, d is the ORIGINAL doc
// (only its Frontmatter/HasFM are used — src's frontmatter bytes must be
// identical to the original's). Shared by `timeline append` and `write`;
// callers refuse an unclosed frontmatter before calling it.
func Touch(src []byte, d *doc.Doc, key, today string) ([]byte, bool, error) {
	if !d.HasFM {
		return src, false, nil
	}
	fmText := string(src[d.Frontmatter.Start:d.Frontmatter.End])
	lines := strings.Split(fmText, "\n")
	prefix := key + ":"

	for i, l := range lines {
		if !strings.HasPrefix(l, prefix) {
			continue
		}
		rest := l[len(prefix):]
		m := reUpdatedLine.FindStringSubmatch(rest)
		if m == nil {
			return src, false, nil
		}
		existing := m[3]
		if existing == today {
			return src, false, nil
		}
		lines[i] = prefix + m[1] + m[2] + today + m[4] + m[5]
		return spliceFrontmatter(src, d, strings.Join(lines, "\n")), true, nil
	}

	// Key missing: insert "KEY: today" directly before the closing '---'.
	insertAt := len(lines) - 1
	for insertAt > 0 && strings.TrimRight(lines[insertAt], " \t\r") != "---" {
		insertAt--
	}
	newLines := append(append([]string(nil), lines[:insertAt]...), key+": "+today)
	newLines = append(newLines, lines[insertAt:]...)
	return spliceFrontmatter(src, d, strings.Join(newLines, "\n")), true, nil
}

func spliceFrontmatter(src []byte, d *doc.Doc, newFM string) []byte {
	out := append(append([]byte(nil), src[:d.Frontmatter.Start]...), []byte(newFM)...)
	out = append(out, src[d.Frontmatter.End:]...)
	return out
}

// locateNewEntryLine reparses final and returns the 1-based line of the
// entry at idx (the new entry's position among the sole block's Entries,
// computed at insertion time — never by content or date-key matching,
// which breaks when the new entry shares its date with an existing one).
func locateNewEntryLine(final []byte, cfg *config.Config, idx int) (int, error) {
	nd := doc.Parse("", final)
	page := Parse(nd, cfg.Timeline)
	if len(page.Blocks) != 1 {
		return 0, fmt.Errorf("reparse: expected exactly one Timeline block, got %d", len(page.Blocks))
	}
	b := page.Blocks[0]
	if idx < 0 || idx >= len(b.Entries) {
		return 0, fmt.Errorf("reparse: entry index %d out of range (%d entries)", idx, len(b.Entries))
	}
	return b.Entries[idx].Line, nil
}
