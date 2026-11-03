package ui

import (
	"reflect"
	"testing"
	"time"
)

// TestSmartParse: a request's @org, #label, !priority, due words, ticket
// keys and URLs are found; words made plain and unknown orgs are not.
func TestSmartParse(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC) // a Saturday
	orgs := []string{"Acme", "Globex"}
	for _, c := range []struct {
		in    string
		plain map[string]bool
		want  []smartTok
	}{
		{"Renew it on @globex before friday, ABC-123 !high #Infra", nil, []smartTok{
			{"org", "@globex", "Globex"}, {"due", "friday", "2026-11-06"}, {"prio", "!high", "High"}, {"label", "#Infra", "infra"}, {"link", "ABC-123", "ABC-123"}}},
		{"@Nope !bogus # ! @", nil, nil},
		{"today", nil, []smartTok{{"due", "today", "2026-10-31"}}},
		{"tomorrow", nil, []smartTok{{"due", "tomorrow", "2026-11-01"}}},
		{"saturday", nil, []smartTok{{"due", "saturday", "2026-11-07"}}}, // today's name is next week's
		{"sunday", nil, []smartTok{{"due", "sunday", "2026-11-01"}}},
		{"next week", nil, []smartTok{{"due", "next week", "2026-11-07"}}},
		{"a week", nil, nil},
		{"week", nil, nil},
		{"by 2026-12-01.", nil, []smartTok{{"due", "2026-12-01", "2026-12-01"}}},
		{"!urgent !hi !med !lo !none", nil, []smartTok{{"prio", "!urgent", "High"}, {"prio", "!med", "Medium"}, {"prio", "!lo", "Low"}}},
		{"see https://x.example.com/a/ABC-9. and ABC-9", nil, []smartTok{{"link", "https://x.example.com/a/ABC-9.", "https://x.example.com/a/ABC-9"}, {"link", "ABC-9", "ABC-9"}}},
		{"#a #a", nil, []smartTok{{"label", "#a", "a"}}},
		{"@Acme friday", map[string]bool{"friday": true}, []smartTok{{"org", "@Acme", "Acme"}}},
	} {
		if got := smartParse(c.in, orgs, now, c.plain); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q:\n got %v\nwant %v", c.in, got, c.want)
		}
	}
}

// TestSmartTitle: the title drops the words that only set fields and is
// cut at 100 runes.
func TestSmartTitle(t *testing.T) {
	in := "Renew the cert on @Acme, before friday !high #infra.\nmore notes"
	ts := smartParse(in, []string{"Acme"}, time.Now(), nil)
	if got := smartTitle(in, ts); got != "Renew the cert on before friday" {
		t.Fatalf("title %q", got)
	}
	long := ""
	for range 120 {
		long += "a"
	}
	if got := []rune(smartTitle(long, nil)); len(got) != 100 || got[99] != '…' {
		t.Fatalf("long title %d runes", len(got))
	}
	if got := smartTitle(long[:100], nil); got != long[:100] {
		t.Fatalf("100 runes cut: %q", got)
	}
	if smartVal(ts, "nope") != "" || len(smartVals(ts, "label")) != 1 {
		t.Fatal("smartVal/smartVals")
	}
}
