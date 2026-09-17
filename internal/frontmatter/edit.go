package frontmatter

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"

	"github.com/toppynl/vaulty/internal/doc"
)

// ErrRefused wraps every reason an edit declines to write (exit 3).
var ErrRefused = errors.New("refused")

// OpKind is one frontmatter write operation.
type OpKind int

const (
	OpSet OpKind = iota
	OpAdd
	OpRemove
	OpUnset
)

// Op edits one key. Set uses Values[0]; Add/Remove use all Values; Unset
// uses none.
type Op struct {
	Kind   OpKind
	Key    string
	Values []string
}

// Result is the outcome of Apply.
type Result struct {
	New       []byte
	Changed   []string // keys whose lines changed, incl. the touched key
	Unchanged []string // requested keys that needed no change
	Touched   bool
}

// Apply runs ops against src's frontmatter in order. With touchKey set, a
// write that changes anything also sets touchKey to today — unless an op
// already targets touchKey (an explicit edit wins). A no-op returns src
// itself and no Changed keys.
func Apply(src []byte, ops []Op, touchKey, today string) (*Result, error) {
	d := doc.Parse("", src)
	if d.FMUnclosed {
		return nil, fmt.Errorf("%w: frontmatter is not closed", ErrRefused)
	}
	inner := Inner(d)
	if d.HasFM {
		var m map[string]any
		if err := yaml.Unmarshal([]byte(inner), &m); err != nil {
			return nil, fmt.Errorf("%w: frontmatter is not valid YAML: %v", ErrRefused, err)
		}
	}

	res := &Result{}
	changed := map[string]bool{}
	var order []string
	for _, op := range ops {
		if !slices.Contains(order, op.Key) {
			order = append(order, op.Key)
		}
		next, ch, err := applyOp(inner, op)
		if err != nil {
			return nil, err
		}
		inner = next
		changed[op.Key] = changed[op.Key] || ch
	}
	for _, k := range order {
		if changed[k] {
			res.Changed = append(res.Changed, k)
		} else {
			res.Unchanged = append(res.Unchanged, k)
		}
	}
	if len(res.Changed) == 0 {
		res.New = src
		return res, nil
	}
	if touchKey != "" && !slices.Contains(order, touchKey) {
		next, ch, err := applyOp(inner, Op{Kind: OpSet, Key: touchKey, Values: []string{today}})
		if err != nil {
			return nil, err
		}
		if ch {
			inner = next
			res.Touched = true
			res.Changed = append(res.Changed, touchKey)
		}
	}

	if !d.HasFM {
		res.New = append([]byte("---\n"+inner+"---\n"), src...)
		return res, nil
	}
	res.New = splice(src, d, inner)
	return res, nil
}

// Inner returns the frontmatter text between the '---' lines ("" when the
// page has none).
func Inner(d *doc.Doc) string {
	if !d.HasFM {
		return ""
	}
	fm := string(d.Src[d.Frontmatter.Start:d.Frontmatter.End])
	open := strings.IndexByte(fm, '\n') + 1
	closeAt := strings.LastIndex(strings.TrimSuffix(fm, "\n"), "\n") + 1
	if closeAt < open {
		return ""
	}
	return fm[open:closeAt]
}

// splice replaces the inner frontmatter text, keeping both delimiter lines
// and the body byte-for-byte.
func splice(src []byte, d *doc.Doc, inner string) []byte {
	fm := string(src[d.Frontmatter.Start:d.Frontmatter.End])
	open := strings.IndexByte(fm, '\n') + 1
	closeAt := strings.LastIndex(strings.TrimSuffix(fm, "\n"), "\n") + 1
	if closeAt < open {
		closeAt = open
	}
	out := append([]byte(nil), src[:d.Frontmatter.Start+open]...)
	out = append(out, inner...)
	out = append(out, src[d.Frontmatter.Start+closeAt:]...)
	return out
}

func unsupported(key string) error {
	return fmt.Errorf("%w: unsupported YAML shape for key %s", ErrRefused, key)
}

func applyOp(inner string, op Op) (string, bool, error) {
	b := Parse(inner)
	e := b.Find(op.Key)
	if e != nil && e.Shape == ShapeUnsupported {
		return "", false, unsupported(op.Key)
	}
	switch op.Kind {
	case OpSet:
		return b.set(e, op.Key, op.Values[0])
	case OpAdd:
		return b.add(e, op.Key, op.Values)
	case OpRemove:
		return b.remove(e, op.Key, op.Values)
	case OpUnset:
		if e == nil {
			return inner, false, nil
		}
		b.Lines = slices.Delete(b.Lines, e.Start, e.End)
		return b.Text(), true, nil
	}
	return "", false, fmt.Errorf("unknown op %d", op.Kind)
}

func (b *Block) set(e *Entry, key, v string) (string, bool, error) {
	if e == nil {
		b.Lines = append(b.Lines, key+": "+Render(v, 0, false))
		return b.Text(), true, nil
	}
	if e.Shape != ShapeScalar {
		return "", false, fmt.Errorf("%w: key %s is a list (use add/remove)", ErrRefused, key)
	}
	if e.Decoded == v && (!e.Empty || v == "") {
		return b.Text(), false, nil
	}
	sep := e.Sep
	if sep == "" {
		sep = " "
	}
	b.Lines[e.Start] = key + ":" + sep + Render(v, e.Quote, false) + e.Comment
	return b.Text(), true, nil
}

