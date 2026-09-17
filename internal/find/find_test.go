package find

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/toppynl/vaulty/internal/vault"
)

// TestSearchOnlyFilter pins the --only interaction that find-ranking's
// golden case can only show one side of (a single CLI invocation can't
// assert "found by default" and "hidden with --only" pointed elsewhere at
// once): a page under wiki/ is found by default and dropped once the
// caller restricts the search to another directory via --only.
func TestSearchOnlyFilter(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "archive", "old-thing.md"), "---\ntype: initiative\n---\n\n# Old Thing\n")
	mustWrite(t, filepath.Join(dir, "wiki", "unrelated.md"), "---\ntype: system\n---\n\n# Unrelated\n")

	v, err := vault.Open("", dir)
	if err != nil {
		t.Fatalf("vault.Open: %v", err)
	}

	terms := NormalizeTerms([]string{"old thing"})

	results, err := Search(v, terms, Options{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || results[0].Path != "archive/old-thing.md" {
		t.Errorf("default (no --only): got %+v, want one result archive/old-thing.md", results)
	}

	results, err = Search(v, terms, Options{Only: []string{"wiki"}})
	if err != nil {
		t.Fatalf("Search --only wiki: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("--only wiki: got %d results, want 0 (archive/old-thing.md is outside wiki/**); results=%+v", len(results), results)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
