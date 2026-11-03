package ui

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/lfdominguez/desk/internal/kanban"
)

// A new card's request says things about the card itself as it is typed:
// @Org, #label, !priority, a due day (today, friday, 2026-11-06…), ticket
// keys and URLs. smartParse finds them; the dialog shows each as a pill,
// fills the card's fields from them, and a pill's click makes its word
// plain text again.

// smartTok is one such word: what it sets (org, label, prio, due, link),
// as typed, and its value.
type smartTok struct{ kind, raw, val string }

var (
	ticketKey = regexp.MustCompile(`\b[A-Z][A-Z0-9]+-[0-9]+\b`)
	urlWord   = regexp.MustCompile(`https?://[^\s<>"')]+`)
	weekdays  = []string{"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"}
)

// smartParse finds text's tokens; orgs are the organizations @ may name,
// now the day due words count from, plain the words made plain text.
func smartParse(text string, orgs []string, now time.Time, plain map[string]bool) []smartTok {
	var ts []smartTok
	add := func(kind, raw, val string) {
		if plain[raw] || slices.ContainsFunc(ts, func(t smartTok) bool { return t.kind == kind && t.val == val }) {
			return
		}
		ts = append(ts, smartTok{kind, raw, val})
	}
	day := func(d int) string { return now.AddDate(0, 0, d).Format(kanban.DateLayout) }
	words := strings.Fields(text)
	for i, w := range words {
		w = strings.TrimRight(w, ".,;:?)")
		lw := strings.ToLower(w)
		switch {
		case strings.HasPrefix(w, "@") && len(w) > 1:
			if i := slices.IndexFunc(orgs, func(o string) bool { return strings.EqualFold(o, w[1:]) }); i >= 0 {
				add("org", w, orgs[i])
			}
		case strings.HasPrefix(w, "#") && len(w) > 1:
			add("label", w, strings.ToLower(w[1:]))
		case strings.HasPrefix(w, "!") && len(w) > 1:
			name := map[string]string{"urgent": "High", "hi": "High", "med": "Medium", "lo": "Low"}[lw[1:]]
			if name == "" {
				name = lw[1:]
			}
			if p := slices.IndexFunc(prioNames, func(n string) bool { return strings.EqualFold(n, name) }); p > 0 {
				add("prio", w, prioNames[p])
			}
		case lw == "today":
			add("due", w, day(0))
		case lw == "tomorrow":
			add("due", w, day(1))
		case lw == "week" && i > 0 && strings.EqualFold(words[i-1], "next"):
			add("due", "next week", day(7))
		case slices.Contains(weekdays, lw):
			d := (slices.Index(weekdays, lw) - int(now.Weekday()) + 7) % 7
			if d == 0 {
				d = 7 // today's name is next week's
			}
			add("due", w, day(d))
		default:
			if _, err := time.Parse(kanban.DateLayout, w); err == nil {
				add("due", w, w)
			}
		}
	}
	for _, u := range urlWord.FindAllString(text, -1) {
		add("link", u, strings.TrimRight(u, ".,;:"))
	}
	for _, k := range ticketKey.FindAllString(urlWord.ReplaceAllString(text, ""), -1) {
		add("link", k, k)
	}
	return ts
}

// tokens is smartParse of text in s, kept until the text, its plain words,
// the organizations or the day change: the dialog asks several times a
// frame, and on every key and caret blink.
func (u *ui) tokens(s *sheet, text string) []smartTok {
	now := time.Now()
	k := fmt.Sprint(len(s.plain), len(u.orgList), now.Format(time.DateOnly), "\x00", text)
	if ts, ok := s.toks[k]; ok {
		return ts
	}
	if s.toks == nil || len(s.toks) > 8 { // the request and what the user wrote before Improve: a few at a time
		s.toks = map[string][]smartTok{}
	}
	ts := smartParse(text, u.orgList, now, s.plain)
	s.toks[k] = ts
	return ts
}

// smartTitle is a request's first line without the words that only set
// fields (@org, #label, !priority), cut to a card title's length.
func smartTitle(text string, ts []smartTok) string {
	line := firstLine(strings.TrimSpace(text))
	var keep []string
	for _, w := range strings.Fields(line) {
		bare := strings.TrimRight(w, ".,;:?)")
		if slices.ContainsFunc(ts, func(t smartTok) bool {
			return t.raw == bare && (t.kind == "org" || t.kind == "label" || t.kind == "prio")
		}) {
			continue
		}
		keep = append(keep, w)
	}
	t := strings.Join(keep, " ")
	if r := []rune(t); len(r) > 100 {
		t = strings.TrimSpace(string(r[:99])) + "…"
	}
	return t
}

// smartVal is the value of the first token of kind, "" without one.
func smartVal(ts []smartTok, kind string) string {
	for _, t := range ts {
		if t.kind == kind {
			return t.val
		}
	}
	return ""
}

// smartVals is every token value of kind.
func smartVals(ts []smartTok, kind string) []string {
	var vs []string
	for _, t := range ts {
		if t.kind == kind {
			vs = append(vs, t.val)
		}
	}
	return vs
}
