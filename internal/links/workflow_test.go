package links

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lfdominguez/desk/internal/kanban"
	"github.com/lfdominguez/desk/internal/orgs"
)

// fakeJira is a Jira with ABC-1 in *status, assigned to *assignee ("" nobody),
// its project's statuses, and transitions to To do, In progress, Test, Done.
func fakeJira(t *testing.T, status, assignee *string, posted *[]string) (*httptest.Server, orgs.Config, kanban.Link) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method+" "+r.URL.Path == jiraFails {
			w.WriteHeader(500)
			return
		}
		switch {
		case r.Method == "GET" && r.URL.Path == "/rest/api/3/issue/ABC-1":
			a := "null"
			if *assignee != "" {
				a = `{"accountId":"` + *assignee + `"}`
			}
			w.Write([]byte(`{"fields":{"status":` + *status + `,"assignee":` + a + `}}`))
		case r.URL.Path == "/rest/api/3/myself":
			w.Write([]byte(`{"accountId":"me-1"}`))
		case r.Method == "GET" && r.URL.Path == "/rest/api/3/issue/ABC-1/transitions":
			w.Write([]byte(`{"transitions":[
				{"id":"11","to":{"name":"To Do","statusCategory":{"key":"new"}}},
				{"id":"21","to":{"name":"In Progress","statusCategory":{"key":"indeterminate"}}},
				{"id":"31","to":{"name":"Test","statusCategory":{"key":"indeterminate"}}},
				{"id":"41","to":{"name":"Done","statusCategory":{"key":"done"}}}]}`))
		case r.Method == "POST" && r.URL.Path == "/rest/api/3/issue/ABC-1/transitions":
			var b struct{ Transition struct{ ID string } }
			json.NewDecoder(r.Body).Decode(&b)
			*posted = append(*posted, b.Transition.ID)
			w.WriteHeader(204)
		case r.URL.Path == "/rest/api/3/project/ABC/statuses":
			w.Write([]byte(`[{"statuses":[{"name":"To Do","statusCategory":{"key":"new"}},{"name":"Test","statusCategory":{"key":"indeterminate"}}]},
				{"statuses":[{"name":"Test","statusCategory":{"key":"indeterminate"}},{"name":"Done","statusCategory":{"key":"done"}}]}]`))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	cfg := orgs.Config{Jira: []orgs.Jira{{URL: srv.URL, Email: "me@globex.com", Projects: []string{"ABC"}, Secret: orgs.Secret{Token: "tok"}}}}
	return srv, cfg, kanban.Link{URL: srv.URL + "/browse/ABC-1", Kind: KindJira, Ref: "ABC-1"}
}

// jiraFails is a request ("POST /rest/…") fakeJira answers with a 500.
var jiraFails string

const (
	inTest     = `{"name":"Test","statusCategory":{"key":"indeterminate"}}`
	inProgress = `{"name":"In Progress","statusCategory":{"key":"indeterminate"}}`
)

