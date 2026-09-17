package cli

import (
	"fmt"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/toppynl/vaulty/internal/diag"
	"github.com/toppynl/vaulty/internal/doc"
	"github.com/toppynl/vaulty/internal/name"
	"github.com/toppynl/vaulty/internal/section"
	"github.com/toppynl/vaulty/internal/timeline"
)

type readOpts struct {
	timeline    bool
	since       string
	last        int
	frontmatter bool
	headings    bool
	section     string
	maxBytes    int
	maxBytesSet bool
}

func (a *app) newReadCmd() *cobra.Command {
	var o readOpts
	cmd := &cobra.Command{
		Use:   "read <page>",
		Short: "Print compiled truth (default), Timeline entries, headings or one section of a page",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			o.maxBytesSet = cmd.Flags().Changed("max-bytes")
			return a.runRead(o, args[0])
		},
	}
	cmd.Flags().BoolVar(&o.timeline, "timeline", false, "print Timeline entries instead of compiled truth")
	cmd.Flags().StringVar(&o.since, "since", "", "only entries overlapping YYYY-MM-DD or later (implies --timeline)")
	cmd.Flags().IntVar(&o.last, "last", 0, "only the last N entries (implies --timeline)")
	cmd.Flags().BoolVar(&o.frontmatter, "frontmatter", false, "also print the frontmatter block first")
	cmd.Flags().BoolVar(&o.headings, "headings", false, "list section headings with line number, line count and byte count instead of printing content")
	cmd.Flags().StringVar(&o.section, "section", "", "print only this section (a heading's text, from the heading to the next heading of equal-or-higher level, the Timeline divider or EOF); its hash goes to stderr (for write --if-hash)")
	cmd.Flags().IntVar(&o.maxBytes, "max-bytes", 0, "truncate the printed content to N bytes, with a marker noting how much was cut")
	return cmd
}

func (a *app) runRead(o readOpts, pageArg string) error {
	v, err := a.openVault()
	if err != nil {
		return err
	}
	rel, err := v.Resolve(pageArg)
	if err != nil {
		return &ExitError{Code: ExitUsage, Err: err}
	}
	full, err := v.ContentFile(rel)
	if err != nil {
		return &ExitError{Code: ExitUsage, Err: err}
	}
	src, err := os.ReadFile(full)
	if err != nil {
		return &ExitError{Code: ExitIO, Err: err}
	}
	d := doc.Parse(rel, src)
	page := timeline.Parse(d, v.Config.Timeline)

	if o.headings && o.section != "" {
		return &ExitError{Code: ExitUsage, Err: fmt.Errorf("--headings and --section are mutually exclusive")}
	}
	if o.headings && o.frontmatter {
		return &ExitError{Code: ExitUsage, Err: fmt.Errorf("--headings does not combine with --frontmatter")}
	}
	if o.headings && o.maxBytesSet {
		return &ExitError{Code: ExitUsage, Err: fmt.Errorf("--headings does not combine with --max-bytes")}
	}
	if o.section != "" && (o.timeline || o.since != "" || o.last != 0) {
		return &ExitError{Code: ExitUsage, Err: fmt.Errorf("--section does not combine with --timeline/--since/--last")}
	}
	if o.maxBytesSet && o.maxBytes <= 0 {
		return &ExitError{Code: ExitUsage, Err: fmt.Errorf("--max-bytes must be > 0")}
	}
	if o.last < 0 {
		return &ExitError{Code: ExitUsage, Err: fmt.Errorf("--last must be >= 0")}
	}
	var since string
	if o.since != "" {
		since, err = normalizeSince(o.since)
		if err != nil {
			return &ExitError{Code: ExitUsage, Err: err}
		}
	}

	if o.headings {
		return a.renderHeadings(rel, d, page)
	}

	var sec *doc.Heading
	if o.section != "" {
		hs := doc.Headings(d)
		h, ok := findSection(hs, o.section)
		if !ok {
			return &ExitError{Code: ExitUsage, Err: sectionNotFoundError(hs, o.section)}
		}
		sec = &h
	}

	timelineMode := o.timeline || o.since != "" || o.last != 0
	entries := collectEntries(page)
	entries = filterSince(entries, since)
	entries = lastN(entries, o.last)

	if a.flags.json {
		out := readJSON{Path: rel, Diags: page.AllDiags()}
		if o.frontmatter {
			out.Frontmatter = string(d.Src[d.Frontmatter.Start:d.Frontmatter.End])
		}
		switch {
		case sec != nil:
			full := sectionText(page, *sec)
			text, trunc := maybeTruncate(full, o)
			out.Section = &readSectionJSON{Line: sec.Line, Heading: sec.Text, Text: text, Hash: section.Hash(full)}
			out.Truncated = trunc
		case timelineMode:
			for _, e := range entries {
				out.Entries = append(out.Entries, readEntryJSON{Line: e.Line, Date: e.Date, Text: strings.Join(nonBlankReadLines(e.Lines), "\n")})
			}
			if out.Entries == nil {
				out.Entries = []readEntryJSON{}
			}
			// --max-bytes is not applied to --json --timeline output: the
			// entries are already structured and bounded by --since/--last
			// (DESIGN.md §7.2); truncating mid-entry would corrupt that
			// structure for no token-budget benefit a JSON consumer can't
			// already get by slicing the array itself.
		default:
			text, trunc := maybeTruncate(string(d.Src[page.CompiledTruth.Start:page.CompiledTruth.End]), o)
			out.CompiledTruth = text
			out.Truncated = trunc
		}
		return a.writeJSON(out)
	}

	if o.frontmatter && d.HasFM {
		a.stdout.Write(d.Src[d.Frontmatter.Start:d.Frontmatter.End])
	}

	if timelineMode && sec == nil && len(entries) == 0 {
		// DESIGN.md §7: a page (or filtered range) with no Timeline entries
		// prints nothing, unlike the default/section modes which always
		// print at least a blank line.
		return nil
	}

	var content string
	switch {
	case sec != nil:
		content = sectionText(page, *sec)
	case timelineMode:
		var b strings.Builder
		for _, e := range entries {
			for _, l := range nonBlankReadLines(e.Lines) {
				b.WriteString(l)
				b.WriteByte('\n')
			}
		}
		content = strings.TrimSuffix(b.String(), "\n")
	default:
		content = trimBlankEdges(string(d.Src[page.CompiledTruth.Start:page.CompiledTruth.End]))
	}

	kept, trunc := maybeTruncate(content, o)
	fmt.Fprintln(a.stdout, kept)
	if trunc != nil {
		fmt.Fprintf(a.stdout, "[... %d bytes / %d lines truncated ...]\n", trunc.Bytes, trunc.Lines)
	}
	if sec != nil {
		// On stderr so stdout stays exactly the section text; the hash is
		// over the untruncated text, for `vaulty write --if-hash`.
		fmt.Fprintf(a.stderr, "%s: section hash %s\n", name.Binary, section.Hash(content))
	}
	return nil
}

