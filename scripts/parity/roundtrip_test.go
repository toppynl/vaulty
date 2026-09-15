// Package parity holds the live round-trip proof against the real vault
// (DESIGN.md §10.3, scope call 2026-09-15). It never runs in CI and never
// touches the vault: point it at a read-only scratch copy.
package parity

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/toppynl/vaulty/internal/doc"
	"github.com/toppynl/vaulty/internal/timeline"
	"github.com/toppynl/vaulty/internal/vault"
)

// TestVaultRoundTrip is gated on VAULTY_PARITY_ROOT (skipped otherwise, so
// `go test ./...` stays green without the private vault). Point it at a
// scratch copy of the vault, e.g.:
//
//	VAULTY_PARITY_ROOT=/path/to/scratch go test ./scripts/parity/... -run TestVaultRoundTrip -v
//
// For every .md file returned by vault.Walk (the default config's dirs:
// wiki me now archive), the file is independently reconstructed byte for
// byte from its parsed model, never from a copy-then-splice of the
// original bytes (a copy-then-splice test can never fail once the pieces
// it splices back are the same bytes it already checked equal — see the
// finding this replaced):
//
//   - frontmatter and the compiled-truth span are copied verbatim (this
//     tool never restructures either);
//   - each Timeline block's heading line is copied verbatim, and its body
//     is independently reserialized from the parsed Entries/Gaps/blanks via
//     SerializeBody when the block is Sortable, or copied verbatim
//     otherwise (a non-Sortable block is not round-trippable by
//     construction — it is counted separately, not identity-checked);
//   - any bytes between blocks, or after the last block (TL003's "content
//     after" case), are copied verbatim.
//
// The independently reconstructed file must be byte-identical to the
// source. A mismatch confined to one block is reported as a block mismatch
// (the SerializeBody output differs from the source body); any other
// mismatch is reported as a file mismatch (a span/boundary bug elsewhere in
// the parser). Both are always reported for every file, so the two never
// hide each other.
func TestVaultRoundTrip(t *testing.T) {
	root := os.Getenv("VAULTY_PARITY_ROOT")
	if root == "" {
		t.Skip("VAULTY_PARITY_ROOT not set; skipping live vault round-trip check")
	}

	v, err := vault.Open(root, root)
	if err != nil {
		t.Fatal(err)
	}
	files, err := v.Walk()
	if err != nil {
		t.Fatal(err)
	}

	var blocksChecked, blocksSkipped, blockMismatches, fileMismatches int

	for _, rel := range files {
		full := filepath.Join(v.Root, filepath.FromSlash(rel))
		src, err := os.ReadFile(full)
		if err != nil {
			t.Fatal(err)
		}
		d := doc.Parse(rel, src)
		page := timeline.Parse(d, v.Config.Timeline)

		var out []byte
		if len(page.Blocks) == 0 {
			// No Timeline block: nothing to reconstruct, the whole file is
			// "compiled truth" as far as this tool is concerned.
			out = append(out, src...)
		} else {
			out = append(out, src[:page.Blocks[0].Heading.Start]...) // frontmatter + compiled truth
			cursor := page.Blocks[0].Heading.Start
			for i := range page.Blocks {
				b := &page.Blocks[i]
				out = append(out, src[b.Heading.Start:b.Heading.End]...) // heading, verbatim

				if b.Sortable() {
					blocksChecked++
					body := timeline.SerializeBody(b.Entries, b.Gaps, b.LeadingBlanks, b.TrailingBlanks)
					if body != string(src[b.Body.Start:b.Body.End]) {
						blockMismatches++
						t.Errorf("round-trip mismatch (block): %s block at line %d", rel, b.HeadingLine)
					}
					out = append(out, []byte(body)...)
				} else {
					blocksSkipped++
					out = append(out, src[b.Body.Start:b.Body.End]...) // not round-trippable by construction
				}
				cursor = b.Body.End

				next := len(src)
				if i+1 < len(page.Blocks) {
					next = page.Blocks[i+1].Heading.Start
				}
				out = append(out, src[cursor:next]...) // gap to the next block, or trailing content, verbatim
				cursor = next
			}
		}

		if !bytes.Equal(out, src) {
			fileMismatches++
			t.Errorf("round-trip mismatch (full file): %s", rel)
		}
	}

	t.Logf("files=%d blocks_checked=%d blocks_skipped_non_sortable=%d block_mismatches=%d file_mismatches=%d",
		len(files), blocksChecked, blocksSkipped, blockMismatches, fileMismatches)
}
