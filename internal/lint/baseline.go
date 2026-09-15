package lint

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/toppynl/vaulty/internal/config"
	"github.com/toppynl/vaulty/internal/diag"
	"github.com/toppynl/vaulty/internal/vault"
)

// ratchetDisabled reports whether a `lint.overrides` entry (DESIGN.md
// §6.1a "per-path overrides") switches the ratchet off for code on path:
// matching path against every override, in order, the last explicit
// `ratchet.<code>` setting wins. No matching override, or none mentioning
// code, leaves the ratchet on (the default) — matching config.Override's
// own doc comment.
func ratchetDisabled(overrides []config.Override, path string, code diag.Code) bool {
	disabled := false
	for _, ov := range overrides {
		if !vault.MatchAny(ov.Paths, path) {
			continue
		}
		if v, ok := ov.Ratchet[string(code)]; ok {
			disabled = !v
		}
	}
	return disabled
}

// PageBaseline is the accepted (ratcheted) debt for one page (DESIGN.md
// §6.1a): the TL006/TL008 finding counts and the PG002 token count last
// accepted for this path. A page absent from Baseline.Pages has an implicit
// zero baseline: any TL006/TL008 finding, or any PG002 finding at all, on
// such a page is a brand-new violation and is always an error.
type PageBaseline struct {
	TL006       int `json:"tl006"`
	TL008       int `json:"tl008"`
	PG002Tokens int `json:"pg002_tokens"`
}

// Baseline is the on-disk ratchet file (DESIGN.md §6.1a). A nil *Baseline
// means the file does not exist: ratchet is inactive and severities are
// exactly the configured defaults (pre-U8 behavior).
type Baseline struct {
	Pages map[string]PageBaseline `json:"pages"`
}

// LoadBaseline reads path. A missing file is not an error: it returns
// (nil, nil), meaning "ratchet inactive".
func LoadBaseline(path string) (*Baseline, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out Baseline
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if out.Pages == nil {
		out.Pages = map[string]PageBaseline{}
	}
	return &out, nil
}

