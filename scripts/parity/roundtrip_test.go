// Package parity holds the live round-trip proof against the real vault
// (DESIGN.md §10.3, scope call 2026-09-15). It never runs in CI and never
// touches the vault: point it at a read-only scratch copy.
package parity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/toppynl/vaulty/internal/config"
	"github.com/toppynl/vaulty/internal/doc"
	"github.com/toppynl/vaulty/internal/timeline"
)

// TestVaultRoundTrip is gated on VAULTY_PARITY_ROOT (skipped otherwise, so
// `go test ./...` stays green without the private vault). Point it at a
// scratch copy of the vault, e.g.:
//
//	VAULTY_PARITY_ROOT=/path/to/scratch go test ./scripts/parity/... -run TestVaultRoundTrip -v
//
// For every .md file under wiki/me/now/archive: parse it, reconstruct every
// Sortable block's body from its original (unsorted) entries/gaps/blanks via
// SerializeBody, and splice it back in place. The result must be
// byte-identical to the source file. Non-Sortable blocks are skipped from
// the identity check (they're not round-trippable by construction) and
// counted separately.
func TestVaultRoundTrip(t *testing.T) {
	root := os.Getenv("VAULTY_PARITY_ROOT")
	if root == "" {
		t.Skip("VAULTY_PARITY_ROOT not set; skipping live vault round-trip check")
	}

	cfg := config.Default()
	dirs := []string{"wiki", "me", "now", "archive"}

	var files, blocksChecked, blocksSkipped, mismatches int

	for _, dir := range dirs {
		base := filepath.Join(root, dir)
		if _, err := os.Stat(base); err != nil {
			continue
		}
		err := filepath.WalkDir(base, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if p != base && strings.HasPrefix(d.Name(), ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(d.Name(), ".md") {
				return nil
			}
			files++
			src, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			d2 := doc.Parse(p, src)
			page := timeline.Parse(d2, cfg.Timeline)

			out := append([]byte(nil), src...)
			// Splice from the end backwards so earlier offsets stay valid.
			for i := len(page.Blocks) - 1; i >= 0; i-- {
				b := &page.Blocks[i]
				if !b.Sortable() {
					blocksSkipped++
					continue
				}
				blocksChecked++
				body := timeline.SerializeBody(b.Entries, b.Gaps, b.LeadingBlanks, b.TrailingBlanks)
				if body != string(src[b.Body.Start:b.Body.End]) {
					mismatches++
					t.Errorf("round-trip mismatch: %s block at line %d", p, b.HeadingLine)
					continue
				}
				out = append(out[:b.Body.Start:b.Body.Start], append([]byte(body), out[b.Body.End:]...)...)
			}
			if string(out) != string(src) {
				mismatches++
				t.Errorf("round-trip mismatch (full file): %s", p)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	t.Logf("files=%d blocks_checked=%d blocks_skipped_non_sortable=%d mismatches=%d",
		files, blocksChecked, blocksSkipped, mismatches)
}
