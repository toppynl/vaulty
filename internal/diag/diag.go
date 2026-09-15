// Package diag is the shared finding/warning type (DESIGN.md §9).
package diag

type Severity string

const (
	Error   Severity = "error"
	Warning Severity = "warning"
	Off     Severity = "off" // only via config override; never emitted
)

type Code string

// Timeline codes. Parse emits them as warnings-with-severity; lint decides
// the exit code. See DESIGN.md §9 for the full table.
const (
	TL001MultipleTimelines Code = "TL001"
	TL002MissingDivider    Code = "TL002"
	TL003ContentAfter      Code = "TL003"
	TL004Forbidden         Code = "TL004"
	TL005NotAscending      Code = "TL005"
	TL006EntryFormat       Code = "TL006"
	TL007LooseLine         Code = "TL007"
	TL008PartialDate       Code = "TL008"
	TL009EmptyTimeline     Code = "TL009"
	TL010InvalidDate       Code = "TL010"
)

// Page codes (lint only; per-file = error, vault-wide = counts only).
const (
	PG001WorkMaterial      Code = "PG001"
	PG002CompiledTruthSize Code = "PG002"
)

// Document-level codes.
const (
	FM001UnterminatedFrontmatter Code = "FM001"
)

// Shard codes (lint only; DESIGN.md §16): hub/child hygiene for the
// hub-page-plus-children sharding convention (ingest skill's "Sharding"
// section). Always full findings in both files and vault mode — unlike
// PG001/PG002 they are not expected to be noisy at vault scale, so they are
// never collapsed into a vault-mode count.
const (
	SH001ChildWithoutHub Code = "SH001" // a hub directory has no sibling hub page
	SH002ChildMissingHub Code = "SH002" // a child's related: does not list its hub
	SH003HubMissingChild Code = "SH003" // a hub does not link one of its children
	SH004ChildOversized  Code = "SH004" // a child's compiled truth exceeds the size budget
	SH005TimelineInChild Code = "SH005" // a child carries its own '## Timeline'
)

// Diag is one finding. Line is 1-based (0 = whole file).
type Diag struct {
	Code     Code     `json:"code"`
	Severity Severity `json:"severity"`
	Path     string   `json:"path,omitempty"`
	Line     int      `json:"line"`
	Message  string   `json:"message"`
	// Blocking: the Timeline block cannot be safely re-serialized or
	// appended to (== the Node oracle's parseBlock returning ok:false).
	Blocking bool `json:"blocking,omitempty"`
}
