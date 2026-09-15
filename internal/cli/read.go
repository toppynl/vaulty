package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/toppynl/vaulty/internal/diag"
	"github.com/toppynl/vaulty/internal/doc"
	"github.com/toppynl/vaulty/internal/timeline"
)

func (a *app) runTimelineRead(o readOpts, pageArg string) error {
	v, err := a.openVault()
	if err != nil {
		return err
	}
	rel, err := v.Resolve(pageArg)
	if err != nil {
		return &ExitError{Code: ExitUsage, Err: err}
	}
	full := filepath.Join(v.Root, filepath.FromSlash(rel))
	src, err := os.ReadFile(full)
	if err != nil {
		return &ExitError{Code: ExitIO, Err: err}
	}
	d := doc.Parse(rel, src)
	page := timeline.Parse(d, v.Config.Timeline)

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
	timelineMode := o.timeline || o.since != "" || o.last != 0

	entries := collectEntries(page)
	entries = filterSince(entries, since)
	entries = lastN(entries, o.last)

	if a.flags.json {
		out := readJSON{Path: rel, Diags: page.AllDiags()}
		if o.frontmatter {
			out.Frontmatter = string(d.Src[d.Frontmatter.Start:d.Frontmatter.End])
		}
		if !timelineMode {
			out.CompiledTruth = string(d.Src[page.CompiledTruth.Start:page.CompiledTruth.End])
		} else {
			for _, e := range entries {
				out.Entries = append(out.Entries, readEntryJSON{Line: e.Line, Date: e.Date, Text: strings.Join(nonBlankReadLines(e.Lines), "\n")})
			}
			if out.Entries == nil {
				out.Entries = []readEntryJSON{}
			}
		}
		return a.writeJSON(out)
	}

	if o.frontmatter && d.HasFM {
		a.stdout.Write(d.Src[d.Frontmatter.Start:d.Frontmatter.End])
	}

	if timelineMode {
		for _, e := range entries {
			for _, l := range nonBlankReadLines(e.Lines) {
				fmt.Fprintln(a.stdout, l)
			}
		}
		return nil
	}

	text := trimBlankEdges(string(d.Src[page.CompiledTruth.Start:page.CompiledTruth.End]))
	fmt.Fprintln(a.stdout, text)
	return nil
}

type readJSON struct {
	Path          string          `json:"path"`
	Frontmatter   string          `json:"frontmatter,omitempty"`
	CompiledTruth string          `json:"compiled_truth,omitempty"`
	Entries       []readEntryJSON `json:"entries,omitempty"`
	Diags         []diag.Diag     `json:"diags"`
}

type readEntryJSON struct {
	Line int           `json:"line"`
	Date timeline.Date `json:"date"`
	Text string        `json:"text"`
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