// SaveBaseline writes b to path as indented JSON (stable key order — Go
// sorts map[string]... keys when marshaling).
func SaveBaseline(path string, b *Baseline) error {
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

// BuildBaseline recomputes the current ratchet baseline over files (vault
// mode: DESIGN.md §6.1a "write/update baseline"). Recomputing always
// reflects the current state exactly, so it lets a shrink tighten the
// baseline; it also lets a deliberate growth loosen it — that's a human
// review decision (the diff of the baseline file), not something lint
// enforces. Only pages carrying at least one TL006/TL008 finding or a
// PG002-over-max finding get an entry; a page with none is safely
// represented by its absence (an absent page's implicit zero baseline
// never trips, since 0 findings > 0 baseline is false).
func BuildBaseline(v *vault.Vault, files []string) (*Baseline, error) {
	b := &Baseline{Pages: map[string]PageBaseline{}}
	pc := v.Config.Lint.PageChecks

	for _, rel := range files {
		p, err := parseFile(v, rel)
		if err != nil {
			return nil, err
		}
		pageChecksApply := vault.MatchAny(pc.Paths, rel)
		n006, n008, tokens := pageDebtCounts(p, pageChecksApply, pc, v.Config.Lint.Overrides)
		if n006 > 0 || n008 > 0 || tokens > 0 {
			b.Pages[rel] = PageBaseline{TL006: n006, TL008: n008, PG002Tokens: tokens}
		}
	}
	return b, nil
}

// GrowthRefusal records one page/code where a freshly recomputed baseline
// value exceeded the previously accepted one during --write-baseline
// (DESIGN.md §6.1a). Default mode keeps Old (growth refused); --accept-growth
// keeps New (growth accepted) — either way it is reported so a human sees it.
type GrowthRefusal struct {
	Path string
	Code diag.Code // TL006EntryFormat, TL008PartialDate or PG002CompiledTruthSize
	Old  int
	New  int
}

// ExceedsBaseline reports whether disk has, for any page/code, a value
// strictly greater than head's — i.e. whether the on-disk baseline file was
// hand-edited upward relative to head. Used by `lint --write-baseline`
// (DESIGN.md §6.1a) to refuse computing growth against a tampered file when
// head (the committed baseline at HEAD) is available. Returns the first
// offending path/code/values found, in sorted path order, for the error
// message.
func ExceedsBaseline(disk, head *Baseline) (exceeds bool, path string, code diag.Code, diskVal, headVal int) {
	paths := make([]string, 0, len(disk.Pages))
	for p := range disk.Pages {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	for _, p := range paths {
		d := disk.Pages[p]
		h := head.Pages[p] // zero value if absent from head
		if d.TL006 > h.TL006 {
			return true, p, diag.TL006EntryFormat, d.TL006, h.TL006
		}
		if d.TL008 > h.TL008 {
			return true, p, diag.TL008PartialDate, d.TL008, h.TL008
		}
		if d.PG002Tokens > h.PG002Tokens {
			return true, p, diag.PG002CompiledTruthSize, d.PG002Tokens, h.PG002Tokens
		}
	}
	return false, "", "", 0, 0
}

// MergeBaseline reconciles a freshly recomputed baseline (fresh) against the
// previously written one (old) for `lint --write-baseline` (DESIGN.md §6.1a,
// Peep's 2026-09-15 decision on `--write-baseline` growth). old == nil means
// no baseline file exists yet — first creation always records the current
// state outright, growth included, so fresh is returned unchanged with no
// growth reports regardless of acceptGrowth.
//
// Otherwise, per page and per code independently: a fresh value <= the old
// one is applied (shrinking the baseline is free, never gated); a fresh
// value greater than the old one is growth — by default it is refused (the
// old value is kept) and reported; with acceptGrowth it is applied (the new,
// higher value is kept) and still reported, so a human always sees what grew
// even when they explicitly accepted it. A page that ends up with an
// all-zero merged entry (every code shrank to zero) is dropped, matching
// BuildBaseline's "only pages with actual debt get an entry" rule.
func MergeBaseline(old, fresh *Baseline, acceptGrowth bool) (*Baseline, []GrowthRefusal) {
	if old == nil {
		return fresh, nil
	}

	merged := &Baseline{Pages: map[string]PageBaseline{}}
	var growth []GrowthRefusal

	paths := make(map[string]bool, len(old.Pages)+len(fresh.Pages))
	for p := range old.Pages {
		paths[p] = true
	}
	for p := range fresh.Pages {
		paths[p] = true
	}

	apply := func(path string, code diag.Code, oldVal, newVal int) int {
		if newVal <= oldVal {
			return newVal
		}
		growth = append(growth, GrowthRefusal{Path: path, Code: code, Old: oldVal, New: newVal})
		if acceptGrowth {
			return newVal
		}
		return oldVal
	}

	for path := range paths {
		o := old.Pages[path]   // zero value if absent (implicit {0,0,0})
		f := fresh.Pages[path] // zero value if absent (page improved to zero)

		m := PageBaseline{
			TL006:       apply(path, diag.TL006EntryFormat, o.TL006, f.TL006),
			TL008:       apply(path, diag.TL008PartialDate, o.TL008, f.TL008),
			PG002Tokens: apply(path, diag.PG002CompiledTruthSize, o.PG002Tokens, f.PG002Tokens),
		}
		if m.TL006 > 0 || m.TL008 > 0 || m.PG002Tokens > 0 {
			merged.Pages[path] = m
		}
	}

	sort.Slice(growth, func(i, j int) bool {
		if growth[i].Path != growth[j].Path {
			return growth[i].Path < growth[j].Path
		}
		return growth[i].Code < growth[j].Code
	})

	return merged, growth
}

// applyRatchet upgrades TL006/TL008/PG002 findings to error when the page's
// current count/tokens exceed the baseline (DESIGN.md §6.1a). baseline == nil
// leaves diags untouched (ratchet inactive). It is a no-op for every other
// code.
func applyRatchet(diags []diag.Diag, path string, baseline *Baseline, overrides []config.Override) []diag.Diag {
	if baseline == nil {
		return diags
	}
	bp := baseline.Pages[path] // zero value if the page has no entry

	var n006, n008 int
	for _, d := range diags {
		switch d.Code {
		case diag.TL006EntryFormat:
			n006++
		case diag.TL008PartialDate:
			n008++
		}
	}
	// PG002 has at most one finding per page and its token count isn't on
	// the Diag; the caller (checkPageHygiene) applies the PG002 ratchet
	// directly via pg002Severity, where the token count is already in hand.

	grew006 := n006 > bp.TL006 && !ratchetDisabled(overrides, path, diag.TL006EntryFormat)
	grew008 := n008 > bp.TL008 && !ratchetDisabled(overrides, path, diag.TL008PartialDate)
	if !grew006 && !grew008 {
		return diags
	}
	for i := range diags {
		switch diags[i].Code {
		case diag.TL006EntryFormat:
			if grew006 {
				diags[i].Severity = diag.Error
			}
		case diag.TL008PartialDate:
			if grew008 {
				diags[i].Severity = diag.Error
			}
		}
	}
	return diags
}

// pg002Severity implements the PG002 ratchet (DESIGN.md §6.1a): error when
// there is no baseline (ratchet inactive, or the page has no entry — a
// brand-new violation), or when tokens grew past the baselined value;
// warning when tokens are at or below the accepted baseline (a legacy
// oversized page that isn't growing further).
func pg002Severity(path string, tokens int, baseline *Baseline) diag.Severity {
	if baseline == nil {
		return diag.Error
	}
	bp, ok := baseline.Pages[path]
	if !ok || tokens > bp.PG002Tokens {
		return diag.Error
	}
	return diag.Warning
}
