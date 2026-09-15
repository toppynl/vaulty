// Package lint runs Timeline (TL*) and page-hygiene (PG*) checks
// (DESIGN.md §6).
package lint

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/toppynl/vaulty/internal/config"
	"github.com/toppynl/vaulty/internal/diag"
	"github.com/toppynl/vaulty/internal/doc"
	"github.com/toppynl/vaulty/internal/timeline"
	"github.com/toppynl/vaulty/internal/vault"
)

type Mode string

const (
	ModeFiles Mode = "files" // explicit files, --changed, --hook: all findings, PG* are errors
	ModeVault Mode = "vault" // no args / directory args: TL* findings, PG* counted only
)

type Options struct {
	Mode   Mode
	Strict bool // warnings count as errors for the exit code

	// FullVault marks a ModeVault run that covers the entire vault (no
	// directory/path args, no --changed) rather than a subset of it. Only
	// then can a baseline path absent from files be trusted as vanished
	// (deleted/renamed) instead of merely outside the requested subset.
	FullVault bool
}

type Result struct {
	Mode         Mode              `json:"mode"`
	FilesChecked int               `json:"files_checked"`
	Findings     []diag.Diag       `json:"findings"`
	Counts       map[diag.Code]int `json:"counts"` // ModeVault: pages per PG code
	Errors       int               `json:"errors"`
	Warnings     int               `json:"warnings"`

	// StaleBaseline counts pages (ModeVault only) whose current TL006/TL008/
	// PG002 debt is strictly below their baseline entry — the ratchet's
	// shrink-gap (DESIGN.md §6.1a): shrinking is free and never enforced, so
	// nothing forces a re-run of --write-baseline to tighten it back up.
	// Zero when the baseline file does not exist (BaselineActive false).
	StaleBaseline  int  `json:"stale_baseline"`
	BaselineActive bool `json:"-"`

	// VanishedBaseline lists baseline paths (sorted) that carry debt but no
	// longer exist in the current vault walk — deleted, or renamed to a
	// path lint never visited. Each one is also counted in StaleBaseline:
	// a page can only get more stale by disappearing, never less, since
	// its "current" debt is implicitly zero and its baseline entry is
	// nonzero by construction (DESIGN.md §6.1a, BuildBaseline). Distinct
	// from an ordinary stale page (still on disk, just improved) because
	// there is no current page to point an agent at — only
	// --write-baseline (which drops it) or a manual rename fixes it.
	VanishedBaseline []string `json:"vanished_baseline,omitempty"`
}

// CheckPage returns all findings for one parsed page. pageChecks enables
// PG001/PG002 (path matched lint.page_checks.paths). baseline may be nil
// (ratchet inactive, DESIGN.md §6.1a).
func CheckPage(p *timeline.Page, v *vault.Vault, pageChecks bool, baseline *Baseline) []diag.Diag {
	diags := p.AllDiags()
	diags = applyRatchet(diags, p.Doc.Path, baseline, v.Config.Lint.Overrides)
	if pageChecks {
		pg := checkPageHygiene(p, v.Config.Lint.PageChecks, p.Doc.Path, baseline)
		for i := range pg {
			pg[i].Path = p.Doc.Path
		}
		diags = append(diags, pg...)
	}
	sh := checkShardChild(p, v.Config, baseline)
	for i := range sh {
		sh[i].Path = p.Doc.Path
	}
	diags = append(diags, sh...)
	return diags
}

// EstimateTokens is the compiled-truth size heuristic: ceil(bytes/4).
func EstimateTokens(n int) int { return (n + 3) / 4 }

var (
	reChecklist = regexp.MustCompile(`^\s*[-*+] \[[ xX]\]( |$)`)
	reTableRow  = regexp.MustCompile(`^\s*\|.*\|\s*$`)
	reTableSep  = regexp.MustCompile(`^\s*\|?\s*:?-{3,}`)
	reHeading   = regexp.MustCompile(`^#{1,6} (.*)`)
	reFence     = regexp.MustCompile("^(```|~~~)")
)

