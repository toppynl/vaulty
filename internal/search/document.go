package search

import (
	"path"
	"sort"
	"strings"

	"github.com/toppynl/vaulty/internal/config"
	"github.com/toppynl/vaulty/internal/doc"
	"github.com/toppynl/vaulty/internal/page"
	"github.com/toppynl/vaulty/internal/timeline"
)

// parsedPage is everything search needs from one page, for indexing and for
// snippet extraction.
type parsedPage struct {
	rel     string
	doc     *doc.Doc
	fm      page.Frontmatter
	h1      string
	summary string
	// 1-based inclusive line ranges; last < first means empty.
	truthFirst, truthLast       int
	timelineFirst, timelineLast int
	truthText, timelineText     string
}

// parsePage splits a page into its metadata, compiled truth (DESIGN.md
// §5.2's CompiledTruth span: after frontmatter, above the Timeline divider)
// and the Timeline remainder (from the divider/heading to EOF).
func parsePage(rel string, src []byte, summary string, tl config.Timeline, fields config.Fields) *parsedPage {
	d := doc.Parse(rel, src)
	p := &parsedPage{rel: rel, doc: d, fm: page.ParseFrontmatter(d, fields), h1: page.FirstH1(d), summary: summary}
	pg := timeline.Parse(d, tl)
	ct := pg.CompiledTruth
	p.truthText = string(src[ct.Start:ct.End])
	p.truthFirst, p.truthLast = spanLines(d, ct.Start, ct.End)
	p.timelineText = string(src[ct.End:])
	p.timelineFirst, p.timelineLast = spanLines(d, ct.End, len(src))
	return p
}

func spanLines(d *doc.Doc, start, end int) (int, int) {
	if end <= start {
		return 1, 0
	}
	return d.LineOf(start), d.LineOf(end - 1)
}

func (p *parsedPage) slug() string {
	return strings.TrimSuffix(path.Base(p.rel), ".md")
}

// bleveDoc is the indexed document (DESIGN.md §19.2). Each of the seven
// content groups is its own field so it can carry its own configurable
// boost (config.Search.Boosts) — unlike find's config.FindField list, which
// fields exist here is fixed by the code, not the vault's config.
func (p *parsedPage) bleveDoc() map[string]any {
	// Every frontmatter scalar/list value as one exact "key=value" term.
	var kv []string
	for k, vals := range p.fm.Values {
		for _, v := range vals {
			kv = append(kv, k+"="+v)
		}
	}
	sort.Strings(kv)

	d := map[string]any{
		groupSlug:          p.slug(),
		groupTitle:         p.fm.Title,
		groupAliases:       p.fm.Aliases,
		groupH1:            p.h1,
		groupIndex:         p.summary,
		groupTags:          p.fm.Tags,
		groupBody:          p.truthText,
		groupTimeline:      p.timelineText,
		fieldTitleStored:   p.fm.Title,
		fieldSummaryStored: p.summary,
		fieldFM:            kv,
	}
	if p.fm.Type != "" {
		d[fieldType] = p.fm.Type
	}
	return d
}
