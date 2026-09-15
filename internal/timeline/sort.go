package timeline

// Order classifies entry order (oracle classifyOrder).
type Order string

const (
	Ascending  Order = "ascending"  // non-decreasing keys, incl. 0/1 entries and all-equal
	Descending Order = "descending" // non-increasing, not ascending
	Mixed      Order = "mixed"
)

// ClassifyOrder mirrors classifyOrder in the oracle.
func ClassifyOrder(entries []Entry) Order {
	nonInc, nonDec := true, true
	for i := 0; i+1 < len(entries); i++ {
		a, b := entries[i].Date.Key, entries[i+1].Date.Key
		if a < b {
			nonInc = false
		}
		if a > b {
			nonDec = false
		}
	}
	switch {
	case nonDec:
		return Ascending
	case nonInc:
		return Descending
	default:
		return Mixed
	}
}

// SortAscending mirrors sortAscending + tieBreakRanks in the oracle
// (DESIGN.md §5.5). Used by parity now and `vault migrate` later.
func SortAscending(entries []Entry, gaps []int, order Order) ([]Entry, []int) {
	// TODO(step 2)
	return entries, gaps
}

// SerializeBody mirrors serializeBlock: leading blanks, entries joined with
// gaps, trailing blanks, joined by "\n".
func SerializeBody(entries []Entry, gaps []int, leadingBlanks, trailingBlanks int) string {
	// TODO(step 2)
	return ""
}
