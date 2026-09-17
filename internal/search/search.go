package search

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/analysis"
	"github.com/blevesearch/bleve/v2/index/scorch"
	"github.com/blevesearch/bleve/v2/mapping"

	"github.com/toppynl/vaulty/internal/page"
	"github.com/toppynl/vaulty/internal/vault"
)

// NoneType is the facet key for pages without a frontmatter type.
const NoneType = "(none)"

// Options are the `search` flags beyond the query string.
type Options struct {
	Only     []string // raw --only tokens (dirs or globs)
	Where    []Filter // --where key=value and --type, AND'ed with query filters
	Limit    int      // 0 = unlimited
	Timeline bool     // also search the Timeline section
	NoCache  bool     // in-memory index for this call only
	Rebuild  bool     // force a full rebuild of the cached index
}

// Result is one hit.
type Result struct {
	Path     string    `json:"path"`
	Type     string    `json:"type"`
	Title    string    `json:"title"`
	Summary  string    `json:"summary"`
	Score    float64   `json:"score"`
	Snippets []Snippet `json:"snippets"`
}

// SummaryOrTitle is the index summary when present, else the title.
func (r Result) SummaryOrTitle() string {
	if r.Summary != "" {
		return r.Summary
	}
	return r.Title
}

// Response is a full search answer.
type Response struct {
	Total      int
	TypeCounts map[string]int
	Results    []Result
	// CacheNote is set when the cached index could not be used and the
	// search ran on an in-memory index instead (printed once to stderr).
	CacheNote string
}

// corpusFile is one walked page with its stat.
type corpusFile struct {
	rel     string
	size    int64
	mtimeNS int64
}

func (f corpusFile) slug() string { return strings.TrimSuffix(path.Base(f.rel), ".md") }

// corpus is vault.Walk() plus index summaries: what the index must reflect.
type corpus struct {
	v         *vault.Vault
	files     []corpusFile
	summaries map[string]string
}

func loadCorpus(v *vault.Vault) (*corpus, error) {
	rels, err := v.Walk()
	if err != nil {
		return nil, err
	}
	c := &corpus{v: v, summaries: page.LoadIndex(v.Root, v.Config.Find.Index)}
	for _, rel := range rels {
		st, err := os.Stat(filepath.Join(v.Root, filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		c.files = append(c.files, corpusFile{rel: rel, size: st.Size(), mtimeNS: st.ModTime().UnixNano()})
	}
	return c, nil
}

// load reads and parses one page, returning its manifest entry.
func (c *corpus) load(f corpusFile) (*parsedPage, Entry, error) {
	src, err := os.ReadFile(filepath.Join(c.v.Root, filepath.FromSlash(f.rel)))
	if err != nil {
		return nil, Entry{}, err
	}
	summary := c.summaries[f.slug()]
	sum := sha256.Sum256(src)
	e := Entry{Size: f.size, MtimeNS: f.mtimeNS, SHA256: hex.EncodeToString(sum[:]), Summary: summary}
	return parsePage(f.rel, src, summary, c.v.Config.Timeline), e, nil
}

// buildMemory indexes the whole corpus into an in-memory scorch index (the
// same engine and scoring as the cached one).
func buildMemory(corp *corpus, im *mapping.IndexMappingImpl) (bleve.Index, error) {
	idx, err := bleve.NewUsing("", im, scorch.Name, scorch.Name, nil)
	if err != nil {
		return nil, err
	}
	if err := indexAll(idx, corp, map[string]Entry{}); err != nil {
		idx.Close()
		return nil, err
	}
	return idx, nil
}

// Run executes q against the vault (DESIGN.md §19).
func Run(v *vault.Vault, q *Query, opts Options) (*Response, error) {
	analyzers := v.Config.Search.Analyzers
	im, err := newMapping(analyzers)
	if err != nil {
		return nil, fmt.Errorf("search.analyzers: %w", err)
	}
	rawAn := im.AnalyzerNamed(rawAnalyzer)
	q.resolve(rawAn)

	corp, err := loadCorpus(v)
	if err != nil {
		return nil, err
	}

	resp := &Response{}
	var idx bleve.Index
	if opts.NoCache {
		idx, err = buildMemory(corp, im)
		if err != nil {
			return nil, err
		}
		defer idx.Close()
	} else {
		ci, cerr := openCached(v, corp, im, opts.Rebuild)
		if cerr == nil {
			sr, serr := execute(ci.idx, corp, q, opts, analyzers)
			if serr == nil {
				ci.close()
				return finish(resp, sr, corp, q, opts, im, analyzers)
			}
			ci.discard()
			cerr = fmt.Errorf("cached index query failed: %v", serr)
		}
		resp.CacheNote = fmt.Sprintf("search cache unavailable (%v), indexing in memory", cerr)
		idx, err = buildMemory(corp, im)
		if err != nil {
			return nil, err
		}
		defer idx.Close()
	}

	sr, err := execute(idx, corp, q, opts, analyzers)
	if err != nil {
		return nil, err
	}
	return finish(resp, sr, corp, q, opts, im, analyzers)
}

func execute(idx bleve.Index, corp *corpus, q *Query, opts Options, analyzers []string) (res *bleve.SearchResult, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("search panic: %v", r)
		}
	}()
	groups := contentGroups
	if opts.Timeline {
		groups = allGroups
	}
	patterns := vault.OnlyPatterns(opts.Only)
	var ids []string
	if len(patterns) > 0 {
		rels := make([]string, len(corp.files))
		for i, f := range corp.files {
			rels[i] = f.rel
		}
		ids = vault.FilterOnly(rels, patterns)
		if ids == nil {
			ids = []string{}
		}
	}
	bq := q.build(groups, analyzers, ids, len(patterns) > 0, opts.Where)

	size := opts.Limit
	if size <= 0 {
		size = len(corp.files) + 1
	}
	req := bleve.NewSearchRequestOptions(bq, size, 0, false)
	req.Fields = []string{fieldType, fieldTitle, fieldSummary}
	req.SortBy([]string{"-_score", "_id"})
	req.AddFacet(fieldType, bleve.NewFacetRequest(fieldType, len(corp.files)+1))
	return idx.Search(req)
}

