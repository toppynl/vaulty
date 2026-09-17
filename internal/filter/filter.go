// Package filter implements the "--where key=value" frontmatter filter
// shared by `vaulty search` (DESIGN.md §19.1) and `vaulty find` (§18, item 3
// of the fields/where follow-up): exact, case-sensitive matching against a
// page's frontmatter, with a list-valued key matching when the list
// contains the value. Both commands parse the same way and match the same
// way; only what they do with a match (a non-scoring bleve filter for
// search, an in-memory pre-filter for find) differs, so that part stays in
// each command's own package.
package filter

import (
	"fmt"
	"strings"
)

// Filter is one exact frontmatter match: the page's frontmatter key Key has
// Value (a list-valued key matches if the list contains Value).
type Filter struct {
	Key   string
	Value string
}

// Parse parses a "--where key=value" argument, splitting on the first "="
// only (so a value may itself contain "=" or "/", e.g.
// "--where thread=spaces/AAA/threads/BBB").
func Parse(s string) (Filter, error) {
	k, v, ok := strings.Cut(s, "=")
	k = strings.TrimSpace(k)
	if !ok || k == "" || v == "" {
		return Filter{}, fmt.Errorf("--where %q: want key=value", s)
	}
	return Filter{Key: k, Value: v}, nil
}

// Match reports whether values (as returned by page.FrontmatterValues) has
// f.Value under f.Key — exact, case-sensitive; a list-valued key matches
// when the list contains the value.
func Match(values map[string][]string, f Filter) bool {
	for _, v := range values[f.Key] {
		if v == f.Value {
			return true
		}
	}
	return false
}

// MatchAll reports whether values matches every filter in fs (AND'ed). An
// empty fs matches everything.
func MatchAll(values map[string][]string, fs []Filter) bool {
	for _, f := range fs {
		if !Match(values, f) {
			return false
		}
	}
	return true
}
