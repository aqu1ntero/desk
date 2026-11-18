// Package diff is a change between two texts, line by line: the card
// panel's diffs (ui) and the infra Claude's file approvals (infra) use it.
package diff

import (
	"fmt"
	"strings"
)

// Line is one line of a change: kept (' '), removed ('-') or added ('+').
type Line struct {
	Op   byte
	Text string
}

// Lines is old turned into new, line by line: the common start and end
// kept, the middle by longest common subsequence. ponytail: O(n·m); past a
// million cells the middle is shown as all removed then all added.
func Lines(old, new string) []Line {
	a, b := Split(old), Split(new)
	var head, tail []Line
	for len(a) > 0 && len(b) > 0 && a[0] == b[0] {
		head = append(head, Line{' ', a[0]})
		a, b = a[1:], b[1:]
	}
	for len(a) > 0 && len(b) > 0 && a[len(a)-1] == b[len(b)-1] {
		tail = append([]Line{{' ', a[len(a)-1]}}, tail...)
		a, b = a[:len(a)-1], b[:len(b)-1]
	}
	mid := make([]Line, 0, len(a)+len(b))
	if len(a)*len(b) > 1_000_000 {
		for _, l := range a {
			mid = append(mid, Line{'-', l})
		}
		for _, l := range b {
			mid = append(mid, Line{'+', l})
		}
		return append(append(head, mid...), tail...)
	}
	n, m := len(a), len(b)
	lcs := make([][]int, n+1) // lcs[i][j]: of a[i:] and b[j:]
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && a[i] == b[j]:
			mid = append(mid, Line{' ', a[i]})
			i, j = i+1, j+1
		case i < n && (j == m || lcs[i+1][j] >= lcs[i][j+1]): // removals first, as diffs read
			mid = append(mid, Line{'-', a[i]})
			i++
		default:
			mid = append(mid, Line{'+', b[j]})
			j++
		}
	}
	return append(append(head, mid...), tail...)
}

// Split is s's lines, without the last one's newline; nil for "".
func Split(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// Unified is the change as unified diff text, ctx kept lines around each
// change ("@@ -l +l @@" heads, " "/"-"/"+" lines), and how many lines it
// adds and removes.
func Unified(old, new string, ctx int) (text string, add, del int) {
	ls := Lines(old, new)
	var b strings.Builder
	oi, ni := 1, 1 // the line numbers ls[i] has in old and new
	nums := make([][2]int, len(ls))
	for i, l := range ls {
		nums[i] = [2]int{oi, ni}
		switch l.Op {
		case '-':
			oi++
			del++
		case '+':
			ni++
			add++
		default:
			oi, ni = oi+1, ni+1
		}
	}
	for i := 0; i < len(ls); {
		if ls[i].Op == ' ' {
			i++
			continue
		}
		from, to := max(0, i-ctx), i
		for j := i; j < len(ls); j++ { // the hunk ends after ctx kept lines that no change follows closely
			if ls[j].Op != ' ' {
				to = j + 1
			} else if j-to >= ctx {
				break
			}
		}
		to = min(len(ls), to+ctx)
		fmt.Fprintf(&b, "@@ -%d +%d @@\n", nums[from][0], nums[from][1])
		for _, l := range ls[from:to] {
			b.WriteByte(l.Op)
			b.WriteString(l.Text)
			b.WriteByte('\n')
		}
		i = to
	}
	return b.String(), add, del
}
