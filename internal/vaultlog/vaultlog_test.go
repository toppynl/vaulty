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
		{"empty op", "op", "  ", true},
		{"newline in op", "op", "bu\nild", true},
		{"cr in title", "title", "ti\rtle", true},
		{"pipe-sep in op", "op", "a | b", true},
		{"bare pipe in op ok", "op", "decision|update", false},
		{"ok body", "body", "one line", false},
		{"newline in body", "body", "line1\nline2", true},
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
