package search

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/analysis"
	"github.com/blevesearch/bleve/v2/search/query"
)

// ErrQuery marks a query string that cannot be parsed (exit 2).
var ErrQuery = errors.New("invalid query")

// clauseKind is the kind of one positive or negated query term.
type clauseKind int

const (
	kindWord   clauseKind = iota // plain word: analyzed, every analyzer + raw
	kindPhrase                   // "exact phrase": raw field, in order
	kindFuzzy                    // term~ / term~N: raw field, edit distance
	kindPrefix                   // term*: raw field, prefix
)

// fuzzyLongRunes is the rune length from which a bare `term~` allows edit
// distance 2; shorter terms get 1 (DESIGN.md §19.1).
const fuzzyLongRunes = 6

// Clause is one term of the query.
type Clause struct {
	Kind  clauseKind
	Text  string // phrase/word text, or the fuzzy/prefix stem (without ~/*)
	Fuzz  int    // kindFuzzy: edit distance
	terms []string
}

// Filter is an exact frontmatter match: the page's frontmatter key Key has
// Value (list keys: contains Value).
type Filter struct {
	Key   string
	Value string
}

// Query is a parsed query string.
type Query struct {
	Raw        string
	Clauses    []Clause // OR'ed, ranked
	Negated    []Clause // exclude pages matching any of these
	Filters    []Filter // AND'ed
	NotFilters []Filter // exclude pages matching any of these
}

// Positive reports whether the query has any ranked terms.
func (q *Query) Positive() bool { return len(q.Clauses) > 0 }

// fuzzyDistRe matches a fuzzy term suffix: "~" optionally followed by digits.
var fuzzyDistRe = regexp.MustCompile(`~(\d*)$`)

// filterKeyRe is a field:value key: a letter or "_" first, then letters,
// digits, "_", "-", ".".
var filterKeyRe = regexp.MustCompile(`^[\p{L}_][\p{L}\p{N}_.-]*$`)

// filterAliases maps query-string field names onto frontmatter keys.
var filterAliases = map[string]string{"tag": "tags"}

// ParseFilter parses a --where "key=value" argument, splitting on the first
// "=" only (values may contain "=" and "/").
func ParseFilter(s string) (Filter, error) {
	k, v, ok := strings.Cut(s, "=")
	k = strings.TrimSpace(k)
	if !ok || k == "" || v == "" {
		return Filter{}, fmt.Errorf("--where %q: want key=value", s)
	}
	return Filter{Key: k, Value: v}, nil
}

// ParseQuery parses the query language (DESIGN.md §19.1). A query that is
// empty after parsing is not an error here; the caller decides (a
// filter-only search is valid).
func ParseQuery(raw string) (*Query, error) {
	q := &Query{Raw: raw}
	s := raw
	i := 0
	for i < len(s) {
		r, w := utf8.DecodeRuneInString(s[i:])
		if isSpace(r) {
			i += w
			continue
		}
		neg := false
		if r == '-' {
			neg = true
			i++
			if i >= len(s) || isSpaceByte(s[i]) {
				return nil, fmt.Errorf("%w: lone \"-\"", ErrQuery)
			}
		}
		var tok string
		quotedValue := false
		if s[i] == '"' {
			end := strings.IndexByte(s[i+1:], '"')
			if end < 0 {
				return nil, fmt.Errorf("%w: unterminated quote", ErrQuery)
			}
			phrase := s[i+1 : i+1+end]
			i += end + 2
			if strings.TrimSpace(phrase) == "" {
				return nil, fmt.Errorf("%w: empty phrase", ErrQuery)
			}
			c := Clause{Kind: kindPhrase, Text: phrase}
			q.add(c, neg)
			continue
		}
		start := i
		for i < len(s) {
			r, w := utf8.DecodeRuneInString(s[i:])
			if isSpace(r) {
				break
			}
			if r == '"' {
				// key:"quoted value"
				if i > start && s[i-1] == ':' {
					end := strings.IndexByte(s[i+1:], '"')
					if end < 0 {
						return nil, fmt.Errorf("%w: unterminated quote", ErrQuery)
					}
					i += end + 2
					quotedValue = true
					break
				}
				return nil, fmt.Errorf("%w: stray quote in %q", ErrQuery, s[start:])
			}
			i += w
		}
		tok = s[start:i]

		if k, v, ok := strings.Cut(tok, ":"); ok && filterKeyRe.MatchString(k) {
			if quotedValue {
				v = strings.TrimSuffix(strings.TrimPrefix(v, `"`), `"`)
			}
			if v == "" {
				return nil, fmt.Errorf("%w: %q has no value", ErrQuery, tok)
			}
			if a, ok := filterAliases[k]; ok {
				k = a
			}
			f := Filter{Key: k, Value: v}
			if neg {
				q.NotFilters = append(q.NotFilters, f)
			} else {
				q.Filters = append(q.Filters, f)
			}
			continue
		}

		c, err := parseTerm(tok)
		if err != nil {
			return nil, err
		}
		q.add(c, neg)
	}
	return q, nil
}

func (q *Query) add(c Clause, neg bool) {
	if neg {
		q.Negated = append(q.Negated, c)
	} else {
		q.Clauses = append(q.Clauses, c)
	}
}

