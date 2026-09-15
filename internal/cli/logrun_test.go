package cli

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/toppynl/vaulty/internal/vaultlog"
)

// TestAppendLogEntryPreservesSymlink guards against a regression to the
// old tmpfile+rename write (internal/cli/append.go's atomicWrite): renaming
// a new file over log.md would replace a symlinked log.md with a plain
// file, silently breaking whatever the symlink pointed at. appendLogEntry
// must write into the existing inode instead (DESIGN.md §16.2).
func TestAppendLogEntryPreservesSymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real-log.md")
	if err := os.WriteFile(real, []byte("## [2026-01-01] build | First\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "log.md")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	if _, err := appendLogEntry(link, "2026-01-02", "fix", "Second", ""); err != nil {
		t.Fatalf("appendLogEntry: %v", err)
	}

	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%s is no longer a symlink after append", link)
	}
	got, err := os.ReadFile(real)
	if err != nil {
		t.Fatal(err)
	}
	want := "## [2026-01-01] build | First\n\n## [2026-01-02] fix | Second\n"
	if string(got) != want {
		t.Errorf("real file content = %q, want %q", got, want)
	}
}

// TestAppendLogEntryConcurrentNoLostEntries drives many concurrent
// appenders at the same file and checks every one of their entries
// survives — the exclusive flock (DESIGN.md §16.2) must serialize the
// read-current-size-then-write-at-that-offset sequence, or concurrent
// writers would compute the same offset and clobber each other.
func TestAppendLogEntryConcurrentNoLostEntries(t *testing.T) {
	dir := t.TempDir()
	full := filepath.Join(dir, "log.md")

	const n = 30
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := appendLogEntry(full, "2026-01-01", "test", titleFor(i), "")
			errs[i] = err
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	src, err := os.ReadFile(full)
	if err != nil {
		t.Fatal(err)
	}
	entries, malformed := vaultlog.Parse(src)
	if len(malformed) != 0 {
		t.Fatalf("malformed = %+v (file corrupted): %s", malformed, src)
	}
	if len(entries) != n {
		t.Fatalf("got %d entries, want %d (lost writes): %s", len(entries), n, src)
	}
	seen := make(map[string]bool, n)
	for _, e := range entries {
		seen[e.Title] = true
	}
	if len(seen) != n {
		t.Errorf("got %d distinct titles, want %d (duplicate/overwritten entry)", len(seen), n)
	}
}

func titleFor(i int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz"
	return "entry-" + string(letters[i%26]) + string(rune('0'+i/26))
}
