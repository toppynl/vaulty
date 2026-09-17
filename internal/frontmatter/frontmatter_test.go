package frontmatter

import (
	"errors"
	"strings"
	"testing"

	"github.com/toppynl/vaulty/internal/doc"
)

const page = `---
title: "vaulty"
owner: "[[peep]]"
status: developing # lifecycle
updated: 2026-09-17
tags: [vault-tooling, cli]
related: ["[[peep]]", "[[me-template]]"]
repos: ["/var/www/lib/vaulty — git@github.com:toppynl/vaulty.git"]
# comment between keys
links:
  - "[[stocky-oms]]"
  # inner comment
  - "[[zo-geregeld]]"
aliases:
- persona
notes: |
  free text
---

# Body
`

func mustApply(t *testing.T, src string, touch string, ops ...Op) *Result {
	t.Helper()
	res, err := Apply([]byte(src), ops, touch, "2026-09-15")
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(res.Changed) > 0 {
		if err := Verify([]byte(src), res.New, ops, touch, "2026-09-15", res.Touched); err != nil {
			t.Fatalf("Verify: %v", err)
		}
	}
	return res
}

// wantDiff asserts New equals src with each old line replaced by new
// ("" new = line deleted).
func wantDiff(t *testing.T, src string, res *Result, repl ...string) {
	t.Helper()
	want := src
	for i := 0; i < len(repl); i += 2 {
		old, nw := repl[i]+"\n", repl[i+1]
		if nw != "" {
			nw += "\n"
		}
		if !strings.Contains(want, old) {
			t.Fatalf("test bug: %q not in src", old)
		}
		want = strings.Replace(want, old, nw, 1)
	}
	if string(res.New) != want {
		t.Errorf("New =\n%s\nwant\n%s", res.New, want)
	}
}

func TestParseShapes(t *testing.T) {
	b := Parse(Inner(docOf(page)))
	cases := map[string]Shape{
		"title": ShapeScalar, "owner": ShapeScalar, "status": ShapeScalar, "updated": ShapeScalar,
		"tags": ShapeFlow, "related": ShapeFlow, "repos": ShapeFlow,
		"links": ShapeBlock, "aliases": ShapeBlock, "notes": ShapeUnsupported,
	}
	for k, want := range cases {
		e := b.Find(k)
		if e == nil || e.Shape != want {
			t.Errorf("%s: entry %+v, want shape %d", k, e, want)
		}
	}
	if e := b.Find("owner"); e.Decoded != "[[peep]]" || e.Quote != '"' {
		t.Errorf("owner decoded %q quote %q", e.Decoded, e.Quote)
	}
	if e := b.Find("updated"); e.Decoded != "2026-09-17" {
		t.Errorf("updated decoded %q", e.Decoded)
	}
	if e := b.Find("links"); len(e.Items) != 2 || e.Items[1].Decoded != "[[zo-geregeld]]" {
		t.Errorf("links items %+v", e.Items)
	}
}

func TestSetKeepsQuotesAndComment(t *testing.T) {
	res := mustApply(t, page, "",
		Op{Kind: OpSet, Key: "title", Values: []string{"vaulty cli"}},
		Op{Kind: OpSet, Key: "status", Values: []string{"mature"}},
		Op{Kind: OpSet, Key: "owner", Values: []string{"robin"}},
	)
	wantDiff(t, page, res,
		`title: "vaulty"`, `title: "vaulty cli"`,
		`status: developing # lifecycle`, `status: mature # lifecycle`,
		`owner: "[[peep]]"`, `owner: "robin"`,
	)
}

func TestSetNewKeyQuotesWhenNeeded(t *testing.T) {
	for v, want := range map[string]string{
		"[[x]]": `"[[x]]"`, "a: b": `"a: b"`, "": `""`, "null": `"null"`, "#x": `"#x"`,
		"plain words": "plain words", "2026-09-17": "2026-09-17", "42": "42", `say "hi"`: `say "hi"`,
	} {
		res := mustApply(t, page, "", Op{Kind: OpSet, Key: "k", Values: []string{v}})
		wantDiff(t, page, res, "  free text", "  free text\nk: "+want)
	}
}

func TestSetOnListRefused(t *testing.T) {
	_, err := Apply([]byte(page), []Op{{Kind: OpSet, Key: "tags", Values: []string{"x"}}}, "", "")
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("err = %v, want refused", err)
	}
}

