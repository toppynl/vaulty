// Package config loads the per-vault .vaulty.yml (DESIGN.md §4). Every key is
// optional; absent keys keep the defaults, which match Robin's vault.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/toppynl/vaulty/internal/name"
)

// FileName is looked up at the vault root (derived from name.Binary).
const FileName = name.ConfigFile

// CurrentVersion is the only schema version this binary understands.
const CurrentVersion = 1

type Config struct {
	Version     int         `yaml:"version" json:"version"`
	Dirs        []string    `yaml:"dirs" json:"dirs"`
	Exclude     []string    `yaml:"exclude" json:"exclude"`
	Frontmatter Frontmatter `yaml:"frontmatter" json:"frontmatter"`
	Timeline    Timeline    `yaml:"timeline" json:"timeline"`
	Lint        Lint        `yaml:"lint" json:"lint"`
	Log         Log         `yaml:"log" json:"log"`
	Fields      Fields      `yaml:"fields" json:"fields"`
	Find        Find        `yaml:"find" json:"find"`
	Search      Search      `yaml:"search" json:"search"`
}

// Fields names the frontmatter keys `find` and `search` read a page's type
// and title from (DESIGN.md §4.1, §18, §19). Shared between the two
// commands since both need the same notion of what a page's type/title is
// (--type filtering, the type facet, the title-display fallback). Every
// other frontmatter key `find`/`search` care about (aliases, tags, or any
// vault-specific key) is named directly, per invocation, via `find.fields`
// (below) rather than through a fixed well-known-key layer like this one.
type Fields struct {
	Type  string `yaml:"type" json:"type"`
	Title string `yaml:"title" json:"title"`
}

// Search configures `vaulty search` (DESIGN.md §19).
type Search struct {
	// Analyzers are the language analyzers every text field is indexed
	// with, one sub-field per analyzer (e.g. [nl, en] for a bilingual
	// vault). The unstemmed "raw" sub-field used for fuzzy/prefix/phrase
	// queries always exists on top of these. See SearchAnalyzers for the
	// accepted names.
	Analyzers []string `yaml:"analyzers" json:"analyzers"`

	// Boosts weights each indexed field group at query time (DESIGN.md
	// §19.2); a higher boost ranks a match in that field higher. Keys are
	// the seven content field groups: title, aliases, slug, h1, index,
	// tags, body. This is a query-time weight only — it never changes what
	// gets indexed, so changing it alone never forces a cache rebuild. The
	// Timeline group (searched only with --timeline) is not in this map:
	// its boost is fixed at 1.0, the same as body's default, since it's
	// off by default and not part of the ranked-by-default field set this
	// config knob is about. This map merges onto the default (like
	// lint.severity, §4.1): a partial override only changes the keys given.
	Boosts map[string]float64 `yaml:"boosts" json:"boosts"`

	// FieldAliases maps a query-string field name (DESIGN.md §19.1's
	// `key:value` syntax) onto the frontmatter key it actually filters —
	// e.g. the default `tag: tags` lets `search tag:billing` filter on the
	// `tags` frontmatter key. Merges onto the default the same way Boosts
	// does.
	FieldAliases map[string]string `yaml:"field_aliases" json:"field_aliases"`
}

// SearchFieldGroups are the query-time-boostable content field groups
// (DESIGN.md §19.2) — the valid keys for Search.Boosts.
var SearchFieldGroups = []string{"title", "aliases", "slug", "h1", "index", "tags", "body"}

// SearchAnalyzers are the analyzer names search.analyzers accepts: bleve's
// language-neutral "standard" (unicode words, lowercased, English stop
// words, no stemming) and "simple" (letters, lowercased), plus bleve's
// stemming language analyzers by language code.
var SearchAnalyzers = []string{
	"standard", "simple",
	"ar", "cjk", "ckb", "da", "de", "en", "es", "fa", "fi", "fr", "hi", "hr",
	"hu", "it", "nl", "no", "pl", "pt", "ro", "ru", "sv", "tr",
}

// Find configures `vaulty find` (DESIGN.md §18).
type Find struct {
	// Index is the vault-relative path to the index file `find` reads
	// summaries from ("- [[name]] — summary" lines, optionally dated). A
	// missing file is skipped silently.
	Index string `yaml:"index" json:"index"`

	// Fields lists the scoring sources, in the order ties break (DESIGN.md
	// §18.2): for one term, the single field with the highest Weight that
	// matches counts; on a weight tie, the earlier entry in this list wins.
	// This slice replaces the default wholesale when given, like every
	// other slice-valued config key (§4.1) — a vault that lists its own
	// `find.fields` opts fully out of the built-in list, rather than
	// appending to it.
	Fields []FindField `yaml:"fields" json:"fields"`
}

