package links

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lfdominguez/desk/internal/kanban"
	"github.com/lfdominguez/desk/internal/orgs"
)

// TestPushJira: a column move takes the transition to its status
// category (by category, not name: workflows and languages differ), Blocked
// a status named so; in step already, someone else's, in an ignored status,
// or opted out, nothing is sent.
func TestPushJira(t *testing.T) {
	status := `{"name":"Tareas por hacer","statusCategory":{"key":"new"}}`
	assignee := `{"accountId":"me-1"}`
	var posted []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, _ := r.BasicAuth(); u != "me@globex.com" || p != "tok" {
			w.WriteHeader(401)
			return
		}
		switch {
		case r.Method == "GET" && r.URL.Path == "/rest/api/3/issue/ABC-1":
			w.Write([]byte(`{"fields":{"status":` + status + `,"assignee":` + assignee + `}}`))
		case r.Method == "GET" && r.URL.Path == "/rest/api/3/myself":
			w.Write([]byte(`{"accountId":"me-1"}`))
		case r.Method == "GET" && r.URL.Path == "/rest/api/3/issue/ABC-1/transitions":
			w.Write([]byte(`{"transitions":[
				{"id":"11","to":{"name":"Tareas por hacer","statusCategory":{"key":"new"}}},
				{"id":"21","to":{"name":"En curso","statusCategory":{"key":"indeterminate"}}},
				{"id":"31","to":{"name":"Bloqueado","statusCategory":{"key":"indeterminate"}}},
				{"id":"41","to":{"name":"Listo","statusCategory":{"key":"done"}}},
				{"id":"51","to":{"name":"En revisión","statusCategory":{"key":"indeterminate"}}}]}`))
		case r.Method == "POST" && r.URL.Path == "/rest/api/3/issue/ABC-1/transitions":
			var b struct{ Transition struct{ ID string } }
			json.NewDecoder(r.Body).Decode(&b)
			posted = append(posted, b.Transition.ID)
			w.WriteHeader(204)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	cfg := orgs.Config{Jira: []orgs.Jira{{URL: srv.URL, Email: "me@globex.com", Projects: []string{"ABC"}, Secret: orgs.Secret{Token: "tok"}}}}
	l := kanban.Link{URL: srv.URL + "/browse/ABC-1", Kind: KindJira, Ref: "ABC-1"}
	ctx := context.Background()
	for _, c := range []struct{ column, want, id string }{
		{"In progress", "En curso", "21"},
		{"Blocked", "Bloqueado", "31"},
		{"Done", "Listo", "41"},
		{"To do", "", ""}, // already new: nothing
	} {
		posted = nil
		got, err := Push(ctx, l, c.column, cfg)
		if err != nil || got != c.want || (c.id != "" && (len(posted) != 1 || posted[0] != c.id)) || (c.id == "" && len(posted) != 0) {
			t.Fatalf("%s: %q %v, posted %v", c.column, got, err, posted)
		}
	}
	status = `{"name":"Bloqueado","statusCategory":{"key":"indeterminate"}}`
	posted = nil
	if got, _ := Push(ctx, l, "In progress", cfg); got != "En curso" || len(posted) != 1 || posted[0] != "21" { // out of Blocked, though the category is the same
		t.Fatalf("unblock: %q %v", got, posted)
	}
	cfg.Jira[0].Status = map[string]string{"Done": "en revisión"} // the owner closes them: Done stops at review
	posted = nil
	if got, err := Push(ctx, l, "Done", cfg); got != "En revisión" || err != nil || len(posted) != 1 || posted[0] != "51" {
		t.Fatalf("custom status: %q %v %v", got, err, posted)
	}
	status = `{"name":"En revisión","statusCategory":{"key":"indeterminate"}}`
	posted = nil
	if got, _ := Push(ctx, l, "Done", cfg); got != "" || len(posted) != 0 {
		t.Fatalf("in review already: %q %v", got, posted)
	}
	status = `{"name":"Pruebas","statusCategory":{"key":"indeterminate"}}`
	cfg.Jira[0].Ignore = []string{"pruebas", "Listo"} // others carry it on from review
	if got, _ := Push(ctx, l, "Done", cfg); got != "" || len(posted) != 0 {
		t.Fatalf("in an ignored status, moved: %q %v", got, posted)
	}
	cfg.Jira[0].Ignore = nil
	status = `{"name":"En curso","statusCategory":{"key":"indeterminate"}}`
	for _, a := range []string{`{"accountId":"someone-2"}`, `null`} {
		assignee = a
		if got, err := Push(ctx, l, "Done", cfg); got != "" || err != nil || len(posted) != 0 {
			t.Fatalf("assignee %s: moved %q %v %v", a, got, err, posted)
		}
	}
	assignee = `{"accountId":"me-1"}`
	cfg.Jira[0].Status = map[string]string{"Done": "Cerrado"}
	if _, err := Push(ctx, l, "Done", cfg); err == nil {
		t.Fatal("a status the workflow can't reach: no error")
	}
	no := false
	cfg.Jira[0].Sync = &no
	posted = nil
	if got, err := Push(ctx, l, "Done", cfg); got != "" || err != nil || len(posted) != 0 {
		t.Fatalf("sync = false still pushed: %q %v %v", got, err, posted)
	}
}

// TestPushGitHub: Done closes an issue, out of Done reopens it; without
// an account of the user's for it, nothing is written.
func TestPushGitHub(t *testing.T) {
	state, assignees := "open", `[{"login":"Me"}]`
	var patched []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/user" {
			w.Write([]byte(`{"login":"me"}`))
			return
		}
		if r.URL.Path != "/repos/globex-org/api/issues/7" {
			w.WriteHeader(404)
			return
		}
		if r.Method == "PATCH" {
			var b map[string]string
			json.NewDecoder(r.Body).Decode(&b)
			patched = append(patched, b["state"]+"/"+b["state_reason"])
			state = b["state"]
		}
		w.Write([]byte(`{"state":"` + state + `","assignees":` + assignees + `}`))
	}))
	defer srv.Close()
	githubAPI = srv.URL
	defer func() { githubAPI = "https://api.github.com" }()
	cfg := orgs.Config{GitHub: []orgs.GitHub{{Owners: []string{"globex-org"}, Secret: orgs.Secret{Token: "tok"}}}}
	l := kanban.Link{URL: "https://github.com/globex-org/api/issues/7", Kind: KindGitHubIssue, Ref: "#7"}
	ctx := context.Background()
	if got, err := Push(ctx, l, "Done", cfg); got != "closed" || err != nil {
		t.Fatalf("done: %q %v", got, err)
	}
	if got, _ := Push(ctx, l, "Done", cfg); got != "" {
		t.Fatalf("closed already: %q", got)
	}
	if got, _ := Push(ctx, l, "In progress", cfg); got != "reopened" || len(patched) != 2 || patched[0] != "closed/completed" || patched[1] != "open/" {
		t.Fatalf("reopen: %q %v", got, patched)
	}
	for _, a := range []string{`[{"login":"someone"}]`, `[]`} {
		assignees = a
		if got, err := Push(ctx, l, "Done", cfg); got != "" || err != nil || len(patched) != 2 {
			t.Fatalf("assignees %s: %q %v %v", a, got, err, patched)
		}
	}
	if got, _ := Push(ctx, kanban.Link{URL: "https://github.com/someone/else/issues/1", Kind: KindGitHubIssue}, "Done", cfg); got != "" {
		t.Fatalf("no account for it: %q", got)
	}
}
