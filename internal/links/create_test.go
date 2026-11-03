package links

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lfdominguez/desk/internal/orgs"
)

// TestCreate: a Jira ticket goes in the first project as its Task type,
// assigned to the token's account; a GitHub issue in the account's repo,
// assigned to its login; an org with neither can't create one.
func TestCreate(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/rest/api/3/issue/createmeta/ABC/issuetypes":
			w.Write([]byte(`{"issueTypes":[{"id":"1","name":"Subtarea","subtask":true},{"id":"2","name":"Error"},{"id":"3","name":"Tarea"}]}`))
		case r.URL.Path == "/rest/api/3/myself":
			w.Write([]byte(`{"accountId":"me-1"}`))
		case r.Method == "POST" && r.URL.Path == "/rest/api/3/issue":
			json.NewDecoder(r.Body).Decode(&got)
			w.Write([]byte(`{"key":"ABC-9"}`))
		case r.URL.Path == "/user":
			w.Write([]byte(`{"login":"me"}`))
		case r.Method == "POST" && r.URL.Path == "/repos/acme/api/issues":
			json.NewDecoder(r.Body).Decode(&got)
			w.Write([]byte(`{"html_url":"https://github.com/acme/api/issues/4"}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	ctx := context.Background()

	jira := orgs.Config{Jira: []orgs.Jira{{URL: srv.URL, Email: "me@example.com", Projects: []string{"ABC"}, Secret: orgs.Secret{Token: "tok"}}}}
	if w := Tracker(jira); w != "ABC (Jira)" {
		t.Fatalf("tracker %q", w)
	}
	l, err := Create(ctx, jira, "Fix login", "## Why\n\nIt **fails**.\n\n- retry")
	if err != nil || l.Ref != "ABC-9" || l.Kind != KindJira {
		t.Fatalf("jira: %+v %v", l, err)
	}
	f := got["fields"].(map[string]any)
	if f["issuetype"].(map[string]any)["id"] != "3" || f["assignee"].(map[string]any)["accountId"] != "me-1" || f["summary"] != "Fix login" {
		t.Fatalf("jira fields %v", f)
	}
	var types []string // the Markdown as Jira's own nodes, not literal ## and **
	for _, n := range f["description"].(map[string]any)["content"].([]any) {
		types = append(types, n.(map[string]any)["type"].(string))
	}
	if strings.Join(types, " ") != "heading paragraph bulletList" {
		t.Fatalf("description nodes %v", types)
	}

	defer func(a string) { githubAPI = a }(githubAPI)
	githubAPI = srv.URL
	gh := orgs.Config{GitHub: []orgs.GitHub{{Repo: "acme/api", Secret: orgs.Secret{Token: "tok"}}}}
	l, err = Create(ctx, gh, "Fix login", "It fails.")
	if err != nil || l.Kind != KindGitHubIssue || got["assignees"].([]any)[0] != "me" {
		t.Fatalf("github: %+v %v %v", l, err, got)
	}

	if _, err := Create(ctx, orgs.Config{}, "x", "y"); err == nil || Tracker(orgs.Config{}) != "" {
		t.Fatal("an org without a tracker created a ticket")
	}
}