// FindField is one `find.fields` entry (DESIGN.md §18.2).
type FindField struct {
	// Source is where the field's value(s) come from: "slug" (the page's
	// filename without .md), "frontmatter" (Key below), "index" (the
	// index.md summary, §18.3), "h1" (the first H1 heading text) or "body"
	// (the compiled-truth text, matched only as a last-resort fallback for
	// a term no other field matched, and only with `find --body` — same as
	// today, regardless of this field's configured Weight). "slug" keeps
	// its exact-match bonus as today: a term whose full token sequence
	// equals the slug's scores Weight; a mere substring/prefix-window match
	// (DESIGN.md §18.1) scores Weight/2. Every other source scores the full
	// Weight on any match, exact or not — this bonus is a fixed property of
	// "slug", not a separately configurable knob.
	Source string `yaml:"source" json:"source"`
	// Key is the frontmatter key to read; required when Source is
	// "frontmatter", read generically (page.FrontmatterValues: a scalar or
	// a list of scalars, stringified; nested maps and non-scalar list
	// elements are skipped). Ignored for every other Source.
	Key string `yaml:"key" json:"key"`
	// Weight is this field's score when a term matches it. Must be > 0.
	Weight int `yaml:"weight" json:"weight"`
	// Match is "token" (tokenize both the term and the value, matching
	// DESIGN.md §18.1's contiguous-prefix-window rule — the default for
	// every built-in field) or "exact" (the whole normalized value must
	// equal the whole normalized term, no tokenizing at all — for values
	// like ids that contain "/" or other punctuation tokenizing would
	// otherwise split on). "exact" normalizes by trimming whitespace and
	// case-folding; it never tokenizes.
	Match string `yaml:"match" json:"match"`
}

// Log configures `vaulty log append|last|lint` (DESIGN.md §17).
type Log struct {
	// Path to the log file, relative to the vault root.
	Path string `yaml:"path" json:"path"`
}

type Frontmatter struct {
	UpdatedKey string `yaml:"updated_key" json:"updated_key"`
}

type Timeline struct {
	Heading  string `yaml:"heading" json:"heading"`     // full heading line, e.g. "## Timeline"
	Divider  string `yaml:"divider" json:"divider"`     // divider line above the heading
	EntryGap string `yaml:"entry_gap" json:"entry_gap"` // "auto" | "0" | "1"
}

type Lint struct {
	HookPaths  []string          `yaml:"hook_paths" json:"hook_paths"`
	PageChecks PageChecks        `yaml:"page_checks" json:"page_checks"`
	Severity   map[string]string `yaml:"severity" json:"severity"` // code -> error|warning|off
	// BaselinePath is where `lint --write-baseline` writes, and where lint
	// reads the ratchet baseline from (DESIGN.md §6.1a). Resolved relative
	// to the vault root. Ratchet is inactive when this file doesn't exist.
	BaselinePath string `yaml:"baseline_path" json:"baseline_path"`
	// Overrides applies severity and/or ratchet exemptions to pages
	// matching Paths, layered on top of Severity/the baseline (DESIGN.md
	// §6.1a "per-path overrides"). Generic — not specific to any one path —
	// so a vault can e.g. keep TL006/TL008 as warnings on its own working
	// layer (now/tracking/**) without ever feeding those findings into the
	// ratchet baseline.
	Overrides []Override `yaml:"overrides" json:"overrides"`
	// Shard configures the SH* hub/child checks (DESIGN.md §16). Empty
	// TypeDirs disables every SH check (no vault uses the sharding
	// convention).
	Shard Shard `yaml:"shard" json:"shard"`
}

// Shard identifies which directories are "type folders" whose immediate
// subdirectories are candidate hub directories, e.g. "wiki/*" matches
// wiki/systems, wiki/vendors, ... so that wiki/systems/<hub>/ is a hub
// directory but wiki/systems/<page>.md (a plain, unsharded page) is not
// mistaken for one. Matching uses the same glob rules as everywhere else
// (vault.MatchAny): a directory D is a hub-directory candidate when
// path.Dir(D) matches one of TypeDirs. This makes "what counts as a shard"
// robust against the vault's own directory conventions (now/tracking/,
// archive/, a vault with no shards at all) instead of hard-coding a fixed
// depth or a single directory name.
type Shard struct {
	TypeDirs []string `yaml:"type_dirs" json:"type_dirs"`
}

