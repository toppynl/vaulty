// Package find implements `vaulty find`: term(s) in, ranked vault-relative
// page paths out (DESIGN.md §18). It replaces raw grep/find as the LLM
// reader agent's discovery step over the vault.
package find

import (
	"errors"
	"os"
	"path"
	"sort"
	"strings"
	"unicode"

	"github.com/toppynl/vaulty/internal/config"
	"github.com/toppynl/vaulty/internal/doc"
	"github.com/toppynl/vaulty/internal/page"
	"github.com/toppynl/vaulty/internal/timeline"
	"github.com/toppynl/vaulty/internal/vault"
)

// ErrNoTerms is returned when every given term is blank (exit 2, DESIGN.md §18.2).
var ErrNoTerms = errors.New("no search terms given")

// weights is the single named table of field weights (DESIGN.md §18.1).
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
	Body bool     // scan compiled-truth body text as a fallback field
	Only []string // raw --only tokens (dir names or globs); empty = whole vault
	Type string   // filter on frontmatter `type` (exact match); "" = no filter
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

// NormalizeTerms tokenizes each term (see tokenize) and rejoins it with
// single spaces, dropping any term with no tokens at all (e.g. blank, or
// pure punctuation). Exported so the CLI layer can validate "no terms"
// (ErrNoTerms) before calling Search, and so JSON output can echo back
// what was actually matched against.
func NormalizeTerms(raw []string) []string {
	var out []string
	for _, t := range raw {
		toks := tokenize(t)
		if len(toks) == 0 {
			continue
		}
		out = append(out, strings.Join(toks, " "))
	}
	return out
}

