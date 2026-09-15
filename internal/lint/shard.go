// Shard hygiene checks (SH001-SH005, DESIGN.md §16) for the hub-page-plus-
// children sharding convention: a hub `wiki/<type>/<x>.md` with its children
// in a sibling directory `wiki/<type>/<x>/`.
package lint

import (
	"fmt"
	"os"
	"path"
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

var reWikilinkTarget = regexp.MustCompile(`\[\[([^\]|#]+)`)

// IsHubDirCandidate reports whether dirRel (a vault-relative directory path,
// no trailing slash) is a hub-directory candidate under sc: its parent
// directory matches one of sc.TypeDirs. This is deliberately the only place
// "what counts as a shard" is decided, so it can be made stricter or looser
// per vault via config instead of a hard-coded depth or name.
func IsHubDirCandidate(sc config.Shard, dirRel string) bool {
	if len(sc.TypeDirs) == 0 {
		return false
	}
	parent := path.Dir(dirRel)
	return vault.MatchAny(sc.TypeDirs, parent)
}

// hubDir is one hub-directory candidate discovered on disk, hub page present
// or not.
type hubDir struct {
	Dir      string // vault-relative, no trailing slash
	HubFile  string // vault-relative hub page path; may not exist
	HubName  string
	Children []string // vault-relative child .md files directly inside Dir
}

// discoverHubDirs walks v.Config.Dirs for directories matching
// v.Config.Lint.Shard.TypeDirs. Directories starting with "." are skipped,
// same as vault.Walk.
func discoverHubDirs(v *vault.Vault) ([]hubDir, error) {
	sc := v.Config.Lint.Shard
	if len(sc.TypeDirs) == 0 {
		return nil, nil
	}
	var out []hubDir
	for _, d := range v.Config.Dirs {
		abs := filepath.Join(v.Root, d)
		st, err := os.Stat(abs)
		if err != nil || !st.IsDir() {
			continue
		}
		err = filepath.WalkDir(abs, func(p string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() {
				return nil
			}
			if p != abs && strings.HasPrefix(entry.Name(), ".") {
				return filepath.SkipDir
			}
			if p == abs {
				return nil
			}
			rel, err := filepath.Rel(v.Root, p)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if !IsHubDirCandidate(sc, rel) {
				return nil
			}
			children, err := childFiles(v.Root, rel)
			if err != nil {
				return err
			}
			hubName := path.Base(rel)
			hubFile := path.Join(path.Dir(rel), hubName+".md")
			out = append(out, hubDir{Dir: rel, HubFile: hubFile, HubName: hubName, Children: children})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Dir < out[j].Dir })
	return out, nil
}

func childFiles(root, dirRel string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(dirRel)))
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		out = append(out, path.Join(dirRel, e.Name()))
	}
	sort.Strings(out)
	return out, nil
}

// CheckShardDirs runs the hub-level checks (SH001: a hub directory has no
// sibling hub page; SH003: a hub does not link one of its children),
// scoped to scope — normally the exact file list a Run() call already
// resolved (whole vault, a directory subset, explicit files, --changed, or
// the single --hook file). A hub directory's findings are kept only when
// scope touches it: its hub file, or at least one of its children, is in
// scope. This makes SH001/SH003 respect the same "lint <path>" / --changed /
// --hook scoping every other check gets, without needing a whole-vault walk
// for a single-file hook run to feel surprising.
func CheckShardDirs(v *vault.Vault, scope []string) ([]diag.Diag, error) {
	hubs, err := discoverHubDirs(v)
	if err != nil {
		return nil, err
	}
	if len(hubs) == 0 {
		return nil, nil
	}

	inScope := make(map[string]bool, len(scope))
	for _, f := range scope {
		inScope[f] = true
	}
	touches := func(h hubDir) bool {
		if inScope[h.HubFile] {
			return true
		}
		for _, c := range h.Children {
			if inScope[c] {
				return true
			}
		}
		return false
	}

	var diags []diag.Diag
	for _, h := range hubs {
		if !touches(h) {
			continue
		}

		if _, err := os.Stat(filepath.Join(v.Root, filepath.FromSlash(h.HubFile))); err != nil {
			diags = append(diags, diag.Diag{
				Code: diag.SH001ChildWithoutHub, Severity: diag.Error,
				Path: h.Dir + "/", Line: 0,
				Message: fmt.Sprintf("%s/ has children but no hub page %s", h.Dir, h.HubFile),
			})
			continue // nothing to link-check without a hub page
		}

		src, err := os.ReadFile(filepath.Join(v.Root, filepath.FromSlash(h.HubFile)))
		if err != nil {
			return nil, err
		}
		hd := doc.Parse(h.HubFile, src)
		linked := wikilinkTargets(string(src))
		for _, c := range h.Children {
			name := strings.TrimSuffix(path.Base(c), ".md")
			if linked[name] {
				continue
			}
			diags = append(diags, diag.Diag{
				Code: diag.SH003HubMissingChild, Severity: diag.Error,
				Path: h.HubFile, Line: hd.LineOf(hd.Body.Start),
				Message: fmt.Sprintf("hub does not link its child [[%s]] (%s)", name, c),
			})
		}
	}
	return diags, nil
}