// TestSyncFollows: with a workflow, the background check never moves a
// ticket back: one someone moved since it was last seen is Moved (even
// when it's someone else's now); one first seen, or not changed since (the
// card's own move couldn't move it), isn't; without one it is Push.
func TestSyncFollows(t *testing.T) {
	status, assignee, posted := inTest, "qa-7", []string{}
	_, cfg, l := fakeJira(t, &status, &assignee, &posted)
	ctx := context.Background()
	cfg.Workflow = orgs.Workflow{Status: map[string]string{"In progress": "In Progress", "Review": "Code Review"}, Follow: map[string]string{"test": "Review"}}
	l.Seen = "In Progress"
	if s, err := Sync(ctx, l, "In progress", cfg); s != (Synced{Seen: "Test", Moved: true}) || err != nil || len(posted) != 0 {
		t.Fatalf("follow: %+v %v %v", s, err, posted)
	}
	l.Seen = ""
	if s, _ := Sync(ctx, l, "In progress", cfg); s != (Synced{Seen: "Test"}) {
		t.Fatalf("first seen: %+v", s)
	}
	l.Seen = "test"
	if s, _ := Sync(ctx, l, "Review", cfg); s != (Synced{Seen: "Test"}) { // the card moved, the ticket couldn't: both stay
		t.Fatalf("unchanged since: %+v", s)
	}
	status = inProgress
	if s, _ := Sync(ctx, l, "Review", cfg); s != (Synced{Seen: "In Progress", Moved: true}) {
		t.Fatalf("back to work: %+v", s)
	}
	if s, _ := Sync(ctx, l, "In progress", cfg); s != (Synced{Seen: "In Progress"}) {
		t.Fatalf("in step: %+v", s)
	}
	cfg.Workflow = orgs.Workflow{Rules: "Only QA moves to Done."} // rules alone: nothing moved back
	if s, _ := Sync(ctx, l, "To do", cfg); s.Now != "" || len(posted) != 0 {
		t.Fatalf("rules only: %+v %v", s, posted)
	}
	cfg.Jira[0].Ignore = []string{"in progress"}
	if s, _ := Sync(ctx, l, "To do", cfg); s != (Synced{Seen: "In Progress"}) {
		t.Fatalf("ignored: %+v", s)
	}
	cfg.Jira[0].Ignore = nil
	cfg.Workflow, assignee = orgs.Workflow{}, "me-1"
	if s, err := Sync(ctx, l, "To do", cfg); s != (Synced{Now: "To Do", Seen: "To Do"}) || err != nil || len(posted) != 1 || posted[0] != "11" {
		t.Fatalf("no workflow: moved back as Push does: %+v %v %v", s, err, posted)
	}
}

// TestSyncGitHubFollows: with a workflow a GitHub issue is never closed or
// reopened by the check either: closed or reopened since last seen, out
// of step with its card, it's Moved; a read that fails says so.
func TestSyncGitHubFollows(t *testing.T) {
	state, patched, fail := "closed", 0, false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			w.WriteHeader(500)
			return
		}
		if r.Method == "PATCH" {
			patched++
		}
		w.Write([]byte(`{"state":"` + state + `","assignees":[{"login":"me"}]}`))
	}))
	defer srv.Close()
	defer func(a string) { githubAPI = a }(githubAPI)
	githubAPI = srv.URL
	cfg := orgs.Config{GitHub: []orgs.GitHub{{Owners: []string{"globex-org"}, Secret: orgs.Secret{Token: "tok"}}},
		Workflow: orgs.Workflow{Rules: "QA closes issues."}}
	l := kanban.Link{URL: "https://github.com/globex-org/api/issues/7", Kind: KindGitHubIssue, Ref: "#7", Seen: "open"}
	ctx := context.Background()
	for _, c := range []struct {
		state, seen, col string
		want             Synced
	}{
		{"closed", "open", "In progress", Synced{Seen: "closed", Moved: true}},
		{"closed", "", "In progress", Synced{Seen: "closed"}},
		{"closed", "closed", "In progress", Synced{Seen: "closed"}},
		{"closed", "open", "Done", Synced{Seen: "closed"}},
		{"open", "closed", "Done", Synced{Seen: "open", Moved: true}},
		{"open", "closed", "Review", Synced{Seen: "open"}},
	} {
		state, l.Seen = c.state, c.seen
		if s, err := Sync(ctx, l, c.col, cfg); s != c.want || err != nil {
			t.Errorf("%+v: %+v %v", c, s, err)
		}
	}
	if patched != 0 {
		t.Fatalf("the check moved an issue: %d", patched)
	}
	fail = true
	if _, err := Sync(ctx, l, "Done", cfg); err == nil {
		t.Fatal("a failed read")
	}
	cfg.GitHub[0].Secret = orgs.Secret{TokenEnv: "DESK_NO_SUCH_TOKEN"}
	if _, err := Sync(ctx, l, "Done", cfg); err == nil {
		t.Fatal("a token that doesn't resolve")
	}
}

// TestPushWorkflowStatus: a column's status comes from the workflow when
// the account names none; the account's wins.
func TestPushWorkflowStatus(t *testing.T) {
	status, assignee, posted := inProgress, "me-1", []string{}
	_, cfg, l := fakeJira(t, &status, &assignee, &posted)
	cfg.Workflow.Status = map[string]string{"Review": "Test"}
	if now, err := Push(context.Background(), l, "Review", cfg); now != "Test" || err != nil || posted[0] != "31" {
		t.Fatalf("%q %v %v", now, err, posted)
	}
	cfg.Jira[0].Status = map[string]string{"Review": "Done"}
	if now, _ := Push(context.Background(), l, "Review", cfg); now != "Done" {
		t.Fatalf("the account's: %q", now)
	}
}