func TestAddFlowDedupeMatchesQuoting(t *testing.T) {
	res := mustApply(t, page, "",
		Op{Kind: OpAdd, Key: "tags", Values: []string{"cli", "go", "go"}},
		Op{Kind: OpAdd, Key: "related", Values: []string{"[[peep]]", "robin"}},
	)
	wantDiff(t, page, res,
		`tags: [vault-tooling, cli]`, `tags: [vault-tooling, cli, go]`,
		`related: ["[[peep]]", "[[me-template]]"]`, `related: ["[[peep]]", "[[me-template]]", "robin"]`,
	)
}

func TestAddBlockKeepsIndentAndComments(t *testing.T) {
	res := mustApply(t, page, "",
		Op{Kind: OpAdd, Key: "links", Values: []string{"[[holler]]"}},
		Op{Kind: OpAdd, Key: "aliases", Values: []string{"brain"}},
	)
	wantDiff(t, page, res,
		`  - "[[zo-geregeld]]"`, "  - \"[[zo-geregeld]]\"\n  - \"[[holler]]\"",
		`- persona`, "- persona\n- brain",
	)
}

func TestRemoveLastItems(t *testing.T) {
	res := mustApply(t, page, "",
		Op{Kind: OpRemove, Key: "tags", Values: []string{"cli", "vault-tooling"}},
		Op{Kind: OpRemove, Key: "links", Values: []string{"[[stocky-oms]]", "[[zo-geregeld]]", "absent"}},
	)
	wantDiff(t, page, res,
		`tags: [vault-tooling, cli]`, `tags: []`,
		"links:", "links: []",
		`  - "[[stocky-oms]]"`, "",
		`  - "[[zo-geregeld]]"`, "",
	)
}

func TestUnsetBlockListKeepsFollowingComment(t *testing.T) {
	res := mustApply(t, page, "", Op{Kind: OpUnset, Key: "links"}, Op{Kind: OpUnset, Key: "missing"})
	wantDiff(t, page, res,
		"links:", "",
		`  - "[[stocky-oms]]"`, "",
		"  # inner comment", "",
		`  - "[[zo-geregeld]]"`, "",
	)
	if strings.Join(res.Unchanged, ",") != "missing" {
		t.Errorf("unchanged = %v", res.Unchanged)
	}
}

func TestUnsupportedShapeRefused(t *testing.T) {
	src := "---\nnotes: |\n  text\nmap:\n  a: b\nmulti: [a,\n  b]\n---\n"
	for _, k := range []string{"notes", "map", "multi"} {
		_, err := Apply([]byte(src), []Op{{Kind: OpUnset, Key: k}}, "", "")
		if err == nil || !strings.Contains(err.Error(), "unsupported YAML shape for key "+k) {
			t.Errorf("%s: err = %v", k, err)
		}
	}
	// Exotic neighbours survive an edit byte-for-byte.
	res := mustApply(t, src, "", Op{Kind: OpSet, Key: "status", Values: []string{"x"}})
	if string(res.New) != "---\nnotes: |\n  text\nmap:\n  a: b\nmulti: [a,\n  b]\nstatus: x\n---\n" {
		t.Errorf("New = %q", res.New)
	}
}

func TestTouchAndNoFrontmatter(t *testing.T) {
	res := mustApply(t, "# Body\n", "updated", Op{Kind: OpAdd, Key: "tags", Values: []string{"a"}})
	if string(res.New) != "---\ntags: [a]\nupdated: 2026-09-15\n---\n# Body\n" || !res.Touched {
		t.Errorf("New = %q touched=%v", res.New, res.Touched)
	}
	res = mustApply(t, page, "updated", Op{Kind: OpSet, Key: "status", Values: []string{"developing"}})
	if len(res.Changed) != 0 || string(res.New) != page {
		t.Errorf("no-op touched: %v", res.Changed)
	}
}

func TestVerifyCatchesCollateralChange(t *testing.T) {
	ops := []Op{{Kind: OpSet, Key: "status", Values: []string{"mature"}}}
	bad := strings.Replace(strings.Replace(page, "developing", "mature", 1), "[vault-tooling, cli]", "[cli]", 1)
	if err := Verify([]byte(page), []byte(bad), ops, "", "", false); err == nil || !strings.Contains(err.Error(), "key tags changed") {
		t.Errorf("err = %v", err)
	}
	badBody := strings.Replace(page, "developing", "mature", 1) + "x"
	if err := Verify([]byte(page), []byte(badBody), ops, "", "", false); err == nil {
		t.Error("body change not caught")
	}
}

func docOf(src string) *doc.Doc { return doc.Parse("", []byte(src)) }
