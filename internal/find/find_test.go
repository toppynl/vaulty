package find

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/toppynl/vaulty/internal/vault"
)

// TestSearchAllBypassesExclude pins the --all/find.exclude interaction that
// find-ranking's golden case can only show one side of (a single CLI
// invocation can't assert "hidden by default" and "shown with --all" at
// once): a page under a find.exclude glob is dropped by default and
// restored by Options.All.
func TestSearchAllBypassesExclude(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "archive", "old-thing.md"), "---\ntype: initiative\n---\n\n# Old Thing\n")
	mustWrite(t, filepath.Join(dir, ".vaulty.yml"), "version: 1\nfind:\n  exclude: [\"archive/**\"]\n")

	v, err := vault.Open("", dir)
	if err != nil {
		t.Fatalf("vault.Open: %v", err)
	}

	terms := NormalizeTerms([]string{"old thing"})

	results, err := Search(v, terms, Options{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("default (no --all): got %d results, want 0 (archive/** excluded); results=%+v", len(results), results)
	}

	results, err = Search(v, terms, Options{All: true})
	if err != nil {
		t.Fatalf("Search --all: %v", err)
	}
	if len(results) != 1 || results[0].Path != "archive/old-thing.md" {
		t.Errorf("--all: got %+v, want one result archive/old-thing.md", results)
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
