// Package page holds the per-page metadata helpers shared by `vaulty find`
// (DESIGN.md §18) and `vaulty search` (DESIGN.md §19): lenient frontmatter
// parsing, index.md summaries, the first H1, and rune-safe byte capping.
// Nothing here scores or ranks; it only reads what a page says about itself.
package page

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"github.com/toppynl/vaulty/internal/config"
	"github.com/toppynl/vaulty/internal/doc"
)

// Frontmatter is the handful of well-known keys find/search read by name,
// plus Values: every top-level scalar (or list-of-scalars) key, stringified
// (see FrontmatterValues).
type Frontmatter struct {
	Type    string
	Title   string
	Status  string
	Aliases []string
	Tags    []string
	Values  map[string][]string
}

// ParseFrontmatter parses d's frontmatter leniently: broken YAML, a
// non-mapping document, or no frontmatter at all yields a zero-value
// Frontmatter rather than an error — a page with malformed frontmatter must
// still match on slug/H1/body (DESIGN.md §18.3: "never fails the command").
// fields names the frontmatter keys Type and Title come from
// (config.Fields, DESIGN.md §4.1); Aliases, Tags and Status keep their
// fixed, conventional keys.
func ParseFrontmatter(d *doc.Doc, fields config.Fields) Frontmatter {
	if !d.HasFM {
		return Frontmatter{}
	}
	var raw map[string]any
	if err := yaml.Unmarshal([]byte(frontmatterInner(d)), &raw); err != nil || raw == nil {
		return Frontmatter{}
	}
	vals := FrontmatterValues(raw)
	first := func(k string) string {
		if v := vals[k]; len(v) > 0 {
			return v[0]
		}
		return ""
	}
	return Frontmatter{
		Type:    first(fields.Type),
		Title:   first(fields.Title),
		Status:  first("status"),
		Aliases: vals["aliases"],
		Tags:    vals["tags"],
		Values:  vals,
	}
}

// FrontmatterValues flattens a decoded frontmatter mapping into key ->
// string values: a scalar (string, number, bool, date) becomes a one-element
// list, a list keeps its scalar elements (non-scalar elements are dropped).
// Nested maps, nulls and empty strings are skipped entirely.
func FrontmatterValues(raw map[string]any) map[string][]string {
	out := map[string][]string{}
	for k, v := range raw {
		switch x := v.(type) {
		case []any:
			var list []string
			for _, e := range x {
				if s, ok := scalarString(e); ok {
					list = append(list, s)
				}
			}
			if len(list) > 0 {
				out[k] = list
			}
		default:
			if s, ok := scalarString(x); ok {
				out[k] = []string{s}
			}
		}
	}
	return out
}

func scalarString(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, x != ""
	case bool:
		return strconv.FormatBool(x), true
	case int:
		return strconv.Itoa(x), true
	case int64:
		return strconv.FormatInt(x, 10), true
	case uint64:
		return strconv.FormatUint(x, 10), true
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), true
	case time.Time:
		if x.Hour() == 0 && x.Minute() == 0 && x.Second() == 0 && x.Nanosecond() == 0 {
			return x.Format("2006-01-02"), true
		}
		return x.Format(time.RFC3339), true
	case nil, map[string]any, []any:
		return "", false
	default:
		return fmt.Sprint(x), true
	}
}

// frontmatterInner strips the two "---" delimiter lines, returning just the
// YAML body between them.
func frontmatterInner(d *doc.Doc) string {
	raw := string(d.Src[d.Frontmatter.Start:d.Frontmatter.End])
	lines := strings.Split(raw, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) < 2 {
		return ""
	}
	return strings.Join(lines[1:len(lines)-1], "\n")
}

// FirstH1 returns the text of the page's first level-1 heading, or "".
func FirstH1(d *doc.Doc) string {
	for _, h := range doc.Headings(d) {
		if h.Level == 1 {
			return h.Text
		}
	}
	return ""
}

// indexLineRe matches "- [[name]] — <rest of line>" lines (DESIGN.md §18.3:
// the vault's index.md convention). The name may itself be a wikilink with
// an alias/anchor ("name|alias" or "name#heading"); only the part before
// "|"/"#" is the lookup key, matching page-name resolution elsewhere
// (vault.Resolve). trailingDateParenRe strips exactly one trailing
// parenthetical, and only when it contains a full date.
var indexLineRe = regexp.MustCompile(`^- \[\[([^\]]+)\]\] — (.+)$`)
var trailingDateParenRe = regexp.MustCompile(`\s*\([^()]*\d{4}-\d{2}-\d{2}[^()]*\)\s*$`)

// LoadIndex reads the index file (config find.index, already resolved via
// vault.ConfigFile) and returns a name -> summary map. A missing file is skipped
// silently; lines that don't match the convention are ignored, never an
// error.
func LoadIndex(indexFile string) map[string]string {
	out := map[string]string{}
	b, err := os.ReadFile(indexFile)
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimRight(line, "\r")
		m := indexLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		name := m[1]
		if i := strings.IndexAny(name, "|#"); i != -1 {
			name = name[:i]
		}
		summary := trailingDateParenRe.ReplaceAllString(m[2], "")
		out[name] = strings.TrimSpace(summary)
	}
	return out
}

// CapBytes caps s at max bytes, backing off to the nearest UTF-8 rune
// boundary so a multi-byte rune is never split.
func CapBytes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
