// Package find implements `vaulty find`: term(s) in, ranked vault-relative
// page paths out (DESIGN.md §19). It replaces raw grep/find as the LLM
// reader agent's discovery step over the vault.
package find

import (
	"errors"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"github.com/toppynl/vaulty/internal/config"
	"github.com/toppynl/vaulty/internal/doc"
	"github.com/toppynl/vaulty/internal/timeline"
	"github.com/toppynl/vaulty/internal/vault"
)

// ErrNoTerms is returned when every given term is blank (exit 2, DESIGN.md §19.2).
var ErrNoTerms = errors.New("no search terms given")

// weights is the single named table of field weights (DESIGN.md §19.1).
// exact/sub apply to fields with an exact-vs-substring distinction (slug,
// title, alias); the rest (tag, index, h1, body) have one weight, applied
// on a substring (Contains) match only.
const (
	weightSlugExact  = 100
	weightSlugSub    = 50
	weightTitleExact = 40
	weightTitleSub   = 30
	weightAliasExact = 40
	weightAliasSub   = 30
	weightTag        = 25
	weightIndex      = 20
	weightH1         = 20
	weightBody       = 5
)

// Options are the `find` flags that affect scoring/scope, beyond the terms
// themselves.
type Options struct {
	Body bool   // scan compiled-truth body text as a fallback field
	All  bool   // ignore config.Find.Exclude
	Type string // filter on frontmatter `type` (exact match); "" = no filter
}

// BodyMatch is one compiled-truth line a body-fallback term matched.
type BodyMatch struct {
	Line int    `json:"line"`
	Text string `json:"text"`
}

// Result is one matching page.
type Result struct {
	Path    string      `json:"path"`
	Type    string      `json:"type"`
	Title   string      `json:"title"`
	Summary string      `json:"summary"`
	Score   int         `json:"score"`
	Matched []string    `json:"matched"`
	Body    []BodyMatch `json:"body,omitempty"`
}

// SummaryOrTitle is the single "what is this page" string human output
// prints: the index summary when present, else the frontmatter title.
func (r Result) SummaryOrTitle() string {
	if r.Summary != "" {
		return r.Summary
	}
	return r.Title
}

// NormalizeTerms lowercases and collapses "-", "_" and whitespace runs to a
// single space in each term, dropping blanks. Exported so the CLI layer can
// validate "no terms" (ErrNoTerms) before calling Search, and so JSON output
// can echo back what was actually matched against.
func NormalizeTerms(raw []string) []string {
	var out []string
	for _, t := range raw {
		n := normalize(t)
		if n != "" {
			out = append(out, n)
		}
	}
	return out
}

// Search scores every page vault.Walk() returns (minus config.Find.Exclude,
// unless opts.All) against terms (OR'ed; already normalized via
// NormalizeTerms), sorts by score desc then path asc, and returns every
// match (the caller applies --limit). total is len(results) before any
// limiting, i.e. always len(results) here — the CLI slices afterwards so it
// can report the pre-limit count.
func Search(v *vault.Vault, terms []string, opts Options) ([]Result, error) {
	if len(terms) == 0 {
		return nil, ErrNoTerms
	}

	files, err := v.Walk()
	if err != nil {
		return nil, err
	}
	if !opts.All {
		files = excludeFind(v.Config.Find.Exclude, files)
	}

	index, err := loadIndex(v)
	if err != nil {
		return nil, err
	}

	var results []Result
	for _, rel := range files {
		full := filepath.Join(v.Root, filepath.FromSlash(rel))
		src, err := os.ReadFile(full)
		if err != nil {
			continue // best effort: an unreadable file just doesn't match
		}
		pi := newPageInfo(rel, src, v.Config.Timeline)
		pi.indexSummary = index[pi.slug]

		if opts.Type != "" && pi.fmType != opts.Type {
			continue
		}

		score, matched, body := scorePage(pi, terms, opts)
		if score == 0 {
			continue
		}
		results = append(results, Result{
			Path:    rel,
			Type:    pi.fmType,
			Title:   pi.title,
			Summary: pi.indexSummary,
			Score:   score,
			Matched: matched,
			Body:    body,
		})
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].Path < results[j].Path
	})
	return results, nil
}

func excludeFind(patterns []string, files []string) []string {
	if len(patterns) == 0 {
		return files
	}
	var out []string
	for _, f := range files {
		if !vault.MatchAny(patterns, f) {
			out = append(out, f)
		}
	}
	return out
}

// ---- per-page metadata -----------------------------------------------

