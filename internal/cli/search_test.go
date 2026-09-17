package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runCLI(t *testing.T, vaultDir string, args ...string) (stdout, stderr string, exit int) {
	t.Helper()
	var out, errb bytes.Buffer
	full := append([]string{"--vault", vaultDir}, args...)
	exit = Execute("test", full, strings.NewReader(""), &out, &errb)
	return out.String(), errb.String(), exit
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// hitPaths returns the result paths (non-indented lines) of human output.
func hitPaths(stdout string) []string {
	var out []string
	for _, line := range strings.Split(stdout, "\n") {
		if line == "" || strings.HasPrefix(line, " ") {
			continue
		}
		out = append(out, strings.SplitN(line, "\t", 2)[0])
	}
	return out
}

func mustStat(t *testing.T, stats, want string) {
	t.Helper()
	for _, line := range strings.Split(stats, "\n") {
		if line == want {
			return
		}
	}
	t.Errorf("--stats output lacks %q:\n%s", want, stats)
}

// TestSearchCacheFreshness pins DESIGN.md §19.3: a modified, an added and a
// deleted page are applied incrementally (exactly 3 updates, no rebuild),
// and a config change to dirs forces a full rebuild.
func TestSearchCacheFreshness(t *testing.T) {
	t.Setenv("VAULTY_ROOT", "")
	t.Setenv("VAULTY_CACHE_DIR", t.TempDir())
	v := t.TempDir()
	writeFile(t, filepath.Join(v, "wiki", "a.md"), "---\ntype: note\n---\n\n# Alpha\n\nalpha apple\n")
	writeFile(t, filepath.Join(v, "wiki", "b.md"), "---\ntype: note\n---\n\n# Beta\n\nbeta banana\n")
	writeFile(t, filepath.Join(v, "wiki", "c.md"), "---\ntype: note\n---\n\n# Gamma\n\ngamma cherry\n")

	out, errOut, code := runCLI(t, v, "search", "apple")
	if code != 0 || strings.Join(hitPaths(out), ",") != "wiki/a.md" {
		t.Fatalf("first search: exit %d, hits %v, stderr %q", code, hitPaths(out), errOut)
	}
	stats, _, _ := runCLI(t, v, "search", "--stats")
	mustStat(t, stats, "pages: 3")
	mustStat(t, stats, "last_update_kind: full")
	mustStat(t, stats, "full_rebuilds: 1")

	writeFile(t, filepath.Join(v, "wiki", "b.md"), "---\ntype: note\n---\n\n# Beta\n\nbeta banana now with kiwi\n")
	writeFile(t, filepath.Join(v, "wiki", "d.md"), "---\ntype: note\n---\n\n# Delta\n\ndelta kiwi\n")
	if err := os.Remove(filepath.Join(v, "wiki", "c.md")); err != nil {
		t.Fatal(err)
	}

	out, errOut, code = runCLI(t, v, "search", "kiwi", "cherry")
	if strings.Contains(errOut, "cache unavailable") {
		t.Fatalf("unexpected in-memory fallback: %q", errOut)
	}
	got := hitPaths(out)
	if code != 0 || len(got) != 2 || !strings.Contains(out, "wiki/b.md\t") || !strings.Contains(out, "wiki/d.md\t") {
		t.Fatalf("after changes: exit %d, hits %v (want wiki/b.md and wiki/d.md, no deleted wiki/c.md)", code, got)
	}
	stats, _, _ = runCLI(t, v, "search", "--stats")
	mustStat(t, stats, "pages: 3")
	mustStat(t, stats, "last_check_updated: 3")
	mustStat(t, stats, "last_update_kind: incremental")
	mustStat(t, stats, "full_rebuilds: 1")

	writeFile(t, filepath.Join(v, ".vaulty.yml"), "version: 1\ndirs: [wiki, notes]\n")
	if _, errOut, code = runCLI(t, v, "search", "kiwi"); code != 0 {
		t.Fatalf("search after config change: exit %d, stderr %q", code, errOut)
	}
	stats, _, _ = runCLI(t, v, "search", "--stats")
	mustStat(t, stats, "last_update_kind: full")
	mustStat(t, stats, "full_rebuilds: 2")
}

// TestSearchUnwritableCacheFallsBackToMemory: a cache dir that can't be
// created must not fail the search — it indexes in memory and says so once.
func TestSearchUnwritableCacheFallsBackToMemory(t *testing.T) {
	t.Setenv("VAULTY_ROOT", "")
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	writeFile(t, blocker, "a regular file where the cache dir's parent should be\n")
	t.Setenv("VAULTY_CACHE_DIR", filepath.Join(blocker, "cache"))

	v := t.TempDir()
	writeFile(t, filepath.Join(v, "wiki", "a.md"), "---\ntype: note\n---\n\n# Alpha\n\nalpha apple\n")
	writeFile(t, filepath.Join(v, "wiki", "b.md"), "---\ntype: note\n---\n\n# Beta\n\nbeta banana\n")

	out, errOut, code := runCLI(t, v, "search", "banana")
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	if got := hitPaths(out); len(got) != 1 || got[0] != "wiki/b.md" {
		t.Errorf("hits %v, want [wiki/b.md]", got)
	}
	if n := strings.Count(errOut, "vaulty: search cache unavailable ("); n != 1 || !strings.Contains(errOut, "), indexing in memory\n") {
		t.Errorf("want exactly one fallback line on stderr, got %q", errOut)
	}
}
