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
	"github.com/toppynl/vaulty/internal/filter"
	"github.com/toppynl/vaulty/internal/page"
	"github.com/toppynl/vaulty/internal/timeline"
	"github.com/toppynl/vaulty/internal/vault"
)

// ErrNoTerms is returned when every given term is blank and no --where/--type
// filter was given either (exit 2, DESIGN.md §18.2): with a filter present,
// find is valid with no terms at all (item 3 of the fields/where follow-up
// — same as `search`, DESIGN.md §19.1's filter-only query).
var ErrNoTerms = errors.New("no search terms given")

// Options are the `find` flags that affect scoring/scope, beyond the terms
// themselves.
type Options struct {
	Body  bool            // scan compiled-truth body text as a fallback field
	Only  []string        // raw --only tokens (dir names or globs); empty = whole vault
	Type  string          // filter on frontmatter `type` (exact match); "" = no filter
	Where []filter.Filter // --where key=value, AND'ed, exact/case-sensitive (list-contains)
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
// (together with a filter check, ErrNoTerms) before calling Search, and so
// JSON output can echo back what was actually matched against.
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

// termInfo is one query term in both forms a config.FindField.Match mode
// needs: tokenized (match: token, DESIGN.md §18.1's prefix-window rule) and
// raw/trimmed, case-folded (match: exact, a whole-value equality check that
// never tokenizes — so punctuation like "/" in an id survives).
type termInfo struct {
	tokens []string
	rawLC  string // trimmed, lowercased
}

// buildTerms computes termInfo for every rawTerm with at least one token,
// in the same order NormalizeTerms would keep them (dropping the same
// blanks), so the two stay in step for validation/JSON display vs. actual
// matching.
func buildTerms(rawTerms []string) []termInfo {
	var out []termInfo
	for _, t := range rawTerms {
		toks := tokenize(t)
		if len(toks) == 0 {
			continue
		}
		out = append(out, termInfo{tokens: toks, rawLC: strings.ToLower(strings.TrimSpace(t))})
	}
	return out
}

// Search scores every page vault.Walk() returns — the whole vault (minus
// the global config.Exclude, already applied by Walk) by default; opts.Only
// narrows that further via vault.OnlyPatterns/FilterOnly (DESIGN.md §18) —
// against rawTerms (OR'ed), sorts by score desc then path asc, and returns
// every match (the caller applies --limit). rawTerms are the caller's
// original arguments (not pre-tokenized): Search tokenizes them itself, and
// separately keeps each term's raw, case-folded text for match: "exact"
// fields (config.FindField), which must never be split on punctuation like
// tokenizing would do.
//
// With no terms at all (every rawTerm blank), Search requires at least one
// of opts.Type/opts.Where — a filter-only call, mirroring `search`'s
// filter-only query (DESIGN.md §19.1) — and returns every matching page
// sorted by path, score 0. With no terms and no filter, it's the caller's
// mistake (ErrNoTerms).
func Search(v *vault.Vault, rawTerms []string, opts Options) ([]Result, error) {
	terms := buildTerms(rawTerms)
	if len(terms) == 0 && opts.Type == "" && len(opts.Where) == 0 {
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
	fields := v.Config.Find.Fields

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
		pi := newPageInfo(rel, src, v.Config)
		pi.setIndexSummary(index[pi.slug])

		if opts.Type != "" && pi.fmType != opts.Type {
			continue
		}
		if !filter.MatchAll(pi.fmValues, opts.Where) {
			continue
		}

		if len(terms) == 0 {
			// Filter-only: every match counts, score 0 (sorted by path below).
			results = append(results, Result{
				Path: rel, Type: pi.fmType, Title: pi.title, Summary: pi.indexSummary, Matched: []string{},
			})
			continue
		}

		score, matched, body := scorePage(pi, terms, fields, opts)
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
	slug         string // basename without .md, as written (index lookup key)
	fmType       string
	title        string
	fmValues     map[string][]string // generic frontmatter (page.FrontmatterValues); backs source: frontmatter and --where
	h1           string
	indexSummary string

	// body support (lazy: only computed when needed)
	tlCfg   config.Timeline
	doc     *doc.Doc
	bodyLns []bodyLine
	bodyLnd bool
}

type bodyLine struct {
	num  int
	text string
}

func newPageInfo(rel string, src []byte, cfg *config.Config) *pageInfo {
	slug := strings.TrimSuffix(path.Base(rel), ".md")
	pi := &pageInfo{slug: slug, tlCfg: cfg.Timeline}
	pi.doc = doc.Parse(rel, src)

	fm := page.ParseFrontmatter(pi.doc, cfg.Fields)
	pi.fmType = fm.Type
	pi.title = fm.Title
	pi.fmValues = fm.Values

	pi.h1 = page.FirstH1(pi.doc)
	return pi
}

// setIndexSummary records the page's index.md summary (raw, for Result
// output, and matching).
func (pi *pageInfo) setIndexSummary(s string) {
	pi.indexSummary = s
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

// values returns f's raw candidate value(s) on pi, or nil if f has none
// (e.g. an empty index summary, or a frontmatter key the page doesn't set).
func (pi *pageInfo) values(f config.FindField) []string {
	switch f.Source {
	case "slug":
		return []string{pi.slug}
	case "frontmatter":
		return pi.fmValues[f.Key]
	case "index":
		if pi.indexSummary == "" {
			return nil
		}
		return []string{pi.indexSummary}
	case "h1":
		if pi.h1 == "" {
			return nil
		}
		return []string{pi.h1}
	}
	return nil
}

// label is the "matched" field name reported for f (DESIGN.md §18.5),
// keeping the built-in names find has always used (singular "alias"/"tag"
// for the plural frontmatter keys) so a vault on the default config sees
// byte-identical output; a vault-defined frontmatter key not among these
// reports its own key name.
func label(f config.FindField) string {
	switch f.Source {
	case "frontmatter":
		switch f.Key {
		case "title":
			return "title"
		case "aliases":
			return "alias"
		case "tags":
			return "tag"
		default:
			return f.Key
		}
	default:
		return f.Source
	}
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

// matchField scores term against f's value(s) on pi: 0 if none match, else
// f.Weight — except f.Source == "slug" with match: token, which keeps its
// exact-match bonus (DESIGN.md §18.2, config.FindField's doc comment): a
// full token-sequence match scores f.Weight, a mere substring/prefix-window
// match scores f.Weight/2.
func matchField(term termInfo, pi *pageInfo, f config.FindField) int {
	vals := pi.values(f)
	if len(vals) == 0 {
		return 0
	}
	for _, v := range vals {
		if f.Match == "exact" {
			if strings.ToLower(strings.TrimSpace(v)) == term.rawLC {
				return f.Weight
			}
			continue
		}
		vt := tokenize(v)
		if len(vt) == 0 {
			continue
		}
		if f.Source == "slug" {
			if tokenExact(term.tokens, vt) {
				return f.Weight
			}
			if tokenPrefixWindow(term.tokens, vt) {
				return f.Weight / 2
			}
			continue
		}
		if tokenPrefixWindow(term.tokens, vt) {
			return f.Weight
		}
	}
	return 0
}

// bestField returns the single best-matching non-body field for one term,
// or a zero weight if none match. fields is v.Config.Find.Fields in order;
// ties (equal weight) break in favor of the earlier entry (DESIGN.md
// §18.2), via a strict ">" comparison.
func bestField(term termInfo, pi *pageInfo, fields []config.FindField) (fieldLabel string, weight int) {
	for _, f := range fields {
		if f.Source == "body" {
			continue // body is a last-resort fallback only (§18.4), handled separately
		}
		if w := matchField(term, pi, f); w > weight {
			weight = w
			fieldLabel = label(f)
		}
	}
	return fieldLabel, weight
}

// bodyField returns config's "body" field entry, if any (the default config
// always has one; a vault-defined find.fields list might not).
func bodyField(fields []config.FindField) (config.FindField, bool) {
	for _, f := range fields {
		if f.Source == "body" {
			return f, true
		}
	}
	return config.FindField{}, false
}

// scorePage sums, per term, the single best-matching field's weight (0 if
// none), returning the page's total score, the deduplicated list of fields
// that contributed (in first-contributed order), and — with opts.Body — the
// first compiled-truth line recorded per body-fallback term, capped at 3
// lines total per page (DESIGN.md §18.4).
func scorePage(pi *pageInfo, terms []termInfo, fields []config.FindField, opts Options) (score int, matched []string, body []BodyMatch) {
	seen := map[string]bool{}
	add := func(field string) {
		if !seen[field] {
			seen[field] = true
			matched = append(matched, field)
		}
	}

	var bodyTerms []termInfo
	for _, t := range terms {
		fieldLabel, w := bestField(t, pi, fields)
		if w > 0 {
			score += w
			add(fieldLabel)
			continue
		}
		if opts.Body {
			bodyTerms = append(bodyTerms, t)
		}
	}

	bf, ok := bodyField(fields)
	if len(bodyTerms) == 0 || !ok {
		return score, matched, nil
	}

	// A found term stops scanning for that term (first matching line per
	// body term), and recording stops once the page-wide cap of 3 lines is
	// hit — whichever comes first.
	found := make([]bool, len(bodyTerms))
	for _, ln := range pi.compiledTruthLines() {
		lineTokens := tokenize(ln.text)
		for i, t := range bodyTerms {
			if found[i] {
				continue
			}
			if tokenPrefixWindow(t.tokens, lineTokens) {
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
			score += bf.Weight
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