type pageInfo struct {
	rel          string
	slug         string // basename without .md, as written (index lookup key)
	slugNorm     string
	fmType       string
	title        string
	titleNorm    string
	aliasesNorm  []string
	tagsNorm     []string
	h1           string
	h1Norm       string
	indexSummary string

	// body support (lazy: only computed when needed)
	src     []byte
	tlCfg   config.Timeline
	doc     *doc.Doc
	bodyLns []bodyLine
	bodyLnd bool
}

type bodyLine struct {
	num  int
	text string
}

func newPageInfo(rel string, src []byte, tlCfg config.Timeline) *pageInfo {
	slug := strings.TrimSuffix(path.Base(rel), ".md")
	pi := &pageInfo{rel: rel, slug: slug, slugNorm: normalize(slug), src: src, tlCfg: tlCfg}
	pi.doc = doc.Parse(rel, src)

	fm := parseFrontmatter(pi.doc)
	pi.fmType = fm.Type
	pi.title = fm.Title
	pi.titleNorm = normalize(fm.Title)
	for _, a := range fm.Aliases {
		if n := normalize(a); n != "" {
			pi.aliasesNorm = append(pi.aliasesNorm, n)
		}
	}
	for _, t := range fm.Tags {
		if n := normalize(t); n != "" {
			pi.tagsNorm = append(pi.tagsNorm, n)
		}
	}

	for _, h := range doc.Headings(pi.doc) {
		if h.Level == 1 {
			pi.h1 = h.Text
			pi.h1Norm = normalize(h.Text)
			break
		}
	}
	return pi
}

// compiledTruthLines lazily splits the page's compiled-truth span
// (DESIGN.md §5.2, the same span `timeline read`'s default mode prints)
// into 1-based (line, text) pairs, for the --body fallback field.
func (pi *pageInfo) compiledTruthLines() []bodyLine {
	if pi.bodyLnd {
		return pi.bodyLns
	}
	pi.bodyLnd = true
	page := timeline.Parse(pi.doc, pi.tlCfg)
	start := page.CompiledTruth.Start
	end := page.CompiledTruth.End
	if end <= start {
		return nil
	}
	firstLine := pi.doc.LineOf(start)
	lastLine := pi.doc.LineOf(end - 1)
	for n := firstLine; n <= lastLine; n++ {
		text := pi.doc.Line(n)
		if strings.TrimSpace(text) == "" {
			continue
		}
		pi.bodyLns = append(pi.bodyLns, bodyLine{num: n, text: text})
	}
	return pi.bodyLns
}

// ---- frontmatter (lenient) ---------------------------------------------

type frontmatter struct {
	Type    string
	Title   string
	Aliases []string
	Tags    []string
}

type rawFrontmatter struct {
	Type    string `yaml:"type"`
	Title   string `yaml:"title"`
	Aliases any    `yaml:"aliases"`
	Tags    any    `yaml:"tags"`
}

// parseFrontmatter parses d's frontmatter leniently: broken YAML, or no
// frontmatter at all, yields a zero-value frontmatter rather than an error
// — a page with malformed frontmatter must still match on slug/H1/body
// (DESIGN.md §19: "never fails the command").
func parseFrontmatter(d *doc.Doc) frontmatter {
	if !d.HasFM {
		return frontmatter{}
	}
	inner := frontmatterInner(d)
	var raw rawFrontmatter
	if err := yaml.Unmarshal([]byte(inner), &raw); err != nil {
		return frontmatter{}
	}
	return frontmatter{
		Type:    raw.Type,
		Title:   raw.Title,
		Aliases: toStringSlice(raw.Aliases),
		Tags:    toStringSlice(raw.Tags),
	}
}

// frontmatterInner strips the two "---" delimiter lines, returning just the
// YAML body between them.
func frontmatterInner(d *doc.Doc) string {
	raw := string(d.Src[d.Frontmatter.Start:d.Frontmatter.End])
	lines := strings.Split(raw, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) < 2 {
		return ""
	}
	return strings.Join(lines[1:len(lines)-1], "\n")
}

