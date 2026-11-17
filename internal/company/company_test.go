package company

import (
	"slices"
	"strings"
	"testing"

	"github.com/lfdominguez/desk/internal/claude"
	"github.com/lfdominguez/desk/internal/orgs"
)

func names(as []claude.Agent) []string {
	var out []string
	for _, a := range as {
		out = append(out, a.Name)
	}
	return out
}

// TestRoles: desk's roles are read from their files, each told how work
// comes and goes; only the lead can hand work on.
func TestRoles(t *testing.T) {
	rs := Roles()
	want := []string{"desk-architect", "desk-cfo", "desk-ciso", "desk-cto", "desk-devops", "desk-qa", "desk-security", "desk-senior-dev", "desk-tl"}
	if !slices.Equal(names(rs), want) {
		t.Fatalf("roles: %v", names(rs))
	}
	for _, r := range rs {
		if r.Description == "" || r.Model == "" || !strings.Contains(r.Prompt, "VERDICT: READY") {
			t.Errorf("%s: %+v", r.Name, r)
		}
		leafy := strings.Contains(r.Prompt, "whoever briefed you")
		if (r.Name == "desk-tl") == leafy || slices.Contains(r.Tools, "Agent") {
			t.Errorf("%s: only the lead delegates (tools %v)", r.Name, r.Tools)
		}
	}
}

// TestRoster: the organization's agents come first; desk's role is left
// out when one of them has its name or is mapped to it, when skipped, or
// when the company is off.
func TestRoster(t *testing.T) {
	own := []claude.Agent{{Name: "tl"}, {Name: "desk-qa"}, {Name: "backend"}}
	got := names(Roster(own, orgs.Company{Roles: map[string]string{"devops": "ops"}, Skip: []string{"cfo"}}))
	want := []string{"tl", "desk-qa", "backend", "desk-architect", "desk-ciso", "desk-cto", "desk-security", "desk-senior-dev"}
	if !slices.Equal(got, want) {
		t.Errorf("roster: %v", got)
	}
	if got := names(Roster(own, orgs.Company{Off: true})); !slices.Equal(got, []string{"tl", "desk-qa", "backend"}) {
		t.Errorf("off: %v", got)
	}
	if !HasRoles(Roster(nil, orgs.Company{})) || HasRoles(own[:1]) {
		t.Error("HasRoles")
	}
}

// TestRosterModels: [company.models] puts a role on another model; the
// rest keep their own.
func TestRosterModels(t *testing.T) {
	for _, a := range Roster(nil, orgs.Company{Models: map[string]string{"tl": "haiku", "qa": "inherit"}}) {
		switch want := map[string]string{"desk-tl": "haiku", "desk-qa": "inherit", "desk-cfo": "sonnet"}[a.Name]; {
		case want != "" && a.Model != want:
			t.Errorf("%s: %s, want %s", a.Name, a.Model, want)
		}
	}
}