func parseTerm(tok string) (Clause, error) {
	switch {
	case strings.HasSuffix(tok, "*"):
		stem := strings.TrimSuffix(tok, "*")
		if stem == "" || strings.HasSuffix(stem, "*") {
			return Clause{}, fmt.Errorf("%w: %q needs a prefix before \"*\"", ErrQuery, tok)
		}
		return Clause{Kind: kindPrefix, Text: stem}, nil
	case fuzzyDistRe.MatchString(tok):
		m := fuzzyDistRe.FindStringSubmatch(tok)
		stem, dist := tok[:len(tok)-len(m[0])], m[1]
		if dist != "" && dist != "1" && dist != "2" {
			return Clause{}, fmt.Errorf("%w: %q: fuzzy distance must be 1 or 2", ErrQuery, tok)
		}
		if stem == "" {
			return Clause{}, fmt.Errorf("%w: %q needs a term before \"~\"", ErrQuery, tok)
		}
		fuzz := 1
		switch dist {
		case "1":
		case "2":
			fuzz = 2
		default:
			if utf8.RuneCountInString(stem) >= fuzzyLongRunes {
				fuzz = 2
			}
		}
		return Clause{Kind: kindFuzzy, Text: stem, Fuzz: fuzz}, nil
	}
	return Clause{Kind: kindWord, Text: tok}, nil
}

func isSpace(r rune) bool     { return r == ' ' || r == '\t' || r == '\n' || r == '\r' }
func isSpaceByte(b byte) bool { return isSpace(rune(b)) }

// analyzeTerms runs text through analyzer a and returns its terms.
func analyzeTerms(a analysis.Analyzer, text string) []string {
	var out []string
	for _, t := range a.Analyze([]byte(text)) {
		out = append(out, string(t.Term))
	}
	return out
}

// resolve tokenizes every clause with the raw analyzer and drops clauses
// with nothing searchable left (e.g. pure punctuation). A query whose search
// terms all resolve to nothing is an ErrQuery, even when filters are present:
// the caller asked for terms, and silently listing every page would be wrong.
func (q *Query) resolve(raw analysis.Analyzer) error {
	hadTerms := len(q.Clauses)+len(q.Negated) > 0
	fix := func(cs []Clause) []Clause {
		var out []Clause
		for _, c := range cs {
			c.terms = analyzeTerms(raw, c.Text)
			if len(c.terms) > 0 {
				out = append(out, c)
			}
		}
		return out
	}
	q.Clauses = fix(q.Clauses)
	q.Negated = fix(q.Negated)
	if hadTerms && len(q.Clauses)+len(q.Negated) == 0 {
		return fmt.Errorf("%w: %q has no searchable terms", ErrQuery, q.Raw)
	}
	return nil
}

// clauseQuery builds one clause as a disjunction over every searched field.
func clauseQuery(c Clause, groups, analyzers []string, boosted bool) query.Query {
	var subs []query.Query
	boost := func(g string) float64 {
		if boosted {
			return groupBoost[g]
		}
		return 1
	}
	for _, g := range groups {
		rawField := groupField(g, rawAnalyzer)
		switch c.Kind {
		case kindWord:
			for _, a := range append(append([]string{}, analyzers...), rawAnalyzer) {
				mq := bleve.NewMatchQuery(c.Text)
				mq.SetField(groupField(g, a))
				// Explicit: bleve resolves analyzers by document path,
				// which a "<group>_<analyzer>" sub-field name isn't.
				mq.Analyzer = a
				mq.SetBoost(boost(g))
				subs = append(subs, mq)
			}
		case kindPhrase:
			pq := bleve.NewMatchPhraseQuery(c.Text)
			pq.SetField(rawField)
			pq.Analyzer = rawAnalyzer
			pq.SetBoost(boost(g))
			subs = append(subs, pq)
		case kindFuzzy:
			for _, t := range c.terms {
				fq := bleve.NewFuzzyQuery(t)
				fq.SetField(rawField)
				fq.SetFuzziness(c.Fuzz)
				fq.SetBoost(boost(g))
				subs = append(subs, fq)
			}
		case kindPrefix:
			for _, t := range c.terms {
				pq := bleve.NewPrefixQuery(t)
				pq.SetField(rawField)
				pq.SetBoost(boost(g))
				subs = append(subs, pq)
			}
		}
	}
	return bleve.NewDisjunctionQuery(subs...)
}

// filterQuery matches pages whose frontmatter has key=value.
func filterQuery(f Filter) query.Query {
	tq := bleve.NewTermQuery(f.Key + "=" + f.Value)
	tq.SetField(fieldFM)
	return tq
}

// build assembles the bleve query: ranked clauses (Should), negations
// (MustNot), and filters plus the --only doc-id set as a non-scoring
// boolean Filter, so narrowing never changes a page's score. A query with
// no ranked clauses matches everything its filters/negations allow.
func (q *Query) build(groups, analyzers []string, onlyIDs []string, onlyActive bool, extra []Filter) query.Query {
	bq := query.NewBooleanQuery(nil, nil, nil)
	for _, c := range q.Clauses {
		bq.AddShould(clauseQuery(c, groups, analyzers, true))
	}
	for _, c := range q.Negated {
		bq.AddMustNot(clauseQuery(c, groups, analyzers, false))
	}
	for _, f := range q.NotFilters {
		bq.AddMustNot(filterQuery(f))
	}
	var filters []query.Query
	for _, f := range append(append([]Filter{}, q.Filters...), extra...) {
		filters = append(filters, filterQuery(f))
	}
	if onlyActive {
		filters = append(filters, bleve.NewDocIDQuery(onlyIDs))
	}
	if len(filters) > 0 {
		bq.AddFilter(bleve.NewConjunctionQuery(filters...))
	}
	if len(q.Clauses) == 0 && len(filters) == 0 {
		bq.AddMust(bleve.NewMatchAllQuery())
	}
	return bq
}
