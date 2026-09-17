// Package search implements `vaulty search` (DESIGN.md §19): full-text,
// BM25-ranked content search over the vault backed by a persisted bleve
// index that is kept fresh incrementally, with an in-memory fallback.
package search

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/analysis/analyzer/custom"
	"github.com/blevesearch/bleve/v2/analysis/analyzer/keyword"
	_ "github.com/blevesearch/bleve/v2/analysis/analyzer/simple"
	_ "github.com/blevesearch/bleve/v2/analysis/analyzer/standard"
	_ "github.com/blevesearch/bleve/v2/analysis/lang/ar"
	_ "github.com/blevesearch/bleve/v2/analysis/lang/cjk"
	_ "github.com/blevesearch/bleve/v2/analysis/lang/ckb"
	_ "github.com/blevesearch/bleve/v2/analysis/lang/da"
	_ "github.com/blevesearch/bleve/v2/analysis/lang/de"
	_ "github.com/blevesearch/bleve/v2/analysis/lang/en"
	_ "github.com/blevesearch/bleve/v2/analysis/lang/es"
	_ "github.com/blevesearch/bleve/v2/analysis/lang/fa"
	_ "github.com/blevesearch/bleve/v2/analysis/lang/fi"
	_ "github.com/blevesearch/bleve/v2/analysis/lang/fr"
	_ "github.com/blevesearch/bleve/v2/analysis/lang/hi"
	_ "github.com/blevesearch/bleve/v2/analysis/lang/hr"
	_ "github.com/blevesearch/bleve/v2/analysis/lang/hu"
	_ "github.com/blevesearch/bleve/v2/analysis/lang/it"
	_ "github.com/blevesearch/bleve/v2/analysis/lang/nl"
	_ "github.com/blevesearch/bleve/v2/analysis/lang/no"
	_ "github.com/blevesearch/bleve/v2/analysis/lang/pl"
	_ "github.com/blevesearch/bleve/v2/analysis/lang/pt"
	_ "github.com/blevesearch/bleve/v2/analysis/lang/ro"
	_ "github.com/blevesearch/bleve/v2/analysis/lang/ru"
	_ "github.com/blevesearch/bleve/v2/analysis/lang/sv"
	_ "github.com/blevesearch/bleve/v2/analysis/lang/tr"
	"github.com/blevesearch/bleve/v2/analysis/token/lowercase"
	"github.com/blevesearch/bleve/v2/analysis/tokenizer/unicode"
	"github.com/blevesearch/bleve/v2/mapping"
	index "github.com/blevesearch/bleve_index_api"
)

// FormatVersion is vaulty's own index-format version. Bump it whenever the
// document schema (fields, how they are filled) changes, so every cached
// index is rebuilt instead of being queried with the wrong shape.
//
//	1: initial search index (name/head/tags/body/timeline groups, fm key=value)
//	2: split the name/head groups into their own per-field groups (slug,
//	   title, aliases, h1, index) so each can carry its own configurable
//	   boost (config.Search.Boosts, DESIGN.md §19.2) instead of one boost
//	   shared by every field bundled into "name"/"head".
const FormatVersion = 2

// rawAnalyzer is the unstemmed analyzer every group also gets: unicode word
// tokenizer + lowercase, no stop words, no stemming. Fuzzy, prefix and
// phrase queries run against it.
const rawAnalyzer = "vaulty_raw"

// Field groups. Each group is indexed once per configured analyzer
// ("<group>_<analyzer>") plus once raw ("<group>_raw"). These seven content
// groups are exactly config.SearchFieldGroups, the valid keys for
// config.Search.Boosts (DESIGN.md §19.2); groupTimeline is an eighth group,
// searched only with --timeline, whose boost is fixed (never user-
// configurable — see timelineBoost).
const (
	groupTitle    = "title"
	groupAliases  = "aliases"
	groupSlug     = "slug"
	groupH1       = "h1"
	groupIndex    = "index"
	groupTags     = "tags"     // tags (as text)
	groupBody     = "body"     // compiled truth
	groupTimeline = "timeline" // everything from the Timeline divider on
)

