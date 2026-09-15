// Package vault finds the vault root, loads its config, resolves page
// arguments and walks content dirs (DESIGN.md §4.2, §3.3).
package vault

import (
	"errors"
	"path"
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
)

// Open resolves the root: flagRoot > $VAULTY_ROOT > nearest ancestor of
// start containing the config file > nearest ancestor containing .git > start.
func Open(flagRoot, start string) (*Vault, error) {
	// TODO(step 1)
	return nil, errors.New("not implemented")
}

// Resolve maps a page argument (path, name, or [[wikilink]]) to a
// vault-relative slash path (DESIGN.md §3.3).
func (v *Vault) Resolve(arg string) (string, error) {
	// TODO(step 1)
	return "", errors.New("not implemented")
}

// Walk returns vault-relative .md files under dirs (default: Config.Dirs),
// sorted, skipping dot-dirs and Config.Exclude.
func (v *Vault) Walk(dirs ...string) ([]string, error) {
	// TODO(step 1)
	return nil, errors.New("not implemented")
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
