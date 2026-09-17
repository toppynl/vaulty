package search

import (
	"errors"
	"testing"
)

// TestParseQueryRejects pins the exit-2 query errors that only show up after
// parsing or clause resolution (DESIGN.md §19.1, §19.4).
func TestParseQueryRejects(t *testing.T) {
	im, err := newMapping([]string{"standard"})
	if err != nil {
		t.Fatal(err)
	}
	raw := im.AnalyzerNamed(rawAnalyzer)
	for _, tc := range []struct {
		query   string
		wantErr bool
	}{
		{`!!!`, true},           // punctuation only: nothing searchable
		{`type:note !!!`, true}, // a filter doesn't excuse empty terms
		{`bluestne~3`, true},    // fuzzy distance must be 1 or 2
		{`"unterminated`, true}, // unterminated phrase
		{`bluestne~2`, false},   // pinned distance
		{`foo !!!`, false},      // one searchable term is enough
		{`type:note`, false},    // filter-only
	} {
		q, err := ParseQuery(tc.query)
		if err == nil {
			err = q.resolve(raw)
		}
		if got := errors.Is(err, ErrQuery); got != tc.wantErr {
			t.Errorf("%q: ErrQuery = %v (err %v), want %v", tc.query, got, err, tc.wantErr)
		}
	}
}
