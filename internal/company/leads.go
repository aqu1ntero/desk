package company

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Department is one of an organization's departments with a lead: a
// long-running Claude of its own that keeps the department's decisions
// over cards, reads the organization's folder and never edits. Servers
// have theirs already: the organization's infra Claude.
type Department struct {
	Name, Lead, Charge string
}

// Departments are the leads a card's Claude can brief with ask_lead.
var Departments = []Department{
	{"engineering", "Engineering lead", "how the organization's software is built: its architecture, conventions, the decisions taken and why, what work is in flight and how it fits together"},
	{"security", "Security lead", "the organization's security posture: secrets, access, exposure, dependencies, what was approved and on what conditions"},
	{"finance", "Finance lead", "what the organization's work costs: cloud and services, licenses, AI spend, and what was agreed to spend"},
}

// DepartmentOf is the department named name (any case); ok false: none.
func DepartmentOf(name string) (Department, bool) {
	i := slices.IndexFunc(Departments, func(d Department) bool { return strings.EqualFold(d.Name, strings.TrimSpace(name)) })
	if i < 0 {
		return Department{}, false
	}
	return Departments[i], true
}

// LeadPrompt is the system prompt of org's lead of d.
func LeadPrompt(org string, d Department) string {
	return fmt.Sprintf(`

You are the %s of %s, in desk's company: a long-running session that keeps %s. You work in the
organization's folder and only read: you never edit, run or deploy.

Cards' Claudes brief you when their work touches your department. Answer each brief with what the
organization already decided and why, what its own CLAUDE.md, skills and workflows require, and your
recommendation, citing paths. Keep what you learn: later briefs build on your earlier answers.

The organization's own rules come first; decisions that are the user's (pushes, deploys, spending,
access) stay theirs: say so instead of deciding them.

End with a result of at most 25 lines whose last line is exactly one of:
VERDICT: READY
VERDICT: BLOCKED <why>
VERDICT: PARTIAL <what is missing>`, d.Lead, org, d.Charge)
}

// Brief is a card's Claude's ask_lead: the work, laid out for a lead.
type Brief struct {
	Department string `json:"department"`
	Objective  string `json:"objective"`
	Context    string `json:"context"`     // where to look: paths, cards, links
	Limits     string `json:"constraints"` // what it must not do or change
	DoneWhen   string `json:"done_when"`
	Decision   string `json:"decision"` // the decision needed, if any
}

// Text lays b out for the lead, from card key's Claude.
func (b Brief) Text(key string) string {
	var s strings.Builder
	fmt.Fprintf(&s, "Brief from %s's Claude\nObjective: %s\n", key, strings.TrimSpace(b.Objective))
	for _, f := range [][2]string{{"Where to look", b.Context}, {"Limits", b.Limits}, {"Done when", b.DoneWhen}, {"Decision needed", b.Decision}} {
		if v := strings.TrimSpace(f[1]); v != "" {
			fmt.Fprintf(&s, "%s: %s\n", f[0], v)
		}
	}
	return strings.TrimSpace(s.String())
}

// Sessions are org's leads' Claude Code sessions by department, kept in
// its company folder (leads.json) so a lead resumes its memory.
func Sessions(orgDir string) map[string]string {
	m := map[string]string{}
	b, _ := os.ReadFile(filepath.Join(Dir(orgDir), "leads.json"))
	json.Unmarshal(b, &m)
	return m
}

// SetSession keeps department's session id for org.
func SetSession(orgDir, department, id string) error {
	m := Sessions(orgDir)
	m[department] = id
	b, _ := json.MarshalIndent(m, "", "  ")
	if err := os.MkdirAll(Dir(orgDir), 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(Dir(orgDir), "leads.json"), append(b, '\n'), 0o644)
}
