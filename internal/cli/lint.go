package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/toppynl/vaulty/internal/config"
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
		if o.acceptGrowth && !stdinIsTTY(a.stdin) {
			return &ExitError{Code: ExitUsage, Err: fmt.Errorf("--accept-growth requires an interactive terminal on stdin: it is a human decision, never run it from a script or agent")}
		}
		return a.runWriteBaseline(v, o.acceptGrowth)
	}
	if o.acceptGrowth {
		return &ExitError{Code: ExitUsage, Err: fmt.Errorf("--accept-growth only applies with --write-baseline")}
	}

	var mode lint.Mode
	var files []string
	fullVault := false

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
		fullVault = true
		files, err = v.Walk()
		if err != nil {
			return &ExitError{Code: ExitIO, Err: err}
		}
	}

	res, err := lint.Run(v, files, lint.Options{Mode: mode, Strict: o.strict, FullVault: fullVault})
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
// recompute the TL006/TL008/PG002 ratchet baseline over the whole vault.
//
// When no baseline file exists yet, this is first creation: the current
// state is written outright, growth included. Once a baseline exists,
// writing is shrink-only by default — a page whose debt grew keeps its old,
// lower value (growth refused), reported on stderr, and the command exits
// nonzero so an agent blocked by the ratchet cannot silently paper over it
// via `--write-baseline`. `--accept-growth` is the explicit, human-only
// escape hatch: it applies the growth instead of refusing it, but still
// reports every increase on stderr for visibility.
func (a *app) runWriteBaseline(v *vault.Vault, acceptGrowth bool) error {
	files, err := v.Walk()
	if err != nil {
		return &ExitError{Code: ExitIO, Err: err}
	}
	fresh, err := lint.BuildBaseline(v, files)
	if err != nil {
		return &ExitError{Code: ExitIO, Err: err}
	}

	path := filepath.Join(v.Root, filepath.FromSlash(v.Config.Lint.BaselinePath))
	old, err := resolveWriteBaselineOld(v, path)
	if err != nil {
		return &ExitError{Code: ExitIO, Err: err}
	}

	reportOverrideFilteredEntries(a, old, v.Config.Lint.Overrides)
	reportUnmatchedOverrideGlobs(a, files, v.Config.Lint.Overrides)

	merged, growth := lint.MergeBaseline(old, fresh, acceptGrowth)

	if err := lint.SaveBaseline(path, merged); err != nil {
		return &ExitError{Code: ExitIO, Err: err}
	}

	for _, g := range growth {
		if acceptGrowth {
			fmt.Fprintf(a.stderr, "%s: %s %s grew %d -> %d: accepted\n", name.Binary, g.Path, g.Code, g.Old, g.New)
		} else {
			fmt.Fprintf(a.stderr, "%s: %s %s grew %d -> %d: refused, kept at %d\n", name.Binary, g.Path, g.Code, g.Old, g.New, g.Old)
		}
	}

	if a.flags.json {
		type growthJSON struct {
			Path     string    `json:"path"`
			Code     diag.Code `json:"code"`
			Old      int       `json:"old"`
			New      int       `json:"new"`
			Accepted bool      `json:"accepted"`
		}
		out := make([]growthJSON, len(growth))
		for i, g := range growth {
			out[i] = growthJSON{Path: g.Path, Code: g.Code, Old: g.Old, New: g.New, Accepted: acceptGrowth}
		}
		if err := a.writeJSON(struct {
			Path   string       `json:"path"`
			Pages  int          `json:"pages"`
			Growth []growthJSON `json:"growth"`
		}{v.Config.Lint.BaselinePath, len(merged.Pages), out}); err != nil {
			return &ExitError{Code: ExitIO, Err: err}
		}
	} else {
		fmt.Fprintf(a.stdout, "wrote baseline %s (%d pages)\n", v.Config.Lint.BaselinePath, len(merged.Pages))
	}

	if !acceptGrowth && len(growth) > 0 {
		return &ExitError{Code: ExitFindings}
	}
	return nil
}

