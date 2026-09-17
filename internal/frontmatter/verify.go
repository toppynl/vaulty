package frontmatter

import (
	"bytes"
	"fmt"
	"reflect"
	"slices"

	"gopkg.in/yaml.v3"

	"github.com/toppynl/vaulty/internal/doc"
)

func refusedCheck(check, format string, args ...any) error {
	return fmt.Errorf("safety check failed: %s: %s", check, fmt.Sprintf(format, args...))
}

// Verify re-checks an Apply result independently of the line editor before
// any write: the body is byte-identical, both frontmatters decode with
// yaml.v3, every key no op touched decodes to a deep-equal value, and every
// edited key decodes to what the ops (replayed on the decoded values, not
// the lines) say it should be.
func Verify(orig, next []byte, ops []Op, touchKey, today string, touched bool) error {
	od, nd := doc.Parse("", orig), doc.Parse("", next)
	if nd.FMUnclosed || !nd.HasFM {
		return refusedCheck("frontmatter", "result has no closed frontmatter")
	}
	if !bytes.Equal(orig[od.Body.Start:], next[nd.Body.Start:]) {
		return refusedCheck("body", "bytes after the frontmatter changed")
	}
	om, err := decodeMap(od)
	if err != nil {
		return refusedCheck("frontmatter", "original does not parse: %v", err)
	}
	nm, err := decodeMap(nd)
	if err != nil {
		return refusedCheck("frontmatter", "result does not parse: %v", err)
	}

	// Replay the ops on decoded values: absent{} = unset, nil = bare key,
	// string = scalar, []string = list.
	want := map[string]any{}
	edited := func(k string) bool { _, ok := want[k]; return ok }
	current := func(k string) any {
		if edited(k) {
			return want[k]
		}
		v, ok := om[k]
		if !ok {
			return absent{}
		}
		if list, ok := v.([]any); ok {
			out := []string{}
			for _, it := range list {
				s, _ := Stringify(it)
				out = append(out, s)
			}
			return out
		}
		if v == nil {
			return nil
		}
		s, _ := Stringify(v)
		return s
	}
	for _, op := range ops {
		switch op.Kind {
		case OpSet:
			want[op.Key] = op.Values[0]
		case OpUnset:
			want[op.Key] = absent{}
		case OpAdd, OpRemove:
			cur := current(op.Key)
			list, isList := cur.([]string)
			if op.Kind == OpRemove && !isList {
				want[op.Key] = cur // remove on a missing or bare key is a no-op
				continue
			}
			list = append([]string{}, list...)
			for _, v := range op.Values {
				if op.Kind == OpAdd && !slices.Contains(list, v) {
					list = append(list, v)
				}
				if op.Kind == OpRemove {
					list = slices.DeleteFunc(list, func(s string) bool { return s == v })
				}
			}
			want[op.Key] = list
		}
	}
	if touched {
		want[touchKey] = today
	}

	for k, v := range om {
		if !edited(k) && !reflect.DeepEqual(v, nm[k]) {
			return refusedCheck("frontmatter", "key %s changed", k)
		}
	}
	for k := range nm {
		if _, ok := om[k]; !ok && !edited(k) {
			return refusedCheck("frontmatter", "key %s appeared", k)
		}
	}
	for k, w := range want {
		got, present := nm[k]
		switch w := w.(type) {
		case absent:
			if present {
				return refusedCheck("frontmatter", "key %s still present", k)
			}
		case nil:
			if !present || got != nil {
				return refusedCheck("frontmatter", "key %s decodes to %v, want null", k, got)
			}
		case string:
			s, ok := Stringify(got)
			if !present || !ok || s != w {
				return refusedCheck("frontmatter", "key %s decodes to %v, want %q", k, got, w)
			}
		case []string:
			list, ok := got.([]any)
			if !present || !ok || len(list) != len(w) {
				return refusedCheck("frontmatter", "key %s decodes to %v, want %q", k, got, w)
			}
			for i, it := range list {
				if s, ok := Stringify(it); !ok || s != w[i] {
					return refusedCheck("frontmatter", "key %s item %d decodes to %v, want %q", k, i, it, w[i])
				}
			}
		}
	}
	return nil
}

type absent struct{}

func decodeMap(d *doc.Doc) (map[string]any, error) {
	m := map[string]any{}
	if !d.HasFM {
		return m, nil
	}
	if err := yaml.Unmarshal([]byte(Inner(d)), &m); err != nil {
		return nil, err
	}
	return m, nil
}
