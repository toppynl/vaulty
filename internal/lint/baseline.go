package lint

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/toppynl/vaulty/internal/diag"
	"github.com/toppynl/vaulty/internal/vault"
)

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
		var n006, n008 int
		for _, d := range p.AllDiags() {
			switch d.Code {
			case diag.TL006EntryFormat:
				n006++
			case diag.TL008PartialDate:
				n008++
			}
		}
		tokens := 0
		if vault.MatchAny(pc.Paths, rel) {
			text := string(p.Doc.Src[p.CompiledTruth.Start:p.CompiledTruth.End])
			if t := EstimateTokens(len(text)); t > pc.CompiledTruthMaxTokens {
				tokens = t
			}
		}
		if n006 > 0 || n008 > 0 || tokens > 0 {
			b.Pages[rel] = PageBaseline{TL006: n006, TL008: n008, PG002Tokens: tokens}
		}
	}
	return b, nil
}

// applyRatchet upgrades TL006/TL008/PG002 findings to error when the page's
// current count/tokens exceed the baseline (DESIGN.md §6.1a). baseline == nil
// leaves diags untouched (ratchet inactive). It is a no-op for every other
// code.
func applyRatchet(diags []diag.Diag, path string, baseline *Baseline) []diag.Diag {
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

	grew006 := n006 > bp.TL006
	grew008 := n008 > bp.TL008
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
