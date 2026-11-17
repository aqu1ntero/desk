// Package company is desk's company of Claudes: its own roles (a tech
// lead, specialists, an organization's executives) as Claude Code agents
// every card's session can hand work to, merged with the organization's
// own agents, which come first.
package company

import (
	"embed"
	"slices"
	"strings"

	"github.com/lfdominguez/desk/internal/claude"
	"github.com/lfdominguez/desk/internal/orgs"
)

//go:embed roles/*.md
var roleFiles embed.FS

// Prefix names desk's roles, apart from any agent of an organization's.
const Prefix = "desk-"

// handoff is what every role is told about briefs and results; a role
// without the Agent tool can't hand work on, so it says what it needs.
const handoff = `

## How work reaches you and goes back

The brief you get says the objective, where to look (paths, not pasted text), the limits, and when
it is done. Missing one that matters? Say so in your result instead of guessing.

The organization's own CLAUDE.md, skills and workflows are rules for you too: follow them over
anything here.

End with a result of at most 25 lines: what you did or found, with paths, and what you ran to check
it. Its last line is exactly one of:
VERDICT: READY
VERDICT: BLOCKED <why>
VERDICT: PARTIAL <what is missing>`

// leaf is told to the roles that can't hand work on (no Agent tool).
const leaf = `

When another skill is needed, say which role and why in the result: whoever briefed you hands it on.`

// Roles are desk's roles, by name, each with the hand-off rules.
func Roles() []claude.Agent {
	es, _ := roleFiles.ReadDir("roles")
	var out []claude.Agent
	for _, e := range es {
		b, _ := roleFiles.ReadFile("roles/" + e.Name())
		if a, ok := claude.ParseAgent(string(b)); ok {
			a.Prompt += handoff
			if len(a.Tools) > 0 && !slices.Contains(a.Tools, "Agent") {
				a.Prompt += leaf
			}
			out = append(out, a)
		}
	}
	return out
}

// Roster is what a session of an organization with own agents and
// settings c can hand work to: its own agents, then desk's roles it
// doesn't replace (one of its agents by the role's name, with or without
// Prefix, or mapped in c.Roles), skip, or turn off; each on c.Models' model
// for it, else its own.
func Roster(own []claude.Agent, c orgs.Company) []claude.Agent {
	out := slices.Clone(own)
	if c.Off {
		return out
	}
	for _, r := range Roles() {
		role := strings.TrimPrefix(r.Name, Prefix)
		if c.Roles[role] != "" || slices.Contains(c.Skip, role) ||
			slices.ContainsFunc(own, func(a claude.Agent) bool { return a.Name == role || a.Name == r.Name }) {
			continue
		}
		if m := c.Models[role]; m != "" {
			r.Model = m
		}
		out = append(out, r)
	}
	return out
}

// Prompt tells a card's Claude how the company works; added to its system
// prompt whenever its roster has desk's roles.
const Prompt = `

desk's company: besides the organization's own agents, you can hand work to desk's roles with the
Agent tool (their names start with "desk-": a tech lead, an architect, a senior developer, QA,
DevOps, security, and this organization's CTO, CISO and CFO).
- The organization's own CLAUDE.md, skills, agents and workflows come first: when one of them
  covers the work, use it. desk's roles fill what they don't cover.
- Pick the best approach for each task and say which you used, in one line.
- Delegate only when it helps: a part that needs a specialist's skill, an independent check, or
  parallel work. Small tasks you do yourself.
- Brief each one with the objective, where to look (paths), the limits and when it's done; read
  its result and its VERDICT line before you rely on it.
- What the organization already decided lives with its department leads: brief the engineering,
  security or finance lead with ask_lead when your work touches their area.
- Executives decide or recommend; the user still approves pushes, deploys, spending and access
  changes unless the organization's own workflow says otherwise.`

// HasRoles says whether as holds any of desk's roles.
func HasRoles(as []claude.Agent) bool {
	return slices.ContainsFunc(as, func(a claude.Agent) bool { return strings.HasPrefix(a.Name, Prefix) })
}
