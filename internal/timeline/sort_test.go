package timeline

import (
	"reflect"
	"testing"
)

func entriesFromKeys(keys ...string) []Entry {
	out := make([]Entry, len(keys))
	for i, k := range keys {
		out[i] = Entry{Line: i + 1, Date: Date{Key: k}, Lines: []string{"- **" + k + "** | x — y"}}
	}
	return out
}

func keysOf(entries []Entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Date.Key
	}
	return out
}

func TestClassifyOrder(t *testing.T) {
	cases := []struct {
		keys []string
		want Order
	}{
		{[]string{}, Ascending},
		{[]string{"2026-01-01"}, Ascending},
		{[]string{"2026-01-01", "2026-01-01"}, Ascending},
		{[]string{"2026-01-01", "2026-01-02", "2026-01-03"}, Ascending},
		{[]string{"2026-01-03", "2026-01-02", "2026-01-01"}, Descending},
		{[]string{"2026-01-03", "2026-01-03", "2026-01-01"}, Descending},
		{[]string{"2026-01-01", "2026-01-03", "2026-01-02"}, Mixed},
	}
	for _, c := range cases {
		got := ClassifyOrder(entriesFromKeys(c.keys...))
		if got != c.want {
			t.Errorf("ClassifyOrder(%v) = %s, want %s", c.keys, got, c.want)
		}
	}
}

func TestSortAscendingPureDescending(t *testing.T) {
	entries := entriesFromKeys("2026-01-03", "2026-01-02", "2026-01-01")
	gaps := []int{1, 0}
	got, gotGaps := SortAscending(entries, gaps, Descending)
	want := []string{"2026-01-01", "2026-01-02", "2026-01-03"}
	if !reflect.DeepEqual(keysOf(got), want) {
		t.Errorf("keys = %v, want %v", keysOf(got), want)
	}
	if !reflect.DeepEqual(gotGaps, []int{0, 1}) {
		t.Errorf("gaps = %v, want [0 1]", gotGaps)
	}
}

func TestSortAscendingMixedSameDateDescendingContext(t *testing.T) {
	// A same-date group sitting between two neighbours that put it in a
	// locally descending run gets its relative order flipped.
	entries := entriesFromKeys("2026-01-05", "2026-01-03", "2026-01-03", "2026-01-01")
	got, _ := SortAscending(entries, []int{0, 0, 0}, Mixed)
	// After ascending sort by key: 01-01, then the 01-03 group (flipped
	// because its neighbours put it in a descending run: prev=05 > 03),
	// then 01-05.
	wantKeys := []string{"2026-01-01", "2026-01-03", "2026-01-03", "2026-01-05"}
	if !reflect.DeepEqual(keysOf(got), wantKeys) {
		t.Fatalf("keys = %v, want %v", keysOf(got), wantKeys)
	}
	// The two 03 entries had original relative order (Line 2, Line 3);
	// flip means Line 3 (originally second of the pair) now sorts first.
	if got[1].Line != 3 || got[2].Line != 2 {
		t.Errorf("flip order: got lines %d,%d want 3,2", got[1].Line, got[2].Line)
	}
}

func TestSortAscendingMixedSameDateAscendingContext(t *testing.T) {
	// A same-date group whose neighbours put it in a locally ascending run
	// keeps its original relative (appearance) order.
	entries := entriesFromKeys("2026-01-01", "2026-01-03", "2026-01-03", "2026-01-05")
	got, _ := SortAscending(entries, []int{0, 0, 0}, Mixed)
	wantKeys := []string{"2026-01-01", "2026-01-03", "2026-01-03", "2026-01-05"}
	if !reflect.DeepEqual(keysOf(got), wantKeys) {
		t.Fatalf("keys = %v, want %v", keysOf(got), wantKeys)
	}
	if got[1].Line != 2 || got[2].Line != 3 {
		t.Errorf("no-flip order: got lines %d,%d want 2,3", got[1].Line, got[2].Line)
	}
}

func TestSortAscendingMixedRunsBothDirections(t *testing.T) {
	// A block with one descending-context group and one ascending-context
	// group in the same mixed sort.
	entries := entriesFromKeys(
		"2026-01-10", // 0
		"2026-01-05", // 1 \_ desc context (prev 10 > 05)
		"2026-01-05", // 2 /
		"2026-01-01", // 3
		"2026-01-06", // 4 \_ asc context (prev 01 < 06)
		"2026-01-06", // 5 /
		"2026-01-08", // 6
	)
	got, _ := SortAscending(entries, make([]int, len(entries)-1), Mixed)
	wantKeys := []string{
		"2026-01-01", "2026-01-05", "2026-01-05", "2026-01-06", "2026-01-06", "2026-01-08", "2026-01-10",
	}
	if !reflect.DeepEqual(keysOf(got), wantKeys) {
		t.Fatalf("keys = %v, want %v", keysOf(got), wantKeys)
	}
	// 05-group: descending context -> flipped (lines 2 then 1).
	idx := 1
	if got[idx].Line != 3 || got[idx+1].Line != 2 {
		t.Errorf("05-group flip: got lines %d,%d want 3,2", got[idx].Line, got[idx+1].Line)
	}
	// 06-group: ascending context -> original order (lines 5 then 6).
	idx = 3
	if got[idx].Line != 5 || got[idx+1].Line != 6 {
		t.Errorf("06-group no-flip: got lines %d,%d want 5,6", got[idx].Line, got[idx+1].Line)
	}
}

func TestSerializeBodyRoundTrip(t *testing.T) {
	entries := []Entry{
		{Lines: []string{"- **2026-08-01** | x — y", "  continued."}},
		{Lines: []string{"- **2026-08-03** | x — y"}},
	}
	got := SerializeBody(entries, []int{1}, 1, 1)
	want := "\n- **2026-08-01** | x — y\n  continued.\n\n- **2026-08-03** | x — y\n"
	if got != want {
		t.Errorf("SerializeBody = %q, want %q", got, want)
	}
}

func TestSerializeBodyNoBlanks(t *testing.T) {
	entries := []Entry{
		{Lines: []string{"- **2026-08-01** | x — y"}},
		{Lines: []string{"- **2026-08-03** | x — y"}},
	}
	got := SerializeBody(entries, []int{0}, 0, 0)
	want := "- **2026-08-01** | x — y\n- **2026-08-03** | x — y"
	if got != want {
		t.Errorf("SerializeBody = %q, want %q", got, want)
	}
}
