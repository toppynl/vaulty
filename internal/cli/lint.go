package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/toppynl/vaulty/internal/diag"
	"github.com/toppynl/vaulty/internal/lint"
	"github.com/toppynl/vaulty/internal/name"
	"github.com/toppynl/vaulty/internal/vault"
)

func (a *app) runTimelineLint(o lintOpts, args []string) error {
	if o.hook {
		return a.runTimelineLintHook()
	}

	v, err := a.openVault()
	if err != nil {
		return err
	}

	if o.writeBaseline {
		if len(args) > 0 || o.changed != "" {
			return &ExitError{Code: ExitUsage, Err: fmt.Errorf("--write-baseline takes no paths/--changed: it always covers the whole vault")}
		}
		return a.runWriteBaseline(v)
	}

	var mode lint.Mode
	var files []string

	switch {
	case o.changed != "":
		mode = lint.ModeFiles
		files, err = changedFiles(v, o.changed)
		if err != nil {
			return &ExitError{Code: ExitUsage, Err: fmt.Errorf("--changed=%s: %w", o.changed, err)}
		}
	case len(args) > 0:
		mode, files, err = resolveLintArgs(v, args)
		if err != nil {
			return &ExitError{Code: ExitUsage, Err: err}
		}
	default:
		mode = lint.ModeVault
		files, err = v.Walk()
		if err != nil {
			return &ExitError{Code: ExitIO, Err: err}
		}
	}

	res, err := lint.Run(v, files, lint.Options{Mode: mode, Strict: o.strict})
	if err != nil {
		return &ExitError{Code: ExitIO, Err: err}
	}

	a.renderLint(res, o.warnings)

	if res.Errors > 0 || (o.strict && res.Warnings > 0) {
		return &ExitError{Code: ExitFindings}
	}
	return nil
}

// runWriteBaseline implements `lint --write-baseline` (DESIGN.md §6.1a):
// recompute the TL006/TL008/PG002 ratchet baseline over the whole vault and
// write it to lint.baseline_path.
func (a *app) runWriteBaseline(v *vault.Vault) error {
	files, err := v.Walk()
	if err != nil {
		return &ExitError{Code: ExitIO, Err: err}
	}
	baseline, err := lint.BuildBaseline(v, files)
	if err != nil {
		return &ExitError{Code: ExitIO, Err: err}
	}
	path := filepath.Join(v.Root, filepath.FromSlash(v.Config.Lint.BaselinePath))
	if err := lint.SaveBaseline(path, baseline); err != nil {
		return &ExitError{Code: ExitIO, Err: err}
	}
	if a.flags.json {
		return a.writeJSON(struct {
			Path  string `json:"path"`
			Pages int    `json:"pages"`
		}{v.Config.Lint.BaselinePath, len(baseline.Pages)})
	}
	fmt.Fprintf(a.stdout, "wrote baseline %s (%d pages)\n", v.Config.Lint.BaselinePath, len(baseline.Pages))
	return nil
}

func (a *app) renderLint(res *lint.Result, showWarnings bool) {
	if a.flags.json {
		_ = a.writeJSON(res)
		return
	}
	for _, f := range res.Findings {
		if f.Severity == diag.Warning && !showWarnings {
			continue
		}
		fmt.Fprintf(a.stdout, "%s:%d: %s %s: %s\n", f.Path, f.Line, f.Code, f.Severity, f.Message)
	}
	if res.Mode == lint.ModeVault {
		fmt.Fprintf(a.stdout, "count PG001 work-material %d\n", res.Counts[diag.PG001WorkMaterial])
		fmt.Fprintf(a.stdout, "count PG002 compiled-truth-size %d\n", res.Counts[diag.PG002CompiledTruthSize])
	}
	fmt.Fprintf(a.stderr, "%s: %d errors, %d warnings in %d files\n", name.Binary, res.Errors, res.Warnings, res.FilesChecked)
}

// resolveLintArg resolves one positional lint argument: cwd-relative first,
// falling back to vault-root-relative (mirrors vault path resolution, §3.3).
func resolveLintArg(v *vault.Vault, a string) (string, os.FileInfo, error) {
	if abs, err := filepath.Abs(a); err == nil {
		if st, err := os.Stat(abs); err == nil {
			return abs, st, nil
		}
	}
	abs := filepath.Join(v.Root, a)
	st, err := os.Stat(abs)
	if err != nil {
		return "", nil, err
	}
	return abs, st, nil
}

