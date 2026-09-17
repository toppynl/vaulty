// Package vault finds the vault root, loads its config, resolves page
// arguments and walks content dirs (DESIGN.md §4.2, §3.3).
package vault

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/toppynl/vaulty/internal/config"
	"github.com/toppynl/vaulty/internal/name"
)

// EnvRoot overrides root discovery (below --vault, above auto-discovery).
var EnvRoot = name.EnvRoot

// EnvToday pins "today" (YYYY-MM-DD) for --touch and tests.
var EnvToday = name.EnvToday

type Vault struct {
	Root       string // absolute, symlinks resolved
	ConfigPath string // "" when running on defaults
	Config     *config.Config
}

var (
	ErrNotFound  = errors.New("page not found")
	ErrAmbiguous = errors.New("page name is ambiguous")
	ErrOutside   = errors.New("path is outside the vault")
	// ErrNotContent: the path is inside the root but not on the content
	// allowlist (IsContent), e.g. .git/config or a file outside dirs.
	ErrNotContent = errors.New("path is not vault content")
)

// Open resolves the root: flagRoot > $VAULTY_ROOT > nearest ancestor of
// start containing the config file > nearest ancestor containing .git > start.
func Open(flagRoot, start string) (*Vault, error) {
	root, err := discoverRoot(flagRoot, start)
	if err != nil {
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("vault root %s: %w", root, err)
	}
	v := &Vault{Root: resolved}

	cfgPath := filepath.Join(resolved, config.FileName)
	if st, err := os.Stat(cfgPath); err == nil && !st.IsDir() {
		cfg, err := config.Load(cfgPath)
		if err != nil {
			return nil, err
		}
		v.Config = cfg
		v.ConfigPath = cfgPath
	} else {
		v.Config = config.Default()
	}
	return v, nil
}

func discoverRoot(flagRoot, start string) (string, error) {
	if flagRoot != "" {
		abs, err := filepath.Abs(flagRoot)
		if err != nil {
			return "", err
		}
		return abs, nil
	}
	if envRoot := os.Getenv(EnvRoot); envRoot != "" {
		abs, err := filepath.Abs(envRoot)
		if err != nil {
			return "", err
		}
		return abs, nil
	}
	absStart, err := filepath.Abs(start)
	if err != nil {
		return "", err
	}
	if dir, ok := findAncestorWith(absStart, config.FileName); ok {
		return dir, nil
	}
	if dir, ok := findAncestorWith(absStart, ".git"); ok {
		return dir, nil
	}
	return absStart, nil
}

// findAncestorWith walks up from start looking for a directory containing an
// entry named marker (file or directory — a worktree's ".git" is a file).
func findAncestorWith(start, marker string) (string, bool) {
	dir := start
	for {
		if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// Resolve maps a page argument (path, name, or [[wikilink]]) to a
// vault-relative slash path (DESIGN.md §3.3).
func (v *Vault) Resolve(arg string) (string, error) {
	a := arg
	if strings.HasPrefix(a, "[[") {
		a = strings.TrimPrefix(a, "[[")
		a = strings.TrimSuffix(a, "]]")
		if i := strings.IndexAny(a, "|#"); i != -1 {
			a = a[:i]
		}
	}

	if strings.Contains(a, "/") || strings.HasSuffix(a, ".md") {
		return v.resolvePath(a)
	}
	return v.resolveName(a)
}

func (v *Vault) resolvePath(a string) (string, error) {
	candidates := []string{a}
	if !strings.HasSuffix(a, ".md") {
		candidates = append(candidates, a+".md")
	}
	for _, base := range []string{"", v.Root} {
		for _, c := range candidates {
			full := c
			if base != "" {
				full = filepath.Join(base, c)
			}
			st, err := os.Stat(full)
			if err == nil && !st.IsDir() {
				rel, err := v.toRelInside(full)
				if err != nil {
					return "", err
				}
				if !v.IsContent(rel) {
					return "", fmt.Errorf("%w: %s", ErrNotContent, a)
				}
				return rel, nil
			}
		}
	}
	return "", fmt.Errorf("%w: %s", ErrNotFound, a)
}

// toRelInside converts an absolute (or relative) file path to a vault-relative
// slash path, after verifying (via EvalSymlinks) that it lies inside the root.
func (v *Vault) toRelInside(full string) (string, error) {
	abs, err := filepath.Abs(full)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(v.Root, resolved)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %s", ErrOutside, full)
	}
	return filepath.ToSlash(rel), nil
}

func (v *Vault) resolveName(a string) (string, error) {
	files, err := v.Walk()
	if err != nil {
		return "", err
	}
	target := a + ".md"
	var matches []string
	for _, f := range files {
		if path.Base(f) == target {
			matches = append(matches, f)
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("%w: %s", ErrNotFound, a)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("%w: %s (%s)", ErrAmbiguous, a, strings.Join(matches, ", "))
	}
}

// IsContent reports whether rel (a vault-relative slash path, symlinks
// already resolved) is vault content (DESIGN.md §3.5). This is an allowlist:
// only a `.md` file under one of Config.Dirs, with no segment starting with
// "." and not matched by Config.Exclude. Everything else (.git, other
// hidden dirs, files outside dirs, non-markdown files) is never read,
// indexed or written through a page argument.
func (v *Vault) IsContent(rel string) bool {
	if !strings.HasSuffix(rel, ".md") || !config.SafeRel(rel, false) {
		return false
	}
	inDir := false
	for _, d := range v.Config.Dirs {
		if d == "." || strings.HasPrefix(rel, strings.TrimSuffix(d, "/")+"/") {
			inDir = true
			break
		}
	}
	return inDir && !MatchAny(v.Config.Exclude, rel)
}

// ContentFile returns the absolute path of rel after re-checking, with
// symlinks resolved, that it is still vault content. Callers that read a
// path taken from Walk output or an index use it as a last line of
// defence against a symlink swapped in after the walk.
func (v *Vault) ContentFile(rel string) (string, error) {
	if !v.IsContent(rel) {
		return "", fmt.Errorf("%w: %s", ErrNotContent, rel)
	}
	full := filepath.Join(v.Root, filepath.FromSlash(rel))
	resolved, err := v.toRelInside(full)
	if err != nil {
		return "", err
	}
	if !v.IsContent(resolved) {
		return "", fmt.Errorf("%w: %s", ErrNotContent, rel)
	}
	return full, nil
}

// ConfigFile returns the absolute path of a vault-relative file named in
// config (log.path, find.index, lint.baseline_path). The path must be
// SafeRel (a hidden file name is allowed, a hidden directory is not), and
// when it exists its symlink-resolved target must be too. A missing file
// is not an error: callers decide what absence means.
func (v *Vault) ConfigFile(rel string) (string, error) {
	if !config.SafeRel(rel, true) {
		return "", fmt.Errorf("%w: %s", ErrNotContent, rel)
	}
	full := filepath.Join(v.Root, filepath.FromSlash(rel))
	if _, err := os.Lstat(full); err != nil {
		return full, nil
	}
	resolved, err := v.toRelInside(full)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return full, nil // dangling symlink: reads fail as missing
		}
		return "", err
	}
	if !config.SafeRel(resolved, true) {
		return "", fmt.Errorf("%w: %s", ErrNotContent, rel)
	}
	return full, nil
}