// Override is one glob-scoped exemption (DESIGN.md §6.1a). Severity works
// exactly like the top-level Lint.Severity map, but only for paths matching
// Paths. Ratchet, keyed by diag code (e.g. "TL006"), set to false, excludes
// that code entirely from the ratchet for matching paths: BuildBaseline
// never records debt for it there, applyRatchet never promotes it to error
// there, and the vault-mode stale-baseline count never counts it there —
// the code's default severity (warning, unless Severity above overrides it)
// applies unconditionally instead. Absent or true is the default: ratchet
// applies normally.
type Override struct {
	Paths    []string          `yaml:"paths" json:"paths"`
	Severity map[string]string `yaml:"severity" json:"severity"`
	Ratchet  map[string]bool   `yaml:"ratchet" json:"ratchet"`
}

type PageChecks struct {
	Paths                  []string `yaml:"paths" json:"paths"`
	CompiledTruthMaxTokens int      `yaml:"compiled_truth_max_tokens" json:"compiled_truth_max_tokens"`
	Checklist              bool     `yaml:"checklist" json:"checklist"`
	TableKeywords          []string `yaml:"table_keywords" json:"table_keywords"`
	HeadingKeywords        []string `yaml:"heading_keywords" json:"heading_keywords"`
}

// Default returns the built-in configuration (Robin's vault layout).
func Default() *Config {
	return &Config{
		Version:     CurrentVersion,
		Dirs:        []string{"wiki", "me", "now", "archive"},
		Exclude:     []string{},
		Frontmatter: Frontmatter{UpdatedKey: "updated"},
		Timeline:    Timeline{Heading: "## Timeline", Divider: "---", EntryGap: "auto"},
		Lint: Lint{
			HookPaths: []string{"wiki/**"},
			PageChecks: PageChecks{
				Paths:                  []string{"wiki/**"},
				CompiledTruthMaxTokens: 3000,
				Checklist:              true,
				TableKeywords:          []string{"unit", "units", "step", "steps", "stap", "stappen"},
				HeadingKeywords:        []string{"agent log"},
			},
			Severity:     map[string]string{},
			BaselinePath: ".vaulty-baseline.json",
			Shard:        Shard{TypeDirs: []string{"wiki/*"}},
		},
		Log:    Log{Path: "log.md"},
		Fields: Fields{Type: "type", Title: "title"},
		Find: Find{
			Index: "index.md",
			Fields: []FindField{
				{Source: "slug", Weight: 100, Match: "token"},
				{Source: "frontmatter", Key: "title", Weight: 40, Match: "token"},
				{Source: "frontmatter", Key: "aliases", Weight: 40, Match: "token"},
				{Source: "frontmatter", Key: "tags", Weight: 25, Match: "token"},
				{Source: "index", Weight: 20, Match: "token"},
				{Source: "h1", Weight: 20, Match: "token"},
				{Source: "body", Weight: 5, Match: "token"},
			},
		},
		Search: Search{
			Analyzers: []string{"standard"},
			Boosts: map[string]float64{
				"title": 5.0, "aliases": 5.0, "slug": 5.0,
				"h1": 3.0, "index": 3.0,
				"tags": 2.0,
				"body": 1.0,
			},
			FieldAliases: map[string]string{"tag": "tags"},
		},
	}
}