// Search scores every page vault.Walk() returns — the whole vault (minus
// the global config.Exclude, already applied by Walk) by default; opts.Only
// narrows that further via vault.OnlyPatterns/FilterOnly (DESIGN.md §18) —
// against terms (OR'ed; already normalized via NormalizeTerms), sorts by
// score desc then path asc, and returns every match (the caller applies
// --limit). total is len(results) before any limiting, i.e. always
// len(results) here — the CLI slices afterwards so it can report the
// pre-limit count.
func Search(v *vault.Vault, terms []string, opts Options) ([]Result, error) {
	if len(terms) == 0 {
		return nil, ErrNoTerms
	}

	files, err := v.Walk()
	if err != nil {
		return nil, err
	}
	files = vault.FilterOnly(files, vault.OnlyPatterns(opts.Only))

	idx, err := v.ConfigFile(v.Config.Find.Index)
	if err != nil {
		return nil, err
	}
	index := page.LoadIndex(idx)

	termToks := make([][]string, len(terms))
	for i, t := range terms {
		termToks[i] = tokenize(t)
	}

	var results []Result
	for _, rel := range files {
		full, err := v.ContentFile(rel)
		if err != nil {
			continue
		}
		src, err := os.ReadFile(full)
		if err != nil {
			continue // best effort: an unreadable file just doesn't match
		}
		pi := newPageInfo(rel, src, v.Config.Timeline)
		pi.setIndexSummary(index[pi.slug])

		if opts.Type != "" && pi.fmType != opts.Type {
			continue
		}

		score, matched, body := scorePage(pi, termToks, opts)
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

// ---- per-page metadata -----------------------------------------------

type pageInfo struct {
	rel                string
	slug               string // basename without .md, as written (index lookup key)
	slugTokens         []string
	fmType             string
	title              string
	titleTokens        []string
	aliasesTokens      [][]string
	tagsTokens         [][]string
	h1                 string
	h1Tokens           []string
	indexSummary       string
	indexSummaryTokens []string

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
	pi := &pageInfo{rel: rel, slug: slug, slugTokens: tokenize(slug), src: src, tlCfg: tlCfg}
	pi.doc = doc.Parse(rel, src)

	fm := page.ParseFrontmatter(pi.doc)
	pi.fmType = fm.Type
	pi.title = fm.Title
	pi.titleTokens = tokenize(fm.Title)
	for _, a := range fm.Aliases {
		if toks := tokenize(a); len(toks) > 0 {
			pi.aliasesTokens = append(pi.aliasesTokens, toks)
		}
	}
	for _, t := range fm.Tags {
		if toks := tokenize(t); len(toks) > 0 {
			pi.tagsTokens = append(pi.tagsTokens, toks)
		}
	}

	pi.h1 = page.FirstH1(pi.doc)
	pi.h1Tokens = tokenize(pi.h1)
	return pi
}

// setIndexSummary records the page's index.md summary (raw, for Result
// output, and tokenized, for matching).
func (pi *pageInfo) setIndexSummary(s string) {
	pi.indexSummary = s
	pi.indexSummaryTokens = tokenize(s)
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

// ---- tokenization & scoring ----------------------------------------------

// tokenize splits s into lowercase runs of Unicode letters/digits, treating
// every other character (the separator class this tool's matching treats
// as interchangeable: "-", "_", whitespace, and any other punctuation like
// ".", "/", "(", ":") as a token boundary. This is what makes "po agent",
// "po-agent" and "po_agent" all tokenize to ["po","agent"] and match each
// other (DESIGN.md §18), while also stopping a term like "ai" from
// substring-matching inside an unrelated word like "failed" or "email" —
// "ai" only ever appears as its own token, never as a run of letters
// spanning two real words. unicode.IsLetter/IsDigit make this correct for
// non-ASCII vault text (Dutch).
func tokenize(s string) []string {
	var tokens []string
	var cur []rune
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			cur = append(cur, unicode.ToLower(r))
			continue
		}
		if len(cur) > 0 {
			tokens = append(tokens, string(cur))
			cur = cur[:0]
		}
	}
	if len(cur) > 0 {
		tokens = append(tokens, string(cur))
	}
	return tokens
}

// tokenExact reports whether a and b are the same length and equal
// token-for-token.
func tokenExact(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// tokenPrefixWindow reports whether term's token sequence appears
// contiguously somewhere in field's, with every term token a prefix of the
// aligned field token (DESIGN.md §18: "dam" matches "dam-cutoff", "stock"
// matches "stocky", "po agent" matches "po-agent", but "ai" does not match
// "payment-failed" — "ai" is never a prefix of "payment" or "failed").
func tokenPrefixWindow(term, field []string) bool {
	n, m := len(term), len(field)
	if n == 0 || m == 0 || n > m {
		return false
	}
	for start := 0; start+n <= m; start++ {
		ok := true
		for i := 0; i < n; i++ {
			if !strings.HasPrefix(field[start+i], term[i]) {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

type fieldScore struct {
	field  string
	weight int
}

// bestMetadataField returns the single best-matching metadata field for one
// term (already tokenized), or a zero fieldScore if none match. Candidates
// are considered in DESIGN.md's weight order; a strict ">" keeps that order
// as the tie-break when two fields score equally (e.g. an exact title and
// an exact alias both at 40).
func bestMetadataField(term []string, pi *pageInfo) fieldScore {
	var best fieldScore

	consider := func(field string, weight int) {
		if weight > best.weight {
			best = fieldScore{field: field, weight: weight}
		}
	}

	consider("slug", scoreExactSub(term, pi.slugTokens, weightSlugExact, weightSlugSub))
	consider("title", scoreExactSub(term, pi.titleTokens, weightTitleExact, weightTitleSub))

	aliasBest := 0
	for _, a := range pi.aliasesTokens {
		if w := scoreExactSub(term, a, weightAliasExact, weightAliasSub); w > aliasBest {
			aliasBest = w
		}
	}
	consider("alias", aliasBest)

	tagBest := 0
	for _, t := range pi.tagsTokens {
		if tokenPrefixWindow(term, t) {
			tagBest = weightTag
			break
		}
	}
	consider("tag", tagBest)

	if tokenPrefixWindow(term, pi.indexSummaryTokens) {
		consider("index", weightIndex)
	}
	if tokenPrefixWindow(term, pi.h1Tokens) {
		consider("h1", weightH1)
	}

	return best
}

// scoreExactSub scores one field against term: exactW when the whole
// token sequences are equal, subW when term's tokens merely align (as
// prefixes) somewhere in field's tokens, 0 otherwise.
func scoreExactSub(term, field []string, exactW, subW int) int {
	if len(term) == 0 || len(field) == 0 {
		return 0
	}
	if tokenExact(term, field) {
		return exactW
	}
	if tokenPrefixWindow(term, field) {
		return subW
	}
	return 0
}

// scorePage sums, per term, the single best-matching field's weight (0 if
// none), returning the page's total score, the deduplicated list of fields
// that contributed (in first-contributed order), and — with opts.Body — the
// first compiled-truth line recorded per body-fallback term, capped at 3
// lines total per page (DESIGN.md §18.4).
func scorePage(pi *pageInfo, termToks [][]string, opts Options) (score int, matched []string, body []BodyMatch) {
	seen := map[string]bool{}
	add := func(field string) {
		if !seen[field] {
			seen[field] = true
			matched = append(matched, field)
		}
	}

	var bodyTermToks [][]string
	for _, t := range termToks {
		bf := bestMetadataField(t, pi)
		if bf.weight > 0 {
			score += bf.weight
			add(bf.field)
			continue
		}
		if opts.Body {
			bodyTermToks = append(bodyTermToks, t)
		}
	}

	if len(bodyTermToks) == 0 {
		return score, matched, nil
	}

	// A found term stops scanning for that term (first matching line per
	// body term), and recording stops once the page-wide cap of 3 lines is
	// hit — whichever comes first.
	found := make([]bool, len(bodyTermToks))
	for _, ln := range pi.compiledTruthLines() {
		lineTokens := tokenize(ln.text)
		for i, t := range bodyTermToks {
			if found[i] {
				continue
			}
			if tokenPrefixWindow(t, lineTokens) {
				found[i] = true
				if len(body) < 3 {
					body = append(body, BodyMatch{Line: ln.num, Text: snippet(ln.text)})
				}
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
	return page.CapBytes(strings.TrimSpace(s), 120)
}
