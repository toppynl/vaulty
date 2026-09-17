package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// secret is planted in .git/config and a few other non-content files; no
// command may ever print it or write next to it (DESIGN.md §3.5).
const secret = "ghp_SECRETTOKEN0123456789"

// boundaryVault builds a vault whose content is wiki/a.md, plus every kind
// of non-content a page argument or index could reach: .git metadata,
// a hidden dir, a file outside dirs, a non-markdown file, and symlinks from
// inside wiki/ pointing at all of them.
func boundaryVault(t *testing.T) string {
	t.Helper()
	v := t.TempDir()
	writeFile(t, filepath.Join(v, ".vaulty.yml"), "version: 1\ndirs: [wiki]\n")
	writeFile(t, filepath.Join(v, "wiki", "a.md"), "---\ntype: note\n---\n\n# Alpha\n\nalpha apple\n")
	writeFile(t, filepath.Join(v, ".git", "config"), "[remote \"origin\"]\n\turl = https://x:"+secret+"@github.com/o/r\n")
	writeFile(t, filepath.Join(v, ".git", "notes.md"), "# Notes\n\napple "+secret+"\n")
	writeFile(t, filepath.Join(v, ".hidden", "h.md"), "# Hidden\n\napple "+secret+"\n")
	writeFile(t, filepath.Join(v, "outside", "o.md"), "# Outside\n\napple "+secret+"\n")
	writeFile(t, filepath.Join(v, "wiki", "data.txt"), "apple "+secret+"\n")
	for link, target := range map[string]string{
		"wiki/cfg.md":   "../.git/config",
		"wiki/notes.md": "../.git/notes.md",
		"wiki/out.md":   "../outside/o.md",
		"wiki/git":      "../.git",
	} {
		if err := os.Symlink(target, filepath.Join(v, filepath.FromSlash(link))); err != nil {
			t.Fatal(err)
		}
	}
	return v
}

func assertNoSecret(t *testing.T, what, stdout, stderr string) {
	t.Helper()
	if strings.Contains(stdout, secret) || strings.Contains(stderr, secret) {
		t.Errorf("%s leaked non-content:\nstdout: %s\nstderr: %s", what, stdout, stderr)
	}
}

func TestContentBoundaryPageArgs(t *testing.T) {
	t.Setenv("VAULTY_ROOT", "")
	v := boundaryVault(t)
	gitConfig, _ := os.ReadFile(filepath.Join(v, ".git", "config"))

	pages := []string{
		".git/config", "./.git/config", "wiki/../.git/config", filepath.Join(v, ".git", "config"),
		".git/notes.md", ".git/notes", ".hidden/h.md", "outside/o.md", "wiki/data.txt",
		"wiki/cfg.md", "wiki/notes.md", "wiki/out.md", "wiki/git/config", "wiki/git/notes.md",
		"[[.git/config]]", "cfg", "notes", "out",
	}
	for _, p := range pages {
		out, errOut, code := runCLI(t, v, "timeline", "read", p)
		assertNoSecret(t, "timeline read "+p, out, errOut)
		if code != ExitUsage {
			t.Errorf("timeline read %s: exit %d, want %d", p, code, ExitUsage)
		}
		out, errOut, code = runCLI(t, v, "timeline", "append", p, "- **2026-09-15** | x.")
		assertNoSecret(t, "timeline append "+p, out, errOut)
		if code == 0 {
			t.Errorf("timeline append %s: succeeded, want refusal", p)
		}
		out, errOut, code = runCLI(t, v, "timeline", "lint", p)
		assertNoSecret(t, "timeline lint "+p, out, errOut)
		if code == 0 {
			t.Errorf("timeline lint %s: succeeded, want refusal", p)
		}
	}
	after, _ := os.ReadFile(filepath.Join(v, ".git", "config"))
	if string(after) != string(gitConfig) {
		t.Error(".git/config was modified")
	}

	// The allowlisted page itself still works.
	if out, _, code := runCLI(t, v, "timeline", "read", "wiki/a.md"); code != 0 || !strings.Contains(out, "alpha apple") {
		t.Errorf("timeline read wiki/a.md: exit %d, out %q", code, out)
	}
}

