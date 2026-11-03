package fzf

import (
	"slices"
	"strings"
	"testing"
)

// TestSortBlankQueryKeepsAll: a blank (or spaces-only) query keeps every
// item in its order, even ones a real query would drop.
func TestSortBlankQueryKeepsAll(t *testing.T) {
	in := []string{"zeta", "alpha", "beta"}
	for _, q := range []string{"", "   "} {
		got := Sort(q, in, func(s string) string { return s })
		if len(got) != 3 || got[0] != "zeta" || got[1] != "alpha" || got[2] != "beta" {
			t.Errorf("Sort(%q) = %v", q, got)
		}
	}
}

func TestMatch(t *testing.T) {
	for _, c := range []struct {
		q, hay string
		ok     bool
	}{
		{"", "anything", true},
		{"lgn", "Fix login", true},
		{"lgn ^fix", "Fix login", true},
		{"'gin !retry", "Fix login retry", false},
		{"Login", "fix login", false}, // smart case
		{"redis | login", "Fix login", true},
		{"redis | mysql", "Fix login", false},
		{"redis | login ^nope", "Fix login", false},
		{"login$", "Fix login", true},
		{"fix$", "Fix login", false},
		{"lgn !retry", "Fix login", true},
		{"lgn ! '", "Fix login", true}, // a bare ! or ' asks nothing
		{"cafe", "café", false},        // no accent folding
	} {
		if _, ok := Match(c.q, c.hay); ok != c.ok {
			t.Errorf("Match(%q, %q) = %v", c.q, c.hay, ok)
		}
	}
	for q, want := range map[string][]int{"lgn": {4, 6, 8}, "'LOG": {4, 5, 6}, "xyz": nil, "": nil, "fix loginx": {0, 1, 2}} {
		if got := Runes(q, "Fix login"); !slices.Equal(got, want) {
			t.Errorf("Runes(%q) = %v, want %v", q, got, want)
		}
	}
	got := Sort("acm", []string{"Initech", "Placeacme", "Acme", "Globex"}, func(s string) string { return s })
	if !slices.Equal(got, []string{"Acme", "Placeacme"}) {
		t.Errorf("Sort: %q", got)
	}
}

// TestMatchLongTermScansForward: a term past 1000 runes falls back to fzf's
// greedy match, which takes the first occurrence from the left.
func TestMatchLongTermScansForward(t *testing.T) {
	b := strings.Repeat("b", 1000)
	first, _ := Match("a"+b, "a-"+b)
	both, _ := Match("a"+b, "a-"+b+" a"+b)
	if both != first {
		t.Errorf("score %d, want the first occurrence's %d", both, first)
	}
}
