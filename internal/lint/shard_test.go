package lint

import (
	"testing"

	"github.com/toppynl/vaulty/internal/doc"
)

func TestNormalizeWikilinkTarget(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"plain", "hub", "hub"},
		{"escaped table pipe", `hub\`, "hub"}, // regex already stops before the "|" itself
		{"path form", "things/hub/hub-a", "hub-a"},
		{"path form with type dir", "wiki/systems/hub", "hub"},
		{"md suffix", "hub.md", "hub"},
		{"uppercase", "Hub-D", "hub-d"},
		{"path plus md plus case", "Things/Hub/Hub-A.md", "hub-a"},
		{"surrounding space", "  hub  ", "hub"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeWikilinkTarget(tc.raw); got != tc.want {
				t.Errorf("normalizeWikilinkTarget(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestWikilinkTargets(t *testing.T) {
	text := "| a | [[hub-a\\|A]] |\n" +
		"| b | [[things/hub/hub-b]] |\n" +
		"| c | [[hub-c.md]] |\n" +
		"| d | [[Hub-D]] |\n"
	got := wikilinkTargets(text)
	for _, want := range []string{"hub-a", "hub-b", "hub-c", "hub-d"} {
		if !got[want] {
			t.Errorf("wikilinkTargets missing %q, got %v", want, got)
		}
	}
}

func TestRelatedField(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		wantHub string // hub name relatedListsHub should find, "" if none should match
	}{
		{
			name: "indented list",
			src: "---\n" +
				"title: x\n" +
				"related:\n" +
				"  - \"[[hub]]\"\n" +
				"---\n\nbody\n",
			wantHub: "hub",
		},
		{
			name: "unindented column-0 list",
			src: "---\n" +
				"title: x\n" +
				"related:\n" +
				"- \"[[hub]]\"\n" +
				"---\n\nbody\n",
			wantHub: "hub",
		},
		{
			name: "inline array",
			src: "---\n" +
				"title: x\n" +
				"related: [\"[[hub]]\"]\n" +
				"---\n\nbody\n",
			wantHub: "hub",
		},
		{
			name: "path form in related",
			src: "---\n" +
				"title: x\n" +
				"related:\n" +
				"- \"[[things/hub|Hub]]\"\n" +
				"---\n\nbody\n",
			wantHub: "hub",
		},
		{
			name: "alias form in related",
			src: "---\n" +
				"title: x\n" +
				"related:\n" +
				"- \"[[hub|Hub label]]\"\n" +
				"---\n\nbody\n",
			wantHub: "hub",
		},
		{
			name: "uppercase target in related",
			src: "---\n" +
				"title: x\n" +
				"related:\n" +
				"- \"[[Hub]]\"\n" +
				"---\n\nbody\n",
			wantHub: "hub",
		},
		{
			name: "related_x key is not related:",
			src: "---\n" +
				"title: x\n" +
				"related_extra:\n" +
				"- \"[[hub]]\"\n" +
				"related:\n" +
				"- \"[[other]]\"\n" +
				"---\n\nbody\n",
			wantHub: "other", // not "hub" — related_extra must not be picked up as related:
		},
		{
			name: "no related key",
			src: "---\n" +
				"title: x\n" +
				"---\n\nbody\n",
			wantHub: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := doc.Parse("wiki/things/hub/x.md", []byte(tc.src))
			text, _ := relatedField(d)
			if tc.wantHub == "" {
				if text != "" && relatedListsHub(text, "hub") {
					t.Errorf("relatedField/relatedListsHub unexpectedly matched %q", text)
				}
				return
			}
			if !relatedListsHub(text, tc.wantHub) {
				t.Errorf("relatedListsHub(%q, %q) = false, want true", text, tc.wantHub)
			}
			// related_x case: also assert the wrong hub name is NOT matched.
			if tc.name == "related_x key is not related:" && relatedListsHub(text, "hub") {
				t.Errorf("relatedListsHub matched the related_extra: value, not related:")
			}
		})
	}
}

func TestIsRelatedKeyLine(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{"related:", true},
		{"related: [\"[[hub]]\"]", true},
		{"related:\t[\"[[hub]]\"]", true},
		{"related_extra:", false},
		{"related_extra: foo", false},
		{"relatedness: foo", false},
		{"unrelated: foo", false},
	}
	for _, tc := range cases {
		if got := isRelatedKeyLine(tc.raw); got != tc.want {
			t.Errorf("isRelatedKeyLine(%q) = %v, want %v", tc.raw, got, tc.want)
		}
	}
}

func TestIsRelatedContinuationLine(t *testing.T) {
	cases := []struct {
		l    string
		want bool
	}{
		{"", true},
		{"  - \"[[hub]]\"", true},
		{"\t- \"[[hub]]\"", true},
		{"- \"[[hub]]\"", true},
		{"-", true},
		{"title: x", false},
		{"---", false},
	}
	for _, tc := range cases {
		if got := isRelatedContinuationLine(tc.l); got != tc.want {
			t.Errorf("isRelatedContinuationLine(%q) = %v, want %v", tc.l, got, tc.want)
		}
	}
}
