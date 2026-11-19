package infra

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// A pane's URLs open with Ctrl+click, as in Konsole and Kitty: Ctrl over one
// underlines it and shows the hand.

// urlRE is a web link as terminals find them: up to a space, a quote or an
// angle bracket.
var urlRE = regexp.MustCompile("(?:https?|ftp)://[^\\s<>\"'`]+")

// linkSpan is a URL on a pane's row: its cells [lo, hi). Zero: none.
type linkSpan struct {
	row, lo, hi int
	url         string
}

// urlAt is the URL in line covering cell x, its trailing punctuation left
// out (a sentence's full stop, a closing bracket it didn't open).
// ponytail: one row only; a URL the terminal wrapped over two opens cut.
func urlAt(line []rune, x int) (lo, hi int, url string) {
	s := string(line)
	for _, m := range urlRE.FindAllStringIndex(s, -1) {
		u := trimURL(s[m[0]:m[1]])
		a := utf8.RuneCountInString(s[:m[0]])
		b := a + utf8.RuneCountInString(u)
		if x >= a && x < b {
			return a, b, u
		}
	}
	return 0, 0, ""
}

// trimURL drops what ends a sentence after a URL, and a closing bracket
// with no opening one inside it.
func trimURL(u string) string {
	for u != "" {
		c := u[len(u)-1]
		switch {
		case strings.IndexByte(".,;:!?", c) >= 0:
		case c == ')' && strings.Count(u, "(") < strings.Count(u, ")"):
		case c == ']' && strings.Count(u, "[") < strings.Count(u, "]"):
		default:
			return u
		}
		u = u[:len(u)-1]
	}
	return u
}
