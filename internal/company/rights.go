package company

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/lfdominguez/desk/internal/orgs"
)

// decides is who takes each kind of decision unless the organization's
// [company.decides] says otherwise: a desk role, or the user.
var decides = map[string]string{
	"architecture": "desk-cto",
	"security":     "desk-ciso",
	"spending":     "desk-cfo",
	"merge":        "desk-tl",
	"deploy":       "user",
	"access":       "user",
}

// Rights are the organization's decision rights, one "kind: who" line
// each, sorted: desk's defaults under its own.
func Rights(c orgs.Company) string {
	m := maps.Clone(decides)
	maps.Copy(m, c.Decides)
	var lines []string
	for _, k := range slices.Sorted(maps.Keys(m)) {
		who := m[k]
		if who == "user" {
			who = "the user (ask them; nobody else can grant it)"
		}
		lines = append(lines, "- "+k+": "+who)
	}
	return strings.Join(lines, "\n")
}

// PromptFor is Prompt with the organization's decision rights.
func PromptFor(c orgs.Company) string {
	return Prompt + "\n\nWho decides what in this organization (ask them before you act on it; a role recommends, " +
		"and what it can't grant still goes to the user):\n" + Rights(c)
}

var spendMu sync.Mutex

// spendFile is org's spend by day, <Org>/.desk/company/spend.json.
func spendFile(orgDir string) string { return filepath.Join(Dir(orgDir), "spend.json") }

// Spent is what org's sessions cost on day (its date, local).
func Spent(orgDir string, day time.Time) float64 {
	spendMu.Lock()
	defer spendMu.Unlock()
	return spends(orgDir)[day.Format(time.DateOnly)]
}

func spends(orgDir string) map[string]float64 {
	m := map[string]float64{}
	b, _ := os.ReadFile(spendFile(orgDir))
	json.Unmarshal(b, &m)
	return m
}

// Spend adds usd to org's spend on day.
func Spend(orgDir string, day time.Time, usd float64) error {
	if usd <= 0 {
		return nil
	}
	spendMu.Lock()
	defer spendMu.Unlock()
	m := spends(orgDir)
	m[day.Format(time.DateOnly)] += usd
	b, _ := json.Marshal(m)
	if err := os.MkdirAll(Dir(orgDir), 0o755); err != nil {
		return err
	}
	return os.WriteFile(spendFile(orgDir), b, 0o644)
}

// Over says why a card whose session cost cost, cost allowed when the
// user last wrote, is over its organization's budgets (c) given what the
// organization spent today; "" when it isn't.
func Over(c orgs.Company, cost, allowed, today float64) string {
	switch {
	case c.CardBudget > 0 && cost-allowed >= c.CardBudget:
		return fmt.Sprintf("its $%.2f budget since your last message", c.CardBudget)
	case c.DayBudget > 0 && today >= c.DayBudget:
		return fmt.Sprintf("the organization's $%.2f budget for today", c.DayBudget)
	}
	return ""
}