// TestTransition: a ticket moves to a status by name; one it can't reach
// says where it can go; someone else's, or another kind of link, doesn't.
func TestTransition(t *testing.T) {
	status, assignee, posted := inProgress, "me-1", []string{}
	_, cfg, l := fakeJira(t, &status, &assignee, &posted)
	ctx := context.Background()
	if now, err := Transition(ctx, l, "test", cfg); now != "Test" || err != nil || posted[0] != "31" {
		t.Fatalf("%q %v %v", now, err, posted)
	}
	if now, err := Transition(ctx, l, "in progress", cfg); now != "In Progress" || err != nil || len(posted) != 1 {
		t.Fatalf("there already: %q %v", now, err)
	}
	if _, err := Transition(ctx, l, "Deployed", cfg); err == nil || !strings.Contains(err.Error(), "it can go to: To Do, In Progress, Test, Done") {
		t.Fatalf("no such transition: %v", err)
	}
	for _, req := range []string{"GET /rest/api/3/issue/ABC-1", "GET /rest/api/3/issue/ABC-1/transitions", "POST /rest/api/3/issue/ABC-1/transitions"} {
		jiraFails = req
		if _, err := Transition(ctx, l, "Done", cfg); err == nil {
			t.Errorf("%s failing: no error", req)
		}
	}
	jiraFails = ""
	cfg.Jira[0].Ignore = []string{"in progress"} // where it is (the fake's POST doesn't move it)
	if _, err := Transition(ctx, l, "Done", cfg); err == nil || !strings.Contains(err.Error(), "leaves to others") {
		t.Fatalf("from an ignored status: %v", err)
	}
	cfg.Jira[0].Ignore, cfg.Jira[0].Sync = nil, new(bool)
	if _, err := Transition(ctx, l, "Done", cfg); err == nil || !strings.Contains(err.Error(), "sync = false") || len(posted) != 1 {
		t.Fatalf("an account left out: %v %v", err, posted)
	}
	cfg.Jira[0].Sync = nil
	assignee = "qa-7"
	if _, err := Transition(ctx, l, "Test", cfg); err == nil || !strings.Contains(err.Error(), "isn't assigned to the user") {
		t.Fatalf("someone else's: %v", err)
	}
	if _, err := Transition(ctx, kanban.Link{URL: "https://globex.example.com/x", Kind: KindURL}, "Test", cfg); err == nil {
		t.Fatal("not a ticket")
	}
	if _, err := Transition(ctx, kanban.Link{URL: "https://other.atlassian.net/browse/XYZ-1", Kind: KindJira, Ref: "XYZ-1"}, "Test", orgs.Config{}); err == nil {
		t.Fatal("no Jira account")
	}
	if _, err := Transition(ctx, kanban.Link{URL: "::", Kind: KindJira}, "Test", cfg); err == nil {
		t.Fatal("a broken url")
	}
}

// TestProjectStatuses: every project's statuses once each, with their category.
func TestProjectStatuses(t *testing.T) {
	status, assignee, posted := inProgress, "", []string{}
	srv, cfg, _ := fakeJira(t, &status, &assignee, &posted)
	cfg.Jira = append(cfg.Jira, orgs.Jira{URL: srv.URL}) // no projects: skipped
	got, err := Statuses(context.Background(), cfg)
	if want := "ABC: To Do (new), Test (indeterminate), Done (done)\n"; got != want || err != nil {
		t.Fatalf("%q %v", got, err)
	}
	cfg.Jira[0].Projects = []string{"NOPE"}
	if _, err := Statuses(context.Background(), cfg); err == nil {
		t.Fatal("a project that isn't there")
	}
	cfg.Jira[0].Secret = orgs.Secret{TokenEnv: "DESK_NO_SUCH_TOKEN"}
	if _, err := Statuses(context.Background(), cfg); err == nil {
		t.Fatal("a token that doesn't resolve")
	}
}
