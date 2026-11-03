// Package fzf is every search box's matching: fzf's algorithm and its
// extended search syntax.
package fzf

import (
	"slices"
	"strings"
	"sync"
	"unicode"

	"github.com/junegunn/fzf/src/algo"
	"github.com/junegunn/fzf/src/util"
)

var initOnce sync.Once

// Match matches hay against query as fzf does: space-separated terms must
// all match, fuzzy by default; 'exact, ^prefix, suffix$, !term to exclude
// (exact), and "a | b" for either. Smart case: an uppercase letter makes it
// case-sensitive. An empty query matches all. Higher scores are better.
func Match(query, hay string) (int, bool) {
	initOnce.Do(func() { algo.Init("default") })
	cs := strings.IndexFunc(query, unicode.IsUpper) >= 0
	chars := util.ToChars([]byte(hay))
	total := 0
	fields := strings.Fields(query)
	for i := 0; i < len(fields); i++ {
		best, any := 0, false
		for { // a term, and those "|" joins to it
			if s, ok := term(cs, &chars, fields[i]); ok {
				best, any = max(best, s), true
			}
			if i+2 >= len(fields) || fields[i+1] != "|" {
				break
			}
			i += 2
		}
		if !any {
			return 0, false
		}
		total += best
	}
	return total, true
}

// term matches one term of a query: its score, and whether it holds.
func term(cs bool, chars *util.Chars, t string) (int, bool) {
	match, neg := algo.FuzzyMatchV2, false
	if strings.HasPrefix(t, "!") {
		t, neg, match = t[1:], true, algo.ExactMatchNaive
	}
	switch {
	case strings.HasPrefix(t, "'"):
		t, match = t[1:], algo.ExactMatchNaive
	case strings.HasPrefix(t, "^"):
		t, match = t[1:], algo.PrefixMatch
	case strings.HasSuffix(t, "$") && len(t) > 1:
		t, match = t[:len(t)-1], algo.SuffixMatch
	}
	if t == "" || t == "|" {
		return 0, true
	}
	if !cs {
		t = strings.ToLower(t)
	}
	r, _ := match(cs, false, true, chars, []rune(t), false, nil)
	if found := r.Start >= 0; found == neg {
		return 0, false
	}
	if neg {
		return 0, true
	}
	return r.Score, true
}

// Runes is where the query's first word lands in s as a subsequence (what
// fzf matched, near enough to highlight), nil when it doesn't.
func Runes(q, s string) []int {
	f := strings.Fields(strings.ToLower(q))
	if len(f) == 0 {
		return nil
	}
	w := []rune(strings.TrimLeft(f[0], "'^!"))
	var at []int
	for i, r := range []rune(strings.ToLower(s)) {
		if len(at) < len(w) && r == w[len(at)] {
			at = append(at, i)
		}
	}
	if len(at) < len(w) {
		return nil
	}
	return at
}

// Sort orders items by their match of query, best first (stable: ties keep
// their order), dropping those that don't match. An empty query keeps all.
func Sort[T any](query string, items []T, hay func(T) string) []T {
	if strings.TrimSpace(query) == "" {
		return items
	}
	type scored struct {
		v T
		s int
	}
	var ss []scored
	for _, it := range items {
		if s, ok := Match(query, hay(it)); ok {
			ss = append(ss, scored{it, s})
		}
	}
	slices.SortStableFunc(ss, func(a, b scored) int { return b.s - a.s })
	out := make([]T, len(ss))
	for i, s := range ss {
		out[i] = s.v
	}
	return out
}
