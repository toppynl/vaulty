package timeline

import (
	"github.com/toppynl/vaulty/internal/config"
	"github.com/toppynl/vaulty/internal/doc"
)

// DumpFile builds the parity-harness dump for one file (DESIGN.md §10.3).
// Returns nil when the page has no Timeline blocks (the parity dumper skips
// such files entirely, same as the Node oracle).
func DumpFile(relPath string, d *doc.Doc, cfg config.Timeline) map[string]interface{} {
	p := Parse(d, cfg)
	if len(p.Blocks) == 0 {
		return nil
	}
	blocks := make([]interface{}, len(p.Blocks))
	for i := range p.Blocks {
		blocks[i] = dumpBlock(&p.Blocks[i])
	}
	return map[string]interface{}{"path": relPath, "blocks": blocks}
}

func dumpBlock(b *Block) map[string]interface{} {
	if !b.Sortable() {
		return map[string]interface{}{"heading_line": b.HeadingLine, "ok": false}
	}
	order := ClassifyOrder(b.Entries)
	sortedEntries, sortedGaps := SortAscending(b.Entries, b.Gaps, order)
	sortedBody := SerializeBody(sortedEntries, sortedGaps, b.LeadingBlanks, b.TrailingBlanks)

	entries := make([]map[string]interface{}, len(b.Entries))
	for j, e := range b.Entries {
		entries[j] = map[string]interface{}{"key": e.Date.Key, "lines": e.Lines}
	}
	gaps := b.Gaps
	if gaps == nil {
		gaps = []int{}
	}
	return map[string]interface{}{
		"heading_line":    b.HeadingLine,
		"ok":              true,
		"order":           string(order),
		"leading_blanks":  b.LeadingBlanks,
		"trailing_blanks": b.TrailingBlanks,
		"gaps":            gaps,
		"entries":         entries,
		"sorted_body":     sortedBody,
	}
}