func (b *Block) add(e *Entry, key string, values []string) (string, bool, error) {
	if e != nil && e.Shape == ShapeScalar && !e.Empty {
		return "", false, fmt.Errorf("%w: key %s is a scalar, not a list", ErrRefused, key)
	}
	var have []string
	if e != nil {
		for _, it := range e.Items {
			have = append(have, it.Decoded)
		}
	}
	var fresh []string
	for _, v := range values {
		if !slices.Contains(have, v) && !slices.Contains(fresh, v) {
			fresh = append(fresh, v)
		}
	}
	if len(fresh) == 0 {
		return b.Text(), false, nil
	}
	quote := listQuote(e)

	switch {
	case e == nil || e.Shape == ShapeScalar: // missing or bare `key:`
		var raws []string
		for _, v := range fresh {
			raws = append(raws, Render(v, quote, true))
		}
		line := key + ": [" + strings.Join(raws, ", ") + "]"
		if e == nil {
			b.Lines = append(b.Lines, line)
		} else {
			b.Lines[e.Start] = line
		}
	case e.Shape == ShapeFlow:
		raws := itemRaws(e.Items)
		for _, v := range fresh {
			raws = append(raws, Render(v, quote, true))
		}
		b.Lines[e.Start] = flowLine(e, raws)
	case e.Shape == ShapeBlock:
		last := e.Items[len(e.Items)-1]
		var add []string
		for _, v := range fresh {
			add = append(add, last.Prefix+Render(v, quote, false))
		}
		b.Lines = slices.Insert(b.Lines, last.Line+1, add...)
	}
	return b.Text(), true, nil
}

func (b *Block) remove(e *Entry, key string, values []string) (string, bool, error) {
	if e == nil || e.Shape == ShapeScalar && e.Empty {
		return b.Text(), false, nil
	}
	if e.Shape == ShapeScalar {
		return "", false, fmt.Errorf("%w: key %s is a scalar, not a list", ErrRefused, key)
	}
	var keep []Item
	for _, it := range e.Items {
		if !slices.Contains(values, it.Decoded) {
			keep = append(keep, it)
		}
	}
	if len(keep) == len(e.Items) {
		return b.Text(), false, nil
	}
	if e.Shape == ShapeFlow {
		b.Lines[e.Start] = flowLine(e, itemRaws(keep))
		return b.Text(), true, nil
	}
	// Block: delete removed item lines bottom-up; comment lines stay.
	for i := len(e.Items) - 1; i >= 0; i-- {
		if !slices.Contains(keep, e.Items[i]) {
			b.Lines = slices.Delete(b.Lines, e.Items[i].Line, e.Items[i].Line+1)
		}
	}
	if len(keep) == 0 {
		b.Lines[e.Start] = key + ": []"
	}
	return b.Text(), true, nil
}

func itemRaws(items []Item) []string {
	var raws []string
	for _, it := range items {
		raws = append(raws, it.Raw)
	}
	return raws
}

// flowLine rebuilds a flow sequence line with the entry's own spacing.
func flowLine(e *Entry, raws []string) string {
	if len(raws) == 0 {
		return strings.TrimRight(e.FlowHead, " \t") + strings.TrimLeft(e.FlowTail, " \t")
	}
	return e.FlowHead + strings.Join(raws, e.FlowSep) + e.FlowTail
}

// listQuote picks the quoting for new items so they match the existing
// ones: double if any item is double-quoted, else single if any is single-
// quoted, else plain where safe.
func listQuote(e *Entry) byte {
	if e == nil {
		return 0
	}
	var q byte
	for _, it := range e.Items {
		if it.Quote == '"' {
			return '"'
		}
		if it.Quote == '\'' {
			q = '\''
		}
	}
	return q
}

// Render writes v as a YAML scalar. A double or single quote byte forces that style
// (single falls back to double when v has characters single quotes can't
// hold); 0 writes v plain when it is safe to, double-quoted otherwise. flow
// marks a flow-sequence item, where ",[]{}" also need quoting.
func Render(v string, quote byte, flow bool) string {
	switch {
	case quote == '\'' && printable(v):
		return "'" + strings.ReplaceAll(v, "'", "''") + "'"
	case quote == 0 && plainSafe(v, flow):
		return v
	}
	return doubleQuote(v)
}

func printable(v string) bool {
	for _, r := range v {
		if r < 0x20 || r == 0x7f || !unicode.IsPrint(r) && r != ' ' {
			return false
		}
	}
	return true
}

// plainSafe: v has no character that starts or changes YAML syntax, and it
// decodes back to itself (so "null", "~" or "1e3" get quoted, while
// "developing", "2026-09-17", "true" and "42" stay plain).
func plainSafe(v string, flow bool) bool {
	if v == "" || !printable(v) || strings.TrimSpace(v) != v {
		return false
	}
	if strings.ContainsRune("[{\"'*&!|>%@`#", rune(v[0])) {
		return false
	}
	if strings.Contains(v, ": ") || strings.Contains(v, " #") || strings.HasSuffix(v, ":") {
		return false
	}
	if flow && strings.ContainsAny(v, ",[]{}") {
		return false
	}
	var got any
	if flow {
		var list []any
		if yaml.Unmarshal([]byte("["+v+"]"), &list) != nil || len(list) != 1 {
			return false
		}
		got = list[0]
	} else {
		var m map[string]any
		if yaml.Unmarshal([]byte("k: "+v), &m) != nil || len(m) != 1 {
			return false
		}
		got = m["k"]
	}
	s, ok := Stringify(got)
	return ok && got != nil && s == v
}

func doubleQuote(v string) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for _, r := range v {
		switch r {
		case '"':
			sb.WriteString(`\"`)
		case '\\':
			sb.WriteString(`\\`)
		case '\n':
			sb.WriteString(`\n`)
		case '\t':
			sb.WriteString(`\t`)
		case '\r':
			sb.WriteString(`\r`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&sb, `\x%02X`, r)
			} else {
				sb.WriteRune(r)
			}
		}
	}
	sb.WriteByte('"')
	return sb.String()
}