// timelineBoost is groupTimeline's fixed, non-configurable boost (DESIGN.md
// §19.2): it is off by default (searched only with --timeline) and not one
// of the seven fields config.Search.Boosts covers.
const timelineBoost = 1.0

// allGroups is the indexing order; contentGroups are searched by default
// (groupTimeline only with --timeline).
var (
	contentGroups = []string{groupTitle, groupAliases, groupSlug, groupH1, groupIndex, groupTags, groupBody}
	allGroups     = append(append([]string{}, contentGroups...), groupTimeline)
)

// boostsWithTimeline copies cfg (config.Search.Boosts) and adds the fixed
// Timeline boost, for clauseQuery's lookup (query.go).
func boostsWithTimeline(cfg map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(cfg)+1)
	for k, v := range cfg {
		out[k] = v
	}
	out[groupTimeline] = timelineBoost
	return out
}

// Stored/keyword fields. fieldTitleStored/fieldSummaryStored are
// stored-only display copies, distinct from the indexed/boosted groupTitle
// text group (both happen to be about "title", but one is searched text,
// the other a display-only stored value bleve returns verbatim).
const (
	fieldType          = "type"           // keyword, stored, doc values (facets)
	fieldTitleStored   = "title_stored"   // stored only (display)
	fieldSummaryStored = "summary_stored" // stored only (display)
	fieldFM            = "fm"             // keyword terms "key=value", one per frontmatter value
)

func groupField(group, analyzer string) string {
	if analyzer == rawAnalyzer {
		return group + "_raw"
	}
	return group + "_" + analyzer
}

// newMapping builds the index mapping for the given analyzers.
func newMapping(analyzers []string) (*mapping.IndexMappingImpl, error) {
	im := bleve.NewIndexMapping()
	im.ScoringModel = index.BM25Scoring
	im.DefaultAnalyzer = rawAnalyzer
	im.StoreDynamic = false
	im.IndexDynamic = false
	im.DocValuesDynamic = false
	if err := im.AddCustomAnalyzer(rawAnalyzer, map[string]any{
		"type":          custom.Name,
		"tokenizer":     unicode.Name,
		"token_filters": []any{lowercase.Name},
	}); err != nil {
		return nil, err
	}

	dm := bleve.NewDocumentStaticMapping()
	for _, g := range allGroups {
		var fms []*mapping.FieldMapping
		for _, a := range analyzers {
			fms = append(fms, textField(groupField(g, a), a, false))
		}
		// term vectors on raw only: phrase queries need positions.
		fms = append(fms, textField(groupField(g, rawAnalyzer), rawAnalyzer, true))
		dm.AddFieldMappingsAt(g, fms...)
	}

	typ := bleve.NewTextFieldMapping()
	typ.Analyzer = keyword.Name
	typ.Store = true
	typ.DocValues = true
	typ.IncludeInAll = false
	typ.IncludeTermVectors = false
	dm.AddFieldMappingsAt(fieldType, typ)

	for _, f := range []string{fieldTitleStored, fieldSummaryStored} {
		fm := bleve.NewTextFieldMapping()
		fm.Index = false
		fm.Store = true
		fm.DocValues = false
		fm.IncludeInAll = false
		fm.IncludeTermVectors = false
		dm.AddFieldMappingsAt(f, fm)
	}

	fmKV := bleve.NewTextFieldMapping()
	fmKV.Analyzer = keyword.Name
	fmKV.Store = false
	fmKV.DocValues = false
	fmKV.IncludeInAll = false
	fmKV.IncludeTermVectors = false
	dm.AddFieldMappingsAt(fieldFM, fmKV)

	im.DefaultMapping = dm
	if err := im.Validate(); err != nil {
		return nil, err
	}
	return im, nil
}

func textField(name, analyzer string, termVectors bool) *mapping.FieldMapping {
	fm := bleve.NewTextFieldMapping()
	fm.Name = name
	fm.Analyzer = analyzer
	fm.Store = false
	fm.DocValues = false
	fm.IncludeInAll = false
	fm.IncludeTermVectors = termVectors
	return fm
}

// mappingHash fingerprints the mapping so an analyzer or field change
// forces a full rebuild of a cached index.
func mappingHash(im *mapping.IndexMappingImpl) (string, error) {
	b, err := json.Marshal(im)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
