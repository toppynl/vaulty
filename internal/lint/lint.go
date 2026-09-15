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
}

type Result struct {
	Mode         Mode              `json:"mode"`
	FilesChecked int               `json:"files_checked"`
	Findings     []diag.Diag       `json:"findings"`
	Counts       map[diag.Code]int `json:"counts"` // ModeVault: pages per PG code
	Errors       int               `json:"errors"`
	Warnings     int               `json:"warnings"`
}

// CheckPage returns all findings for one parsed page. pageChecks enables
// PG001/PG002 (path matched lint.page_checks.paths).
func CheckPage(p *timeline.Page, v *vault.Vault, pageChecks bool) []diag.Diag {
	diags := p.AllDiags()
	if pageChecks {
		pg := checkPageHygiene(p, v.Config.Lint.PageChecks)
		for i := range pg {
			pg[i].Path = p.Doc.Path
		}
		diags = append(diags, pg...)
	}
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

func checkPageHygiene(p *timeline.Page, pc config.PageChecks) []diag.Diag {
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
			Code: diag.PG002CompiledTruthSize, Severity: diag.Error, Line: startLine,
			Message: fmt.Sprintf("compiled truth ~%d tokens > %d — move history to Timeline, work to a work file, then compress", tokens, pc.CompiledTruthMaxTokens),
		})
	}
	return diags
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

// Run lints the given vault-relative files in the given mode.
func Run(v *vault.Vault, files []string, opt Options) (*Result, error) {
	res := &Result{Mode: opt.Mode, Counts: map[diag.Code]int{}}

	for _, rel := range files {
		full := filepath.Join(v.Root, filepath.FromSlash(rel))
		src, err := os.ReadFile(full)
		if err != nil {
			return nil, err
		}
		res.FilesChecked++

		d := doc.Parse(rel, src)
		p := timeline.Parse(d, v.Config.Timeline)

		pageChecksApply := vault.MatchAny(v.Config.Lint.PageChecks.Paths, rel)
		all := CheckPage(p, v, pageChecksApply)
		for i := range all {
			all[i].Path = rel
		}
		all = applyOverrides(all, v.Config.Lint.Severity)

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
func applyOverrides(diags []diag.Diag, severity map[string]string) []diag.Diag {
	if len(severity) == 0 {
		return diags
	}
	out := diags[:0]
	for _, d := range diags {
		if sev, ok := severity[string(d.Code)]; ok {
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
