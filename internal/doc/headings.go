package doc

import "strings"

// Heading is one ATX heading ("#" .. "######") found in a Doc's body.
// Span covers the heading line itself through the byte before the next
// heading of level <= Level, or end of file — i.e. the whole section the
// heading introduces, nested sub-headings included. Lines inside fenced
// code blocks (``` or ~~~) are never read as headings.
type Heading struct {
	Line  int    `json:"line"`  // 1-based
	Level int    `json:"level"` // 1..6, number of leading '#'
	Text  string `json:"text"`  // heading text, leading '#'s and whitespace stripped
	Span  Span   `json:"-"`
}

// Lines is the number of lines the heading's section spans.
func (h Heading) Lines(d *Doc) int {
	if h.Span.End <= h.Span.Start {
		return 0
	}
	return d.LineOf(h.Span.End-1) - h.Line + 1
}

// Bytes is the byte length of the heading's section.
func (h Heading) Bytes() int { return h.Span.Len() }

// Headings scans d's body (frontmatter excluded) for ATX headings, in file
// order, skipping anything inside a fenced code block. A heading's section
// ends at the first of: a later heading of level <= its own, a standalone
// "---" divider line (the vault's Timeline divider, §1 — a hard content
// boundary that closes every currently-open heading, not just same-level
// ones), or EOF — never inside a fenced code block either way.
func Headings(d *Doc) []Heading {
	var out []Heading
	var dividers []int // byte offsets of standalone "---" lines
	inFence := false
	var fenceMarker string
	startLine := 1
	if d.Body.Start > 0 {
		startLine = d.LineOf(d.Body.Start)
	}
	for n := startLine; n <= d.NumLines(); n++ {
		line := d.Line(n)
		trimmed := strings.TrimLeft(line, " \t")
		if marker, ok := fenceOf(trimmed); ok {
			switch {
			case !inFence:
				inFence, fenceMarker = true, marker
			case strings.HasPrefix(trimmed, fenceMarker):
				inFence = false
			}
			continue
		}
		if inFence {
			continue
		}
		if isDividerLine(line) {
			dividers = append(dividers, d.LineStarts[n-1])
			continue
		}
		level, text, ok := parseATX(line)
		if !ok {
			continue
		}
		out = append(out, Heading{Line: n, Level: level, Text: text, Span: Span{Start: d.LineStarts[n-1]}})
	}
	for i := range out {
		end := len(d.Src)
		for j := i + 1; j < len(out); j++ {
			if out[j].Level <= out[i].Level {
				end = out[j].Span.Start
				break
			}
		}
		for _, dPos := range dividers {
			if dPos > out[i].Span.Start && dPos < end {
				end = dPos
				break // dividers is in file order, so the first hit is nearest
			}
		}
		out[i].Span.End = end
	}
	return out
}

// isDividerLine reports whether line (with only trailing \r trimmed, as
// Doc.Line already does) is a standalone "---" divider: optional leading
// spaces/tabs, then exactly "---", then nothing but trailing whitespace.
// Matches doc.Parse's frontmatter delimiter check, and deliberately not a
// broader thematic-break rule (CommonMark also allows "***"/"___", and 3+
// repeats) — this exists only to stop leaking the vault's own Timeline
// divider into the section above it, not to parse markdown in general.
func isDividerLine(line string) bool {
	return strings.TrimRight(strings.TrimLeft(line, " \t"), " \t") == "---"
}

// fenceOf reports whether trimmed opens/closes a fenced code block, and the
// 3-rune marker ("```" or "~~~") if so.
func fenceOf(trimmed string) (string, bool) {
	if strings.HasPrefix(trimmed, "```") {
		return "```", true
	}
	if strings.HasPrefix(trimmed, "~~~") {
		return "~~~", true
	}
	return "", false
}

// parseATX parses one ATX heading line: 1-6 '#', then a space (or EOL),
// then the (trimmed) text. Up to 3 leading spaces are allowed per
// CommonMark; more than 3 removes it from consideration (indented code).
func parseATX(line string) (level int, text string, ok bool) {
	rest := line
	indent := 0
	for indent < len(rest) && rest[indent] == ' ' {
		indent++
	}
	if indent > 3 {
		return 0, "", false
	}
	rest = rest[indent:]
	level = 0
	for level < len(rest) && rest[level] == '#' {
		level++
	}
	if level == 0 || level > 6 {
		return 0, "", false
	}
	rest = rest[level:]
	if rest == "" {
		return level, "", true
	}
	if rest[0] != ' ' && rest[0] != '\t' {
		return 0, "", false
	}
	text = strings.TrimSpace(rest)
	// Strip an optional closing sequence of '#'s ("## Heading ##"), only
	// when preceded by whitespace so a heading genuinely ending in '#'
	// ("## C#") is left alone.
	if trimmed := strings.TrimRight(text, "#"); trimmed != text {
		if trimmed == "" || trimmed[len(trimmed)-1] == ' ' || trimmed[len(trimmed)-1] == '\t' {
			text = strings.TrimSpace(trimmed)
		}
	}
	return level, text, true
}
