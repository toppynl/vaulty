// Package lint runs Timeline (TL*) and page-hygiene (PG*) checks
// (DESIGN.md §6).
package lint

import (
	"errors"

	"github.com/toppynl/vaulty/internal/diag"
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
	// TODO(step 3)
	return nil
}

// Run lints the given vault-relative files in the given mode.
func Run(v *vault.Vault, files []string, opt Options) (*Result, error) {
	// TODO(step 3)
	return nil, errors.New("not implemented")
}

// EstimateTokens is the compiled-truth size heuristic: ceil(bytes/4).
func EstimateTokens(n int) int { return (n + 3) / 4 }