func toStringSlice(v any) []string {
	switch x := v.(type) {
	case nil:
		return nil
	case string:
		if x == "" {
			return nil
		}
		return []string{x}
	case []any:
		var out []string
		for _, e := range x {
			if s, ok := e.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

// ---- index.md summaries -------------------------------------------------

// indexLineRe matches "- [[name]] — summary (YYYY-MM-DD)" lines (DESIGN.md
// §19: the vault's index.md convention). The name may itself be a wikilink
// with an alias/anchor ("name|alias" or "name#heading"); only the part
// before "|"/"#" is the lookup key, matching page-name resolution elsewhere
// (vault.Resolve).
var indexLineRe = regexp.MustCompile(`^- \[\[([^\]]+)\]\] — (.+?) \(\d{4}-\d{2}-\d{2}\)\s*$`)

// loadIndex reads config.Find.Index (default "index.md") and returns a
// name -> summary map. A missing file is skipped silently; lines that don't
// match the convention are ignored, never an error.
func loadIndex(v *vault.Vault) (map[string]string, error) {
	full := filepath.Join(v.Root, filepath.FromSlash(v.Config.Find.Index))
	b, err := os.ReadFile(full)
	if err != nil {
		return map[string]string{}, nil
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		m := indexLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		name := m[1]
		if i := strings.IndexAny(name, "|#"); i != -1 {
			name = name[:i]
		}
		out[name] = m[2]
	}
	return out, nil
}

// ---- normalization & scoring --------------------------------------------

// sepRun matches runs of "-", "_" and whitespace, the equivalence classes
// find's matching treats as interchangeable (DESIGN.md §19: "po agent",
// "po-agent" and "po_agent" all match each other).
var sepRun = regexp.MustCompile(`[-_\s]+`)

func normalize(s string) string {
	s = strings.ToLower(s)
	s = sepRun.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

type fieldScore struct {
	field  string
	weight int
}

// bestMetadataField returns the single best-matching metadata field for one
// (already-normalized) term, or a zero fieldScore if none match. Candidates
// are considered in DESIGN.md's weight order; a strict ">" keeps that order
// as the tie-break when two fields score equally (e.g. an exact title and
// an exact alias both at 40).
func bestMetadataField(term string, pi *pageInfo) fieldScore {
	var best fieldScore

	consider := func(field string, weight int) {
		if weight > best.weight {
			best = fieldScore{field: field, weight: weight}
		}
	}

	consider("slug", scoreExactSub(term, pi.slugNorm, weightSlugExact, weightSlugSub))
	consider("title", scoreExactSub(term, pi.titleNorm, weightTitleExact, weightTitleSub))

	aliasBest := 0
	for _, a := range pi.aliasesNorm {
		if w := scoreExactSub(term, a, weightAliasExact, weightAliasSub); w > aliasBest {
			aliasBest = w
		}
	}
	consider("alias", aliasBest)

	tagBest := 0
	for _, t := range pi.tagsNorm {
		if t != "" && strings.Contains(t, term) {
			tagBest = weightTag
			break
		}
	}
	consider("tag", tagBest)

	if pi.indexSummary != "" && strings.Contains(normalize(pi.indexSummary), term) {
		consider("index", weightIndex)
	}
	if pi.h1Norm != "" && strings.Contains(pi.h1Norm, term) {
		consider("h1", weightH1)
	}

	return best
}

func scoreExactSub(term, fieldNorm string, exactW, subW int) int {
	if fieldNorm == "" || term == "" {
		return 0
	}
	if fieldNorm == term {
		return exactW
	}
	if strings.Contains(fieldNorm, term) {
		return subW
	}
	return 0
}

// scorePage sums, per term, the single best-matching field's weight (0 if
// none), returning the page's total score, the deduplicated list of fields
// that contributed (in first-contributed order), and — with opts.Body — up
// to 3 compiled-truth lines recorded for whichever terms only matched via
// body text (DESIGN.md §19).
func scorePage(pi *pageInfo, terms []string, opts Options) (score int, matched []string, body []BodyMatch) {
	seen := map[string]bool{}
	add := func(field string) {
		if !seen[field] {
			seen[field] = true
			matched = append(matched, field)
		}
	}

	var bodyTerms []string
	for _, t := range terms {
		bf := bestMetadataField(t, pi)
		if bf.weight > 0 {
			score += bf.weight
			add(bf.field)
			continue
		}
		if opts.Body {
			bodyTerms = append(bodyTerms, t)
		}
	}

	if len(bodyTerms) == 0 {
		return score, matched, nil
	}

	found := make([]bool, len(bodyTerms))
	for _, ln := range pi.compiledTruthLines() {
		normed := normalize(ln.text)
		for i, t := range bodyTerms {
			if found[i] || !strings.Contains(normed, t) {
				continue
			}
			found[i] = true
			if len(body) < 3 {
				body = append(body, BodyMatch{Line: ln.num, Text: snippet(ln.text)})
			}
		}
	}
	anyFound := false
	for _, f := range found {
		if f {
			anyFound = true
			score += weightBody
		}
	}
	if anyFound {
		add("body")
	}
	return score, matched, body
}

// snippet trims s and caps it at 120 bytes, backing off to the nearest
// UTF-8 rune boundary so a multi-byte rune is never split.
func snippet(s string) string {
	s = strings.TrimSpace(s)
	const max = 120
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