func wikilinkTargets(text string) map[string]bool {
	out := map[string]bool{}
	for _, m := range reWikilinkTarget.FindAllStringSubmatch(text, -1) {
		out[strings.TrimSpace(m[1])] = true
	}
	return out
}

// checkShardChild runs the child-level checks (SH002, SH004, SH005) for one
// already-parsed page, purely from the page itself and its own path — no
// extra I/O, so it runs for free inside the normal per-file CheckPage pass
// in both files and vault mode. A page counts as a child when its own
// directory is a hub-directory candidate (DESIGN.md §16); a plain,
// unsharded page under e.g. wiki/systems/ is never a child.
func checkShardChild(p *timeline.Page, cfg *config.Config) []diag.Diag {
	dir := path.Dir(p.Doc.Path)
	if dir == "." || !IsHubDirCandidate(cfg.Lint.Shard, dir) {
		return nil
	}
	hubName := path.Base(dir)

	var diags []diag.Diag

	relatedText, relatedLine := relatedField(p.Doc)
	if !relatedListsHub(relatedText, hubName) {
		line := relatedLine
		if line == 0 {
			line = 1
		}
		diags = append(diags, diag.Diag{
			Code: diag.SH002ChildMissingHub, Severity: diag.Error, Line: line,
			Message: fmt.Sprintf("child of %s/ does not list its hub [[%s]] in related:", dir, hubName),
		})
	}

	text := string(p.Doc.Src[p.CompiledTruth.Start:p.CompiledTruth.End])
	if tokens := EstimateTokens(len(text)); tokens > cfg.Lint.PageChecks.CompiledTruthMaxTokens {
		diags = append(diags, diag.Diag{
			Code: diag.SH004ChildOversized, Severity: diag.Error,
			Line:    p.Doc.LineOf(p.CompiledTruth.Start),
			Message: fmt.Sprintf("child compiled truth ~%d tokens > %d — split further, or move history/work out", tokens, cfg.Lint.PageChecks.CompiledTruthMaxTokens),
		})
	}

	if len(p.Blocks) > 0 {
		diags = append(diags, diag.Diag{
			Code: diag.SH005TimelineInChild, Severity: diag.Error,
			Line:    p.Blocks[0].HeadingLine,
			Message: "'## Timeline' on a child page — Timeline stays on the hub only",
		})
	}

	return diags
}

// relatedField returns the raw text of the frontmatter `related:` block
// (the key line plus every blank or indented/list-item line after it) and
// the 1-based line the key itself starts at. ("", 0) means no frontmatter,
// or no `related:` key found before the closing delimiter.
func relatedField(d *doc.Doc) (text string, line int) {
	if !d.HasFM {
		return "", 0
	}
	for n := 2; n <= d.NumLines(); n++ { // line 1 is the opening '---'
		raw := d.Line(n)
		if strings.TrimRight(raw, " \t\r") == "---" {
			return "", 0 // closing delimiter reached, key not found
		}
		if !strings.HasPrefix(raw, "related:") {
			continue
		}
		var b strings.Builder
		b.WriteString(raw)
		b.WriteByte('\n')
		for m := n + 1; m <= d.NumLines(); m++ {
			l := d.Line(m)
			if strings.TrimRight(l, " \t\r") == "---" {
				break
			}
			if l == "" || strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t") {
				b.WriteString(l)
				b.WriteByte('\n')
				continue
			}
			break
		}
		return b.String(), n
	}
	return "", 0
}

func relatedListsHub(relatedText, hubName string) bool {
	if relatedText == "" {
		return false
	}
	for _, m := range reWikilinkTarget.FindAllStringSubmatch(relatedText, -1) {
		if strings.TrimSpace(m[1]) == hubName {
			return true
		}
	}
	return false
}

// applyOverridesEach applies lint.severity/lint.overrides to a mixed-path
// diag list (each diag using its own Path), unlike applyOverrides which
// assumes every diag in the slice shares one path — CheckShardDirs' output
// spans many hub directories and pages in one call.
func applyOverridesEach(diags []diag.Diag, severity map[string]string, overrides []config.Override) []diag.Diag {
	if len(severity) == 0 && len(overrides) == 0 {
		return diags
	}
	out := diags[:0]
	for _, d := range diags {
		out = append(out, applyOverrides([]diag.Diag{d}, d.Path, severity, overrides)...)
	}
	return out
}