func checkPageHygiene(p *timeline.Page, pc config.PageChecks, path string, baseline *Baseline) []diag.Diag {
	var diags []diag.Diag
	text := string(p.Doc.Src[p.CompiledTruth.Start:p.CompiledTruth.End])
	lines := strings.Split(text, "\n")
	startLine := p.Doc.LineOf(p.CompiledTruth.Start)

	inFence := false
	for i, line := range lines {
		lineNo := startLine + i
		if reFence.MatchString(strings.TrimRight(line, "\r")) {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}

		if pc.Checklist && reChecklist.MatchString(line) {
			diags = append(diags, diag.Diag{
				Code: diag.PG001WorkMaterial, Severity: diag.Error, Line: lineNo,
				Message: "checklist item above the divider — move to a now/tracking/ work file",
			})
		}
		if reTableRow.MatchString(line) && i+1 < len(lines) && reTableSep.MatchString(lines[i+1]) {
			if tableHasKeywordCell(line, pc.TableKeywords) {
				diags = append(diags, diag.Diag{
					Code: diag.PG001WorkMaterial, Severity: diag.Error, Line: lineNo,
					Message: "unit/step table above the divider — move to a now/tracking/ work file",
				})
			}
		}
		if m := reHeading.FindStringSubmatch(line); m != nil {
			lower := strings.ToLower(m[1])
			for _, kw := range pc.HeadingKeywords {
				if strings.HasPrefix(lower, strings.ToLower(kw)) {
					diags = append(diags, diag.Diag{
						Code: diag.PG001WorkMaterial, Severity: diag.Error, Line: lineNo,
						Message: "heading above the divider — move to a now/tracking/ work file",
					})
					break
				}
			}
		}
	}

	if tokens := EstimateTokens(len(text)); tokens > pc.CompiledTruthMaxTokens {
		diags = append(diags, diag.Diag{
			Code: diag.PG002CompiledTruthSize, Severity: pg002Severity(path, tokens, baseline), Line: startLine,
			Message: fmt.Sprintf("compiled truth ~%d tokens > %d — move history to Timeline, work to a work file, then compress", tokens, pc.CompiledTruthMaxTokens),
		})
	}
	return diags
}

// pageDebtCounts returns the current TL006/TL008 finding counts and the
// PG002 token count (0 when tokens are within budget, or pageChecksApply is
// false) for one already-parsed page. This is the exact triple the ratchet
// baseline stores and compares against (DESIGN.md §6.1a) — BuildBaseline and
// the vault-mode stale count both derive from it, so they can never drift
// apart from what CheckPage/checkPageHygiene actually find.
func pageDebtCounts(p *timeline.Page, pageChecksApply bool, pc config.PageChecks, overrides []config.Override) (n006, n008, tokens int) {
	tl006Off := RatchetDisabled(overrides, p.Doc.Path, diag.TL006EntryFormat)
	tl008Off := RatchetDisabled(overrides, p.Doc.Path, diag.TL008PartialDate)
	for _, d := range p.AllDiags() {
		switch d.Code {
		case diag.TL006EntryFormat:
			if !tl006Off {
				n006++
			}
		case diag.TL008PartialDate:
			if !tl008Off {
				n008++
			}
		}
	}
	if pageChecksApply {
		text := string(p.Doc.Src[p.CompiledTruth.Start:p.CompiledTruth.End])
		if t := EstimateTokens(len(text)); t > pc.CompiledTruthMaxTokens {
			tokens = t
		}
	}
	return
}

func tableHasKeywordCell(headerLine string, keywords []string) bool {
	cell := strings.Trim(headerLine, " \t")
	cell = strings.Trim(cell, "|")
	for _, c := range strings.Split(cell, "|") {
		c = strings.Trim(c, " \t")
		c = strings.ReplaceAll(c, "*", "")
		c = strings.ReplaceAll(c, "`", "")
		c = strings.ToLower(strings.TrimSpace(c))
		fields := strings.Fields(c)
		if len(fields) == 0 {
			continue
		}
		first := fields[0]
		for _, kw := range keywords {
			if first == strings.ToLower(kw) {
				return true
			}
		}
	}
	return false
}

// parseFile reads and parses one vault-relative file.
func parseFile(v *vault.Vault, rel string) (*timeline.Page, error) {
	full := filepath.Join(v.Root, filepath.FromSlash(rel))
	src, err := os.ReadFile(full)
	if err != nil {
		return nil, err
	}
	d := doc.Parse(rel, src)
	return timeline.Parse(d, v.Config.Timeline), nil
}