func TestContentBoundaryFindSearch(t *testing.T) {
	t.Setenv("VAULTY_ROOT", "")
	t.Setenv("VAULTY_CACHE_DIR", t.TempDir())
	v := boundaryVault(t)

	for _, args := range [][]string{
		{"find", "apple", "--body"},
		{"find", "notes", "cfg", "out", "hidden", "config"},
		{"find", "apple", "--body", "--only", ".git"},
		{"search", "apple"},
		{"search", "apple", "--json"},
		{"search", "apple", "--only", ".git,.hidden,outside"},
		{"search", "--no-cache", "apple"},
		{"search", "ghp_SECRETTOKEN0123456789"},
	} {
		out, errOut, _ := runCLI(t, v, args...)
		assertNoSecret(t, strings.Join(args, " "), out, errOut)
		for _, p := range []string{".git", ".hidden", "outside", "wiki/cfg.md", "wiki/notes.md", "wiki/out.md", "data.txt"} {
			if strings.Contains(out, p) {
				t.Errorf("%v: output mentions non-content %q:\n%s", args, p, out)
			}
		}
	}
}

func TestContentBoundaryConfig(t *testing.T) {
	t.Setenv("VAULTY_ROOT", "")
	t.Setenv("VAULTY_CACHE_DIR", t.TempDir())

	// Config may not widen the allowlist into hidden dirs or outside the root.
	for _, cfg := range []string{
		"dirs: [.git]", "dirs: [wiki/.git]", "dirs: [\"../x\"]", "dirs: [/etc]",
		"log:\n  path: .git/config", "find:\n  index: .git/config", "lint:\n  baseline_path: .git/b.json",
		"log:\n  path: ../log.md",
	} {
		v := boundaryVault(t)
		writeFile(t, filepath.Join(v, ".vaulty.yml"), "version: 1\n"+cfg+"\n")
		out, errOut, code := runCLI(t, v, "log", "last")
		assertNoSecret(t, cfg, out, errOut)
		if code != ExitUsage {
			t.Errorf("config %q: exit %d, want %d (invalid config)", cfg, code, ExitUsage)
		}
	}

	// A config-named file that is a symlink into .git is refused at use.
	v := boundaryVault(t)
	if err := os.Symlink(".git/config", filepath.Join(v, "log.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(".git/notes.md", filepath.Join(v, "index.md")); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"log", "last"}, {"log", "lint"}, {"log", "append", "x", "y"},
		{"find", "alpha"}, {"search", "apple"},
	} {
		out, errOut, code := runCLI(t, v, args...)
		assertNoSecret(t, strings.Join(args, " "), out, errOut)
		if code == 0 {
			t.Errorf("%v with symlinked config file: succeeded, want refusal", args)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(v, ".git", "config")); strings.Contains(string(b), "] x |") || strings.Count(string(b), "\n") != 2 {
		t.Errorf(".git/config was written through log.md symlink:\n%s", b)
	}
}

// A cache built while a symlink pointed at content must not serve the
// target once it is swapped to non-content.
func TestContentBoundaryStaleCache(t *testing.T) {
	t.Setenv("VAULTY_ROOT", "")
	t.Setenv("VAULTY_CACHE_DIR", t.TempDir())
	v := boundaryVault(t)
	writeFile(t, filepath.Join(v, "wiki", "b.md"), "# Beta\n\nbanana\n")
	if out, _, code := runCLI(t, v, "search", "banana"); code != 0 || !strings.Contains(out, "wiki/b.md") {
		t.Fatalf("seed search: exit %d, out %q", code, out)
	}
	if err := os.Remove(filepath.Join(v, "wiki", "b.md")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(v, ".git", "b.md"), "# Beta\n\nbanana "+secret+"\n")
	if err := os.Symlink("../.git/b.md", filepath.Join(v, "wiki", "b.md")); err != nil {
		t.Fatal(err)
	}
	out, errOut, _ := runCLI(t, v, "search", "banana")
	assertNoSecret(t, "search after symlink swap", out, errOut)
	if strings.Contains(out, "wiki/b.md") {
		t.Errorf("stale cache served non-content wiki/b.md:\n%s", out)
	}
}
