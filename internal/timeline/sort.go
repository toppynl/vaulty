package timeline

import (
	"sort"
	"strings"
)

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

// tieBreakRanks mirrors the oracle's tieBreakRanks: same-date entries in a
// locally descending run get flipped, entries in a locally ascending run
// (or with no determinable local context) keep original order.
func tieBreakRanks(entries []Entry) []int {
	n := len(entries)
	keys := make([]string, n)
	for i, e := range entries {
		keys[i] = e.Date.Key
	}

	flipByKey := make(map[string]bool, n)
	for i := 0; i < n; i++ {
		key := keys[i]
		if _, ok := flipByKey[key]; ok {
			continue
		}
		var prevKey, nextKey string
		havePrev, haveNext := false, false
		for j := i - 1; j >= 0; j-- {
			if keys[j] != key {
				prevKey, havePrev = keys[j], true
				break
			}
		}
		for j := i + 1; j < n; j++ {
			if keys[j] != key {
				nextKey, haveNext = keys[j], true
				break
			}
		}
		flip := false
		if havePrev {
			flip = prevKey > key
		} else if haveNext {
			flip = nextKey < key
		}
		flipByKey[key] = flip
	}

	total := make(map[string]int, n)
	for _, k := range keys {
		total[k]++
	}
	seen := make(map[string]int, n)
	ranks := make([]int, n)
	for i, k := range keys {
		s := seen[k]
		seen[k] = s + 1
		if flipByKey[k] {
			ranks[i] = total[k] - 1 - s
		} else {
			ranks[i] = s
		}
	}
	return ranks
}

// SortAscending mirrors sortAscending + tieBreakRanks in the oracle
// (DESIGN.md §5.5). Used by parity now and `vault migrate` later.
func SortAscending(entries []Entry, gaps []int, order Order) ([]Entry, []int) {
	if order == Descending {
		re := make([]Entry, len(entries))
		for i, e := range entries {
			re[len(entries)-1-i] = e
		}
		rg := make([]int, len(gaps))
		for i, g := range gaps {
			rg[len(gaps)-1-i] = g
		}
		return re, rg
	}

	ranks := tieBreakRanks(entries)
	type ranked struct {
		e    Entry
		rank int
	}
	tmp := make([]ranked, len(entries))
	for i, e := range entries {
		tmp[i] = ranked{e, ranks[i]}
	}
	sort.SliceStable(tmp, func(i, j int) bool {
		if tmp[i].e.Date.Key != tmp[j].e.Date.Key {
			return tmp[i].e.Date.Key < tmp[j].e.Date.Key
		}
		return tmp[i].rank < tmp[j].rank
	})
	out := make([]Entry, len(entries))
	for i, x := range tmp {
		out[i] = x.e
	}
	return out, append([]int(nil), gaps...)
}

// SerializeBody mirrors serializeBlock: leading blanks, entries joined with
// gaps, trailing blanks, joined by "\n".
func SerializeBody(entries []Entry, gaps []int, leadingBlanks, trailingBlanks int) string {
	var parts []string
	for k := 0; k < leadingBlanks; k++ {
		parts = append(parts, "")
	}
	for idx, e := range entries {
		parts = append(parts, e.Lines...)
		if idx < len(entries)-1 {
			gap := 0
			if idx < len(gaps) {
				gap = gaps[idx]
			}
			for k := 0; k < gap; k++ {
				parts = append(parts, "")
			}
		}
	}
	for k := 0; k < trailingBlanks; k++ {
		parts = append(parts, "")
	}
	return strings.Join(parts, "\n")
}