// resolveLintArgs implements the "lint DIR..." (vault mode) vs
// "lint FILE..." (files mode) split (DESIGN.md §6.2). All-directories goes
// through vault.Walk; anything else resolves each arg as an explicit file
// (which may lie outside config.dirs).
func resolveLintArgs(v *vault.Vault, args []string) (lint.Mode, []string, error) {
	allDirs := true
	abses := make([]string, len(args))
	for i, a := range args {
		abs, st, err := resolveLintArg(v, a)
		if err != nil {
			return "", nil, err
		}
		abses[i] = abs
		if !st.IsDir() {
			allDirs = false
		}
	}

	if allDirs {
		rels := make([]string, len(abses))
		for i, abs := range abses {
			rel, err := filepath.Rel(v.Root, abs)
			if err != nil {
				return "", nil, err
			}
			rels[i] = filepath.ToSlash(rel)
		}
		files, err := v.Walk(rels...)
		return lint.ModeVault, files, err
	}

	files := make([]string, len(abses))
	for i, abs := range abses {
		rel, err := filepath.Rel(v.Root, abs)
		if err != nil {
			return "", nil, err
		}
		files[i] = filepath.ToSlash(rel)
	}
	sort.Strings(files)
	return lint.ModeFiles, files, nil
}

// changedFiles implements --changed (DESIGN.md §6.2): the union of files
// changed vs ref, staged/unstaged changes vs HEAD, and untracked files,
// kept if .md, under config.dirs, not excluded, and still existing.
func changedFiles(v *vault.Vault, ref string) ([]string, error) {
	run := func(args ...string) ([]string, error) {
		cmd := exec.Command("git", args...)
		cmd.Dir = v.Root
		out, err := cmd.Output()
		if err != nil {
			return nil, err
		}
		var lines []string
		for _, l := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
			if l != "" {
				lines = append(lines, l)
			}
		}
		return lines, nil
	}

	diffRef, err := run("diff", "--name-only", "--diff-filter=AMR", ref+"...HEAD")
	if err != nil {
		return nil, err
	}
	diffHead, err := run("diff", "--name-only", "HEAD")
	if err != nil {
		return nil, err
	}
	untracked, err := run("ls-files", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}

	set := map[string]bool{}
	for _, l := range diffRef {
		set[l] = true
	}
	for _, l := range diffHead {
		set[l] = true
	}
	for _, l := range untracked {
		set[l] = true
	}

	dirGlobs := make([]string, len(v.Config.Dirs))
	for i, d := range v.Config.Dirs {
		dirGlobs[i] = d + "/**"
	}

	var out []string
	for rel := range set {
		rel = filepath.ToSlash(rel)
		if !strings.HasSuffix(rel, ".md") {
			continue
		}
		if !vault.MatchAny(dirGlobs, rel) {
			continue
		}
		if vault.MatchAny(v.Config.Exclude, rel) {
			continue
		}
		if _, err := os.Stat(filepath.Join(v.Root, filepath.FromSlash(rel))); err != nil {
			continue
		}
		out = append(out, rel)
	}
	sort.Strings(out)
	return out, nil
}

// ---- --hook -------------------------------------------------------------

type hookPayload struct {
	ToolInput struct {
		FilePath string `json:"file_path"`
	} `json:"tool_input"`
}

func (a *app) runTimelineLintHook() error {
	data, err := io.ReadAll(a.stdin)
	if err != nil || len(data) == 0 {
		return nil
	}
	var payload hookPayload
	if err := json.Unmarshal(data, &payload); err != nil || payload.ToolInput.FilePath == "" {
		return nil
	}
	filePath := payload.ToolInput.FilePath

	abs, err := filepath.Abs(filePath)
	if err != nil {
		return nil
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil // file does not exist: silent no-op
	}

	v, err := a.openVaultFrom(filepath.Dir(resolved))
	if err != nil {
		fmt.Fprintln(a.stderr, name.Binary+":", err)
		return &ExitError{Code: 1}
	}

	rel, err := filepath.Rel(v.Root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil
	}
	rel = filepath.ToSlash(rel)
	if !strings.HasSuffix(rel, ".md") {
		return nil
	}
	if !vault.MatchAny(v.Config.Lint.HookPaths, rel) {
		return nil
	}

	res, err := lint.Run(v, []string{rel}, lint.Options{Mode: lint.ModeFiles})
	if err != nil {
		fmt.Fprintln(a.stderr, name.Binary+":", err)
		return &ExitError{Code: 1}
	}
	if res.Errors == 0 {
		return nil
	}

	fmt.Fprintf(a.stderr, "%s: %s has %d must-fix findings:\n", name.Binary, rel, res.Errors)
	for _, f := range res.Findings {
		if f.Severity != diag.Error {
			continue
		}
		fmt.Fprintf(a.stderr, "%s:%d: %s %s: %s\n", f.Path, f.Line, f.Code, f.Severity, f.Message)
	}
	fmt.Fprintln(a.stderr, "Fix these before finishing (touched page rule).")
	return &ExitError{Code: ExitHookFindings}
}
