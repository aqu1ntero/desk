package company

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lfdominguez/desk/internal/orgs"
)

// TestRights: desk's default decision rights, the organization's over
// them, in the prompt.
func TestRights(t *testing.T) {
	r := Rights(orgs.Company{Decides: map[string]string{"security": "sec-team", "budget": "user"}})
	for _, want := range []string{"- architecture: desk-cto", "- security: sec-team", "- budget: the user", "- deploy: the user"} {
		if !strings.Contains(r, want) {
			t.Errorf("%q missing in\n%s", want, r)
		}
	}
	if !strings.HasPrefix(r, "- access:") {
		t.Errorf("sorted: %s", r)
	}
	if p := PromptFor(orgs.Company{}); !strings.HasPrefix(p, Prompt) || !strings.Contains(p, "- merge: desk-tl") {
		t.Errorf("prompt %q", p)
	}
}

// TestSpend: an organization's spend adds up per day; nothing spent adds
// nothing; an unwritable folder is an error.
func TestSpend(t *testing.T) {
	org := t.TempDir()
	day := time.Date(2026, 3, 4, 10, 0, 0, 0, time.Local)
	Spend(org, day, 1.25)
	Spend(org, day.Add(2*time.Hour), 0.5)
	Spend(org, day.AddDate(0, 0, 1), 3)
	Spend(org, day, 0)
	if got := Spent(org, day); got != 1.75 {
		t.Errorf("that day: %v", got)
	}
	if got := Spent(org, day.AddDate(0, 0, 1)); got != 3 {
		t.Errorf("the next: %v", got)
	}
	os.RemoveAll(Dir(org))
	os.WriteFile(Dir(org), nil, 0o644)
	if Spend(org, day, 1) == nil {
		t.Error("an unwritable folder: an error")
	}
}

// TestOver: over the card's budget since the user's last message, or the
// organization's for the day; neither without budgets.
func TestOver(t *testing.T) {
	c := orgs.Company{CardBudget: 2, DayBudget: 10}
	for _, tc := range []struct {
		cost, allowed, today float64
		want                 string
	}{
		{1.9, 0, 0, ""},
		{2, 0, 0, "its $2.00 budget since your last message"},
		{5, 4, 0, ""},
		{1, 0, 10, "the organization's $10.00 budget for today"},
	} {
		if got := Over(c, tc.cost, tc.allowed, tc.today); got != tc.want {
			t.Errorf("%+v: %q", tc, got)
		}
	}
	if Over(orgs.Company{}, 100, 0, 100) != "" {
		t.Error("no budgets: never over")
	}
}