// renderHeadings implements `read --headings`: independent of the
// default/timeline/section modes, always exits 0 once the page resolved.
func (a *app) renderHeadings(rel string, d *doc.Doc, page *timeline.Page) error {
	hs := doc.Headings(d)
	if a.flags.json {
		out := headingsJSON{Path: rel, Diags: page.AllDiags()}
		for _, h := range hs {
			span, _ := section.Region(d, page, h)
			out.Headings = append(out.Headings, headingJSON{Line: h.Line, Level: h.Level, Text: h.Text, Lines: h.Lines(d), Bytes: h.Bytes(), Hash: section.Hash(section.Text(d, span))})
		}
		if out.Headings == nil {
			out.Headings = []headingJSON{}
		}
		return a.writeJSON(out)
	}
	for _, h := range hs {
		fmt.Fprintf(a.stdout, "%d\t%s %s\t%d\t%d\n", h.Line, strings.Repeat("#", h.Level), h.Text, h.Lines(d), h.Bytes())
	}
	return nil
}

// findSection matches query against headings by text, ignoring any leading
// '#'s/whitespace the caller included. First match wins in file order.
func findSection(hs []doc.Heading, query string) (doc.Heading, bool) {
	want := normalizeHeadingQuery(query)
	for _, h := range hs {
		if h.Text == want {
			return h, true
		}
	}
	return doc.Heading{}, false
}

func normalizeHeadingQuery(s string) string {
	return strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(s), "#"))
}

// sectionNotFoundError builds the --section "no such section" error,
// suggesting the closest heading(s) instead of leaving the caller to guess
// why an otherwise-plausible query didn't match: an exact case-insensitive
// match first (the query differs only in case), else any heading whose
// text contains the query or vice versa (case-insensitive), in file order,
// capped at 5 so a page with many loosely-matching headings doesn't spam
// the error.
func sectionNotFoundError(hs []doc.Heading, query string) error {
	want := normalizeHeadingQuery(query)
	wantFold := strings.ToLower(want)

	var exact []string
	for _, h := range hs {
		if strings.EqualFold(h.Text, want) {
			exact = append(exact, h.Text)
		}
	}
	if len(exact) > 0 {
		return fmt.Errorf("no such section: %q (case-sensitive; did you mean %s?)", query, quoteJoin(exact))
	}

	var closeMatches []string
	for _, h := range hs {
		fold := strings.ToLower(h.Text)
		if strings.Contains(fold, wantFold) || strings.Contains(wantFold, fold) {
			closeMatches = append(closeMatches, h.Text)
			if len(closeMatches) == 5 {
				break
			}
		}
	}
	if len(closeMatches) > 0 {
		return fmt.Errorf("no such section: %q (closest matches: %s)", query, quoteJoin(closeMatches))
	}
	return fmt.Errorf("no such section: %q", query)
}