// Run lints the given vault-relative files in the given mode.
func Run(v *vault.Vault, files []string, opt Options) (*Result, error) {
	res := &Result{Mode: opt.Mode, Counts: map[diag.Code]int{}}
	if opt.Mode == ModeVault {
		// Always present, even at 0 (DESIGN.md §6.3).
		res.Counts[diag.PG001WorkMaterial] = 0
		res.Counts[diag.PG002CompiledTruthSize] = 0
	}

	baselinePath := filepath.Join(v.Root, filepath.FromSlash(v.Config.Lint.BaselinePath))
	baseline, err := LoadBaseline(baselinePath)
	if err != nil {
		return nil, err
	}
	res.BaselineActive = baseline != nil

	seen := map[string]bool{}
	for _, rel := range files {
		seen[rel] = true
	}

	for _, rel := range files {
		p, err := parseFile(v, rel)
		if err != nil {
			return nil, err
		}
		res.FilesChecked++

		pageChecksApply := vault.MatchAny(v.Config.Lint.PageChecks.Paths, rel)

		if opt.Mode == ModeVault && baseline != nil {
			if bp, ok := baseline.Pages[rel]; ok {
				n006, n008, tokens := pageDebtCounts(p, pageChecksApply, v.Config.Lint.PageChecks, v.Config.Lint.Overrides)
				if n006 < bp.TL006 || n008 < bp.TL008 || (pageChecksApply && tokens < bp.PG002Tokens) {
					res.StaleBaseline++
				}
			}
		}

		all := CheckPage(p, v, pageChecksApply, baseline)
		for i := range all {
			all[i].Path = rel
		}
		all = applyOverrides(all, rel, v.Config.Lint.Severity, v.Config.Lint.Overrides)

		hasPG001, hasPG002 := false, false
		for _, dg := range all {
			switch dg.Code {
			case diag.PG001WorkMaterial:
				hasPG001 = true
			case diag.PG002CompiledTruthSize:
				hasPG002 = true
			}
		}
		if opt.Mode == ModeVault {
			if hasPG001 {
				res.Counts[diag.PG001WorkMaterial]++
			}
			if hasPG002 {
				res.Counts[diag.PG002CompiledTruthSize]++
			}
			for _, dg := range all {
				if dg.Code == diag.PG001WorkMaterial || dg.Code == diag.PG002CompiledTruthSize {
					continue
				}
				res.Findings = append(res.Findings, dg)
			}
		} else {
			res.Findings = append(res.Findings, all...)
		}
	}

	shardDiags, err := CheckShardDirs(v, files)
	if err != nil {
		return nil, err
	}
	shardDiags = applyOverridesEach(shardDiags, v.Config.Lint.Severity, v.Config.Lint.Overrides)
	res.Findings = append(res.Findings, shardDiags...)

	if opt.Mode == ModeVault && opt.FullVault && baseline != nil {
		vanished := make([]string, 0, len(baseline.Pages))
		for p := range baseline.Pages {
			if !seen[p] {
				vanished = append(vanished, p)
			}
		}
		sort.Strings(vanished)
		res.VanishedBaseline = vanished
		res.StaleBaseline += len(vanished)
	}

	if res.Findings == nil {
		res.Findings = []diag.Diag{}
	}
	sort.SliceStable(res.Findings, func(i, j int) bool {
		a, b := res.Findings[i], res.Findings[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Code < b.Code
	})

	for _, f := range res.Findings {
		switch f.Severity {
		case diag.Error:
			res.Errors++
		case diag.Warning:
			res.Warnings++
		}
	}
	return res, nil
}

// applyOverrides applies lint.severity: "off" drops the finding, "error"
// and "warning" replace the severity. Blocking is untouched.
// applyOverrides applies the global lint.severity map, then layers
// lint.overrides whose Paths match path on top (DESIGN.md §6.1a
// "per-path overrides"): later-matching overrides win over earlier ones and
// over the global map, exactly like config.Override's own doc comment
// describes for Ratchet.
func applyOverrides(diags []diag.Diag, path string, severity map[string]string, overrides []config.Override) []diag.Diag {
	if len(severity) == 0 && len(overrides) == 0 {
		return diags
	}
	out := diags[:0]
	for _, d := range diags {
		sev, ok := severity[string(d.Code)]
		for _, ov := range overrides {
			if !vault.MatchAny(ov.Paths, path) {
				continue
			}
			if s, has := ov.Severity[string(d.Code)]; has {
				sev, ok = s, true
			}
		}
		if ok {
			switch sev {
			case "off":
				continue
			case "error":
				d.Severity = diag.Error
			case "warning":
				d.Severity = diag.Warning
			}
		}
		out = append(out, d)
	}
	return out
}