// reportOverrideFilteredEntries warns on stderr when a page's baseline
// entry would silently vanish on --write-baseline not because the page
// actually improved, but because `lint.overrides` now disables the ratchet
// for that path/code (DESIGN.md §4.1, §6.1a): BuildBaseline excludes
// ratchet-disabled findings from its counts entirely, so an old baseline
// value that a fresh override now exempts looks exactly like an ordinary
// shrink to MergeBaseline and disappears without a trace. old == nil (no
// baseline yet) has nothing to compare against.
func reportOverrideFilteredEntries(a *app, old *lint.Baseline, overrides []config.Override) {
	if old == nil || len(overrides) == 0 {
		return
	}
	paths := make([]string, 0, len(old.Pages))
	for p := range old.Pages {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	codes := []struct {
		code diag.Code
		val  func(lint.PageBaseline) int
	}{
		{diag.TL006EntryFormat, func(b lint.PageBaseline) int { return b.TL006 }},
		{diag.TL008PartialDate, func(b lint.PageBaseline) int { return b.TL008 }},
		{diag.PG002CompiledTruthSize, func(b lint.PageBaseline) int { return b.PG002Tokens }},
	}
	for _, p := range paths {
		entry := old.Pages[p]
		for _, c := range codes {
			v := c.val(entry)
			if v > 0 && lint.RatchetDisabled(overrides, p, c.code) {
				fmt.Fprintf(a.stderr, "%s: %s %s: lint.overrides disabled the ratchet for this path — baseline entry (was %d) dropped, not a shrink\n", name.Binary, p, c.code, v)
			}
		}
	}
}

// reportUnmatchedOverrideGlobs warns on stderr about a `lint.overrides`
// entry whose Paths glob matches nothing anywhere in the vault (files,
// from the full Walk()) — almost always a typo'd path or a rename left
// behind in config, silently exempting nothing instead of the intended
// pages.
func reportUnmatchedOverrideGlobs(a *app, files []string, overrides []config.Override) {
	for i, ov := range overrides {
		matched := false
		for _, f := range files {
			if vault.MatchAny(ov.Paths, f) {
				matched = true
				break
			}
		}
		if !matched {
			fmt.Fprintf(a.stderr, "%s: lint.overrides[%d] paths %v match no files in the vault\n", name.Binary, i, ov.Paths)
		}
	}
}

// resolveWriteBaselineOld resolves the "old" baseline that --write-baseline
// diffs the freshly recomputed one against (DESIGN.md §6.1a, Peep's
// 2026-09-15 decision). Inside a git repo, "old" is always the baseline as
// committed at HEAD, never the on-disk file: that closes two bypasses of
// the shrink-only ratchet — `rm .vaulty-baseline.json && vaulty timeline
// lint --write-baseline` (no old on disk, but one exists at HEAD) and
// hand-raising a value on disk before running --write-baseline (the disk
// file is never consulted as the growth reference). A baseline file that
// exists at HEAD but is missing on disk, or that is higher on disk than at
// HEAD, is refused outright rather than silently reconciled — those are
// signs of tampering or an incomplete checkout, not a bootstrap. A real
// bootstrap (no baseline at HEAD and none on disk) still returns nil,
// meaning first creation, growth included, exactly as before. Outside a
// git repo, the on-disk file is used directly: current (pre-U8) behavior.
func resolveWriteBaselineOld(v *vault.Vault, path string) (*lint.Baseline, error) {
	if !isGitRepo(v.Root) {
		return lint.LoadBaseline(path)
	}

	relPath, err := filepath.Rel(v.Root, path)
	if err != nil {
		return nil, err
	}
	relPath = filepath.ToSlash(relPath)

	disk, err := lint.LoadBaseline(path)
	if err != nil {
		return nil, err
	}

	headData, headErr := gitShowAtHEAD(v.Root, relPath)
	if headErr != nil {
		if errors.Is(headErr, errGitPathNotAtHEAD) {
			// Not tracked at HEAD (or no HEAD commit yet): nothing
			// committed to diff against. If a file already exists on disk,
			// treat it as the old baseline (matches non-git behavior);
			// otherwise this is a real bootstrap.
			return disk, nil
		}
		// Any other git failure (e.g. the vault living in a subdirectory
		// of the repo and the path being resolved wrong) must never be
		// treated as "nothing at HEAD" — that would silently reopen the
		// bypass §6.1a closes: `rm .vaulty-baseline.json &&
		// --write-baseline` would then look like a bootstrap and accept
		// any growth. Refuse instead of guessing.
		return nil, fmt.Errorf("checking %s at HEAD: %w", v.Config.Lint.BaselinePath, headErr)
	}

	var head lint.Baseline
	if err := json.Unmarshal(headData, &head); err != nil {
		return nil, fmt.Errorf("%s at HEAD: %w", v.Config.Lint.BaselinePath, err)
	}
	if head.Pages == nil {
		head.Pages = map[string]lint.PageBaseline{}
	}

	if disk == nil {
		return nil, fmt.Errorf("%s exists at HEAD but is missing on disk: restore it (e.g. `git checkout HEAD -- %s`) before writing a fresh baseline", v.Config.Lint.BaselinePath, v.Config.Lint.BaselinePath)
	}

	if exceeds, p, code, diskVal, headVal := lint.ExceedsBaseline(disk, &head); exceeds {
		return nil, fmt.Errorf("%s on disk has %s %s=%d, higher than %d committed at HEAD: revert manual edits to the baseline before writing it", v.Config.Lint.BaselinePath, p, code, diskVal, headVal)
	}

	return &head, nil
}

// isGitRepo reports whether root is inside a git working tree.
func isGitRepo(root string) bool {
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	cmd.Dir = root
	out, err := cmd.Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

// errGitPathNotAtHEAD marks a gitShowAtHEAD failure that means "nothing
// committed to diff against" (no HEAD commit yet, or relPath not tracked
// at HEAD) — the only case resolveWriteBaselineOld may fall back from.
// Any other git failure is returned unwrapped and must be refused, not
// silently treated as a bootstrap.
var errGitPathNotAtHEAD = errors.New("path not present at HEAD")

// gitShowAtHEAD returns the content of relPath (relative to root, the
// vault root) as committed at HEAD. It runs with `-C root` and a
// cwd-relative pathspec (`HEAD:./relPath`) rather than plain
// `HEAD:relPath` run with cmd.Dir=root: a bare `HEAD:<path>` is always
// resolved relative to the *repo's* top level, so when the vault is a
// submodule/subdirectory of a larger repo, `HEAD:relPath` silently means
// a different (usually nonexistent) path. `./`-prefixing makes git resolve
// it relative to `-C`'s directory instead, matching what "relPath, from
// the vault root" actually means regardless of where the repo root is.
func gitShowAtHEAD(root, relPath string) ([]byte, error) {
	cmd := exec.Command("git", "-C", root, "show", "HEAD:./"+relPath)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := stderr.String()
		if gitShowMeansNotAtHEAD(msg) {
			return nil, errGitPathNotAtHEAD
		}
		return nil, fmt.Errorf("git show HEAD:./%s: %w: %s", relPath, err, strings.TrimSpace(msg))
	}
	return out, nil
}

// gitShowMeansNotAtHEAD reports whether git's stderr for a failed
// `git show HEAD:./path` means "there is genuinely nothing to compare
// against" (path untracked at HEAD, or no HEAD commit yet) rather than
// some other git failure (permission, corruption, wrong cwd, ...) that
// must be refused instead of treated as a bootstrap.
func gitShowMeansNotAtHEAD(stderr string) bool {
	switch {
	case strings.Contains(stderr, "does not exist in"),
		strings.Contains(stderr, "exists on disk, but not in"),
		strings.Contains(stderr, "bad revision 'HEAD'"),
		strings.Contains(stderr, "unknown revision or path not in the working tree"):
		return true
	default:
		return false
	}
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
		if res.BaselineActive {
			fmt.Fprintf(a.stdout, "count baseline-stale %d (pages below baseline; run --write-baseline to tighten)\n", res.StaleBaseline)
			for _, p := range res.VanishedBaseline {
				fmt.Fprintf(a.stdout, "  %s: no longer in the vault (deleted, or renamed and not repointed) — possible rename, or run --write-baseline to drop it\n", p)
			}
		}
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