func finish(resp *Response, sr *bleve.SearchResult, corp *corpus, q *Query, opts Options, im *mapping.IndexMappingImpl, analyzers []string) (*Response, error) {
	resp.Total = int(sr.Total)
	resp.TypeCounts = map[string]int{}
	if fr, ok := sr.Facets[fieldType]; ok && fr != nil {
		if fr.Terms != nil {
			for _, t := range fr.Terms.Terms() {
				resp.TypeCounts[t.Term] = t.Count
			}
		}
		if fr.Missing > 0 {
			resp.TypeCounts[NoneType] = fr.Missing
		}
	}

	var hl *highlighter
	if q.Positive() {
		var ans []analysis.Analyzer
		for _, a := range analyzers {
			ans = append(ans, im.AnalyzerNamed(a))
		}
		hl = newHighlighter(ans, im.AnalyzerNamed(rawAnalyzer), q.Clauses)
	}

	resp.Results = []Result{}
	for _, h := range sr.Hits {
		r := Result{Path: h.ID, Snippets: []Snippet{}}
		r.Type, _ = h.Fields[fieldType].(string)
		r.Title, _ = h.Fields[fieldTitle].(string)
		r.Summary, _ = h.Fields[fieldSummary].(string)
		if q.Positive() {
			r.Score = math.Round(h.Score*100) / 100
			if src, err := os.ReadFile(filepath.Join(corp.v.Root, filepath.FromSlash(h.ID))); err == nil {
				pg := parsePage(h.ID, src, r.Summary, corp.v.Config.Timeline)
				r.Snippets = hl.snippets(pg, opts.Timeline)
			}
		}
		resp.Results = append(resp.Results, r)
	}
	if !q.Positive() {
		// Filter-only: every score is equal; path order (DESIGN.md §19.4).
		sort.Slice(resp.Results, func(i, j int) bool { return resp.Results[i].Path < resp.Results[j].Path })
	}
	return resp, nil
}

// SortedTypeCounts returns the facet as "type count" pairs, count desc then
// name asc.
func SortedTypeCounts(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if m[keys[i]] != m[keys[j]] {
			return m[keys[i]] > m[keys[j]]
		}
		return keys[i] < keys[j]
	})
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = fmt.Sprintf("%s %d", k, m[k])
	}
	return out
}