// Load reads path and overlays it on Default(). Unknown keys are an error.
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := Default()
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// Validate checks value ranges. Called by Load; call it on hand-built configs too.
func (c *Config) Validate() error {
	if c.Version != CurrentVersion {
		return fmt.Errorf("unsupported version %d (want %d)", c.Version, CurrentVersion)
	}
	if len(c.Dirs) == 0 {
		return errors.New("dirs must not be empty")
	}
	for _, d := range c.Dirs {
		if !SafeRel(strings.TrimSuffix(d, "/"), false) {
			return fmt.Errorf("dirs: %q must be a relative path inside the vault with no hidden (\".\"-prefixed) segment", d)
		}
	}
	if !strings.HasPrefix(c.Timeline.Heading, "#") {
		return fmt.Errorf("timeline.heading %q must be a markdown heading", c.Timeline.Heading)
	}
	if strings.TrimSpace(c.Timeline.Divider) == "" {
		return errors.New("timeline.divider must not be empty")
	}
	switch c.Timeline.EntryGap {
	case "auto", "0", "1":
	default:
		return fmt.Errorf("timeline.entry_gap %q: want auto, 0 or 1", c.Timeline.EntryGap)
	}
	if c.Lint.PageChecks.CompiledTruthMaxTokens <= 0 {
		return errors.New("lint.page_checks.compiled_truth_max_tokens must be > 0")
	}
	if strings.TrimSpace(c.Lint.BaselinePath) == "" {
		return errors.New("lint.baseline_path must not be empty")
	}
	if strings.TrimSpace(c.Log.Path) == "" {
		return errors.New("log.path must not be empty")
	}
	if strings.TrimSpace(c.Fields.Type) == "" {
		return errors.New("fields.type must not be empty")
	}
	if strings.TrimSpace(c.Fields.Title) == "" {
		return errors.New("fields.title must not be empty")
	}
	if strings.TrimSpace(c.Find.Index) == "" {
		return errors.New("find.index must not be empty")
	}
	for key, p := range map[string]string{"log.path": c.Log.Path, "find.index": c.Find.Index, "lint.baseline_path": c.Lint.BaselinePath} {
		if !SafeRel(p, true) {
			return fmt.Errorf("%s: %q must be a relative path inside the vault with no hidden (\".\"-prefixed) directory", key, p)
		}
	}
	if len(c.Find.Fields) == 0 {
		return errors.New("find.fields must not be empty")
	}
	for i, f := range c.Find.Fields {
		switch f.Source {
		case "slug", "frontmatter", "index", "h1", "body":
		default:
			return fmt.Errorf("find.fields[%d].source %q: want slug, frontmatter, index, h1 or body", i, f.Source)
		}
		if f.Source == "frontmatter" && strings.TrimSpace(f.Key) == "" {
			return fmt.Errorf("find.fields[%d]: key is required when source is frontmatter", i)
		}
		if f.Weight <= 0 {
			return fmt.Errorf("find.fields[%d].weight must be > 0", i)
		}
		switch f.Match {
		case "token", "exact":
		default:
			return fmt.Errorf("find.fields[%d].match %q: want token or exact", i, f.Match)
		}
	}
	if len(c.Search.Analyzers) == 0 {
		return errors.New("search.analyzers must not be empty")
	}
	for k, w := range c.Search.Boosts {
		if !slices.Contains(SearchFieldGroups, k) {
			return fmt.Errorf("search.boosts: unknown field %q (want one of %s)", k, strings.Join(SearchFieldGroups, ", "))
		}
		if w <= 0 {
			return fmt.Errorf("search.boosts.%s must be > 0", k)
		}
	}
	for k, v := range c.Search.FieldAliases {
		if strings.TrimSpace(k) == "" || strings.TrimSpace(v) == "" {
			return errors.New("search.field_aliases: keys and values must not be empty")
		}
	}
	seenAnalyzer := map[string]bool{}
	for _, a := range c.Search.Analyzers {
		if !slices.Contains(SearchAnalyzers, a) {
			return fmt.Errorf("search.analyzers: unknown analyzer %q (want one of %s)", a, strings.Join(SearchAnalyzers, ", "))
		}
		if seenAnalyzer[a] {
			return fmt.Errorf("search.analyzers: %q listed twice", a)
		}
		seenAnalyzer[a] = true
	}
	for code, sev := range c.Lint.Severity {
		switch sev {
		case "error", "warning", "off":
		default:
			return fmt.Errorf("lint.severity.%s %q: want error, warning or off", code, sev)
		}
	}
	for i, ov := range c.Lint.Overrides {
		if len(ov.Paths) == 0 {
			return fmt.Errorf("lint.overrides[%d].paths must not be empty", i)
		}
		for code, sev := range ov.Severity {
			switch sev {
			case "error", "warning", "off":
			default:
				return fmt.Errorf("lint.overrides[%d].severity.%s %q: want error, warning or off", i, code, sev)
			}
		}
	}
	return nil
}

// safeRel mirrors vault.SafeRel (config cannot import vault): a clean,
// relative slash path below the root with no hidden segment; with
// hiddenBase the file name itself may be hidden.
func SafeRel(rel string, hiddenBase bool) bool {
	if rel == "." {
		return !hiddenBase
	}
	if rel == "" || path.IsAbs(rel) || filepath.IsAbs(rel) || strings.Contains(rel, "\\") || path.Clean(rel) != rel {
		return false
	}
	segs := strings.Split(rel, "/")
	for i, seg := range segs {
		if seg == ".." {
			return false
		}
		if strings.HasPrefix(seg, ".") && !(hiddenBase && i == len(segs)-1) {
			return false
		}
	}
	return true
}
