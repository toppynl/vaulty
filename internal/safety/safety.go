// Package safety verifies a proposed rewrite before any write (DESIGN.md §8.6).
// Every command that writes a vault file must call Verify and refuse on error.
package safety

import (
	"errors"

	"github.com/toppynl/vaulty/internal/config"
)

// Expect describes the only change a write is allowed to make.
type Expect struct {
	// Region in the original that may change, and where it ends in the new
	// content. For a created section: RegionStart = RegionEnd = len(orig).
	RegionStart, RegionEnd, NewRegionEnd int
	// Non-blank lines the new content gains (multiset). Nothing may be lost.
	Added []string
	// Frontmatter may differ only in the updated-key line.
	AllowUpdatedLine bool
	// Migrations (reorder only) also require equal blank-line counts.
	PreserveBlankCount bool
}

// Verify returns nil when next is orig with exactly the expected change.
func Verify(orig, next []byte, e Expect, cfg *config.Config) error {
	// TODO(step 4)
	return errors.New("not implemented")
}
