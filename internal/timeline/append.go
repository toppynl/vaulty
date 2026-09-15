package timeline

import (
	"errors"

	"github.com/toppynl/vaulty/internal/config"
)

// Position reports where Append put the entry.
type Position string

const (
	PosEnd        Position = "end"
	PosMiddle     Position = "middle"
	PosStart      Position = "start"
	PosNewSection Position = "new-section"
)

type AppendOptions struct {
	Touch bool   // set frontmatter updated: to Today
	Today string // YYYY-MM-DD; caller resolves $VAULT_TODAY / local date
}

type AppendResult struct {
	Line           int      `json:"line"` // 1-based line of the new entry in New
	Position       Position `json:"position"`
	AlreadyPresent bool     `json:"already_present"`
	CreatedSection bool     `json:"created_section"`
	Touched        bool     `json:"touched"` // updated: actually changed
	New            []byte   `json:"-"`
}

// ErrRefused wraps every reason Append declines to write (exit 3).
var ErrRefused = errors.New("refused")

// ValidateEntry normalizes and validates user input (DESIGN.md §8.1).
// Returns the entry's lines as they will be written.
func ValidateEntry(input string) (lines []string, date Date, err error) {
	// TODO(step 4)
	return nil, Date{}, errors.New("not implemented")
}

// Append computes the new file content; it does not write (DESIGN.md §8.2–8.6).
// The caller runs safety.Verify on (p.Doc.Src, res.New) before writing.
func Append(p *Page, entry string, opt AppendOptions, cfg *config.Config) (*AppendResult, error) {
	// TODO(step 4)
	return nil, errors.New("not implemented")
}