// Walk returns vault-relative .md files under dirs (default: Config.Dirs),
// sorted, keeping only vault content (ContentFile: the path and, for a
// symlink, its target).
func (v *Vault) Walk(dirs ...string) ([]string, error) {
	if len(dirs) == 0 {
		dirs = v.Config.Dirs
	}
	var out []string
	for _, d := range dirs {
		if !config.SafeRel(d, false) {
			continue
		}
		abs := filepath.Join(v.Root, d)
		st, err := os.Stat(abs)
		if err != nil || !st.IsDir() {
			continue
		}
		err = filepath.WalkDir(abs, func(p string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if p != abs && strings.HasPrefix(entry.Name(), ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(entry.Name(), ".md") {
				return nil
			}
			rel, err := filepath.Rel(v.Root, p)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if _, err := v.ContentFile(rel); err != nil {
				return nil
			}
			out = append(out, rel)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Strings(out)
	return out, nil
}

// MatchGlob: a pattern ending in "/**" matches everything below that
// prefix; any other pattern uses path.Match on the slash-relative path.
func MatchGlob(pattern, rel string) bool {
	if strings.HasSuffix(pattern, "/**") {
		prefix := strings.TrimSuffix(pattern, "**")
		return strings.HasPrefix(rel, prefix)
	}
	ok, _ := path.Match(pattern, rel)
	return ok
}

// MatchAny reports whether rel matches any pattern.
func MatchAny(patterns []string, rel string) bool {
	for _, p := range patterns {
		if MatchGlob(p, rel) {
			return true
		}
	}
	return false
}

// hasGlobMeta reports whether s contains any glob metacharacter this
// package's matching understands ("*", "?", "["). Used to tell a bare
// directory name ("wiki") from an actual glob ("wiki/**", "wiki/*.md") in
// --only-style flags (OnlyPatterns).
func hasGlobMeta(s string) bool {
	return strings.ContainsAny(s, "*?[")
}

// OnlyPatterns expands raw --only tokens (already split on commas by the
// caller) into match patterns for FilterOnly: a bare name with no glob
// metacharacters becomes "<name>/**" (so "wiki" and "now/actions" both mean
// "everything under that directory"); anything containing "*", "?" or "["
// is used exactly as given (MatchGlob semantics, §4.3). Blank tokens are
// dropped. This is shared, not `find`-specific, so the later `search`
// command can reuse the same --only flag and expansion rule.
func OnlyPatterns(raw []string) []string {
	var out []string
	for _, r := range raw {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		if hasGlobMeta(r) {
			out = append(out, r)
		} else {
			out = append(out, strings.TrimSuffix(r, "/")+"/**")
		}
	}
	return out
}

// FilterOnly keeps only the files matching at least one of patterns
// (already expanded via OnlyPatterns). No patterns means no filtering —
// every file is kept, matching Walk's own "no dirs given" convention. A
// pattern that matches nothing (e.g. --only pointing outside Config.Dirs)
// simply yields no files for that pattern; FilterOnly itself never errors.
func FilterOnly(files []string, patterns []string) []string {
	if len(patterns) == 0 {
		return files
	}
	var out []string
	for _, f := range files {
		if MatchAny(patterns, f) {
			out = append(out, f)
		}
	}
	return out
}
