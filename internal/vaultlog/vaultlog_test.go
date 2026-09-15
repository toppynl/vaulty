package vaultlog

import (
	"reflect"
	"testing"
)

func TestParseWellFormed(t *testing.T) {
	src := "## [2026-01-01] build | First entry\n\nSome body line.\nSecond body line.\n\n## [2026-01-02] fix | Second entry\n"
	entries, malformed := Parse([]byte(src))
	if len(malformed) != 0 {
		t.Fatalf("malformed = %+v, want none", malformed)
	}
	want := []Entry{
		{Line: 1, Date: "2026-01-01", Op: "build", Title: "First entry", Body: "Some body line.\nSecond body line."},
		{Line: 6, Date: "2026-01-02", Op: "fix", Title: "Second entry", Body: ""},
	}
	if !reflect.DeepEqual(entries, want) {
		t.Errorf("entries = %+v, want %+v", entries, want)
	}
}

func TestParseOpContainingBarePipe(t *testing.T) {
	// A bare "|" inside op (no surrounding spaces) is not the separator and
	// must not break parsing — matches a real pattern seen in the wild.
	src := "## [2026-01-01] decision|update | Some title\n"
	entries, malformed := Parse([]byte(src))
	if len(malformed) != 0 {
		t.Fatalf("malformed = %+v, want none", malformed)
	}
	if len(entries) != 1 || entries[0].Op != "decision|update" || entries[0].Title != "Some title" {
		t.Errorf("entries = %+v", entries)
	}
}

func TestParseMalformedNeverStops(t *testing.T) {
	src := "## broken heading, no date\n" +
		"## [2026-01-01] ok | Good entry\n" +
		"## [2026-13-40] op | bad date\n" +
		"## [2026-01-02] no-pipe-title\n" +
		"## [2026-01-03]  | empty op\n" +
		"## [2026-01-04] op | \n" +
		"## [2026-01-05] final | Last good entry\n"
	entries, malformed := Parse([]byte(src))
	if len(entries) != 2 {
		t.Fatalf("entries = %+v, want 2 well-formed entries", entries)
	}
	if entries[0].Title != "Good entry" || entries[1].Title != "Last good entry" {
		t.Errorf("entries = %+v", entries)
	}
	if len(malformed) != 5 {
		t.Fatalf("malformed = %+v, want 5", malformed)
	}
}

func TestParseEmpty(t *testing.T) {
	entries, malformed := Parse(nil)
	if len(entries) != 0 || len(malformed) != 0 {
		t.Errorf("entries=%v malformed=%v, want both empty", entries, malformed)
	}
}

func TestValidateField(t *testing.T) {
	cases := []struct {
		name, field, value string
		wantErr            bool
	}{
		{"ok op", "op", "build", false},
		{"ok op with digit and hyphen", "op", "vibe-review-2", false},
		{"empty op", "op", "  ", true},
		{"newline in op", "op", "bu\nild", true},
		{"cr in title", "title", "ti\rtle", true},
		{"pipe-sep in op", "op", "a | b", true},
		{"bare pipe in op rejected", "op", "decision|update", true},
		{"op with plus rejected", "op", "a+b", true},
		{"op with slash rejected", "op", "x/y", true},
		{"op with space rejected", "op", "two words", true},
		{"op with uppercase rejected", "op", "Build", true},
		{"ok body", "body", "one line", false},
		{"newline in body", "body", "line1\nline2", true},
		{"body is a real level-1 heading", "body", "# fake heading", true},
		{"body is a real level-2 heading (full entry shape)", "body", "## [2026-01-02] fake | injected", true},
		{"body heading indented up to 3 spaces still counts", "body", "   # still a heading", true},
		{"body issue reference is not a heading", "body", "#123 fixed", false},
		{"body containing a hash mid-line is fine", "body", "see issue #42", false},
		{"body starting with many hashes but no space is not a heading", "body", "###no-space", false},
	}
	for _, c := range cases {
		err := ValidateField(c.field, c.value)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: ValidateField(%q, %q) err=%v, wantErr=%v", c.name, c.field, c.value, err, c.wantErr)
		}
	}
}

func TestAppendToEmpty(t *testing.T) {
	got := Append(nil, "2026-01-01", "build", "Title", "")
	want := "## [2026-01-01] build | Title\n"
	if string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestAppendWithBody(t *testing.T) {
	got := Append([]byte("## [2026-01-01] build | First\n"), "2026-01-02", "fix", "Second", "a body line")
	want := "## [2026-01-01] build | First\n\n## [2026-01-02] fix | Second\na body line\n"
	if string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestAppendAddsSeparatorBlankLine(t *testing.T) {
	// Existing content with no trailing newline at all.
	got := Append([]byte("## [2026-01-01] build | First"), "2026-01-02", "fix", "Second", "")
	want := "## [2026-01-01] build | First\n\n## [2026-01-02] fix | Second\n"
	if string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestAppendSuffixNeverTouchesSrcBytes(t *testing.T) {
	// AppendSuffix must return only the bytes to add after src — src
	// concatenated with the suffix must equal what Append itself returns,
	// for every one of the three separator cases.
	cases := [][]byte{
		nil,
		[]byte("## [2026-01-01] build | First"), // no trailing newline
		[]byte("## [2026-01-01] build | First\n"),   // one trailing newline
		[]byte("## [2026-01-01] build | First\n\n"), // already blank-line-terminated
	}
	for _, src := range cases {
		suffix := AppendSuffix(src, "2026-01-02", "fix", "Second", "")
		got := append(append([]byte{}, src...), suffix...)
		want := Append(src, "2026-01-02", "fix", "Second", "")
		if string(got) != string(want) {
			t.Errorf("src=%q: src+AppendSuffix = %q, want %q", src, got, want)
		}
	}
}

func TestFindOutOfOrder(t *testing.T) {
	entries := []Entry{
		{Line: 1, Date: "2026-08-01"},
		{Line: 3, Date: "2026-08-05"},
		{Line: 5, Date: "2026-07-20"}, // out of order vs line 3
		{Line: 7, Date: "2026-07-20"}, // same date as previous: not out of order
		{Line: 9, Date: "2026-08-10"},
	}
	got := FindOutOfOrder(entries)
	if len(got) != 1 {
		t.Fatalf("got %+v, want 1 finding", got)
	}
	want := OutOfOrder{Line: 5, Date: "2026-07-20", PrevLine: 3, PrevDate: "2026-08-05"}
	if got[0] != want {
		t.Errorf("got %+v, want %+v", got[0], want)
	}
}

func TestSortByDateStable(t *testing.T) {
	entries := []Entry{
		{Line: 1, Date: "2026-08-05", Op: "a"},
		{Line: 3, Date: "2026-08-01", Op: "b"},
		{Line: 5, Date: "2026-08-05", Op: "c"}, // same date as line 1, later in file
	}
	got := SortByDate(entries)
	wantOrder := []int{3, 1, 5} // by date asc; the two 08-05 entries keep file order
	for i, w := range wantOrder {
		if got[i].Line != w {
			t.Errorf("got[%d].Line = %d, want %d (full: %+v)", i, got[i].Line, w, got)
		}
	}
	// Original slice untouched.
	if entries[0].Line != 1 {
		t.Errorf("SortByDate mutated its input: %+v", entries)
	}
}