func quoteJoin(ss []string) string {
	quoted := make([]string, len(ss))
	for i, s := range ss {
		quoted[i] = fmt.Sprintf("%q", s)
	}
	return strings.Join(quoted, ", ")
}

// sectionText is the section's region text (section.Region: clamped to
// compiled truth, blank edge lines trimmed) — what `write` hashes too.
func sectionText(p *timeline.Page, h doc.Heading) string {
	span, _ := section.Region(p.Doc, p, h)
	return section.Text(p.Doc, span)
}

type truncatedJSON struct {
	Bytes int `json:"bytes"`
	Lines int `json:"lines"`
}

// maybeTruncate applies o.maxBytes to content when set (DESIGN.md §7.3),
// reporting nil when no truncation happened (either unset, or content
// already fit).
func maybeTruncate(content string, o readOpts) (string, *truncatedJSON) {
	if !o.maxBytesSet {
		return content, nil
	}
	kept, omittedBytes, omittedLines := truncateBytes(content, o.maxBytes)
	if omittedBytes == 0 {
		return kept, nil
	}
	return kept, &truncatedJSON{Bytes: omittedBytes, Lines: omittedLines}
}

// truncateBytes cuts s to at most max bytes, backing off to the nearest
// rune boundary so a multi-byte UTF-8 rune is never split.
func truncateBytes(s string, max int) (kept string, omittedBytes, omittedLines int) {
	if max <= 0 || len(s) <= max {
		return s, 0, 0
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	kept = s[:cut]
	omitted := s[cut:]
	omittedBytes = len(omitted)
	omittedLines = strings.Count(omitted, "\n")
	if omittedBytes > 0 && !strings.HasSuffix(omitted, "\n") {
		omittedLines++
	}
	return kept, omittedBytes, omittedLines
}

type readJSON struct {
	Path          string           `json:"path"`
	Frontmatter   string           `json:"frontmatter,omitempty"`
	CompiledTruth string           `json:"compiled_truth,omitempty"`
	Entries       []readEntryJSON  `json:"entries,omitempty"`
	Section       *readSectionJSON `json:"section,omitempty"`
	Truncated     *truncatedJSON   `json:"truncated,omitempty"`
	Diags         []diag.Diag      `json:"diags"`
}

type readEntryJSON struct {
	Line int           `json:"line"`
	Date timeline.Date `json:"date"`
	Text string        `json:"text"`
}

type readSectionJSON struct {
	Line    int    `json:"line"`
	Heading string `json:"heading"`
	Text    string `json:"text"`
	Hash    string `json:"hash"`
}

type headingsJSON struct {
	Path     string        `json:"path"`
	Headings []headingJSON `json:"headings"`
	Diags    []diag.Diag   `json:"diags"`
}

type headingJSON struct {
	Line  int    `json:"line"`
	Level int    `json:"level"`
	Text  string `json:"text"`
	Lines int    `json:"lines"`
	Bytes int    `json:"bytes"`
	Hash  string `json:"hash"`
}

func collectEntries(p *timeline.Page) []timeline.Entry {
	var out []timeline.Entry
	for _, b := range p.Blocks {
		out = append(out, b.Entries...)
	}
	return out
}

func filterSince(entries []timeline.Entry, since string) []timeline.Entry {
	if since == "" {
		return entries
	}
	var out []timeline.Entry
	for _, e := range entries {
		if e.Date.OnOrAfter(since) {
			out = append(out, e)
		}
	}
	return out
}

func lastN(entries []timeline.Entry, n int) []timeline.Entry {
	if n <= 0 || len(entries) <= n {
		return entries
	}
	return entries[len(entries)-n:]
}

func nonBlankReadLines(lines []string) []string {
	var out []string
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

func normalizeSince(s string) (string, error) {
	if len(s) == len("2006-01-02") {
		if _, err := time.Parse("2006-01-02", s); err != nil {
			return "", fmt.Errorf("--since: invalid date %q", s)
		}
		return s, nil
	}
	if len(s) == len("2006-01") {
		if _, err := time.Parse("2006-01", s); err != nil {
			return "", fmt.Errorf("--since: invalid month %q", s)
		}
		return s + "-01", nil
	}
	return "", fmt.Errorf("--since: invalid date %q (want YYYY-MM-DD or YYYY-MM)", s)
}

func trimBlankEdges(s string) string {
	lines := strings.Split(s, "\n")
	start := 0
	for start < len(lines) && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	end := len(lines)
	for end > start && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return strings.Join(lines[start:end], "\n")
}
