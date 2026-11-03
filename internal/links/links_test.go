package links

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/lfdominguez/desk/internal/kanban"
	"github.com/lfdominguez/desk/internal/orgs"
	"github.com/lfdominguez/desk/internal/vault"
)

func TestParse(t *testing.T) {
	cfg := orgs.Config{Jira: []orgs.Jira{{URL: "https://globex.atlassian.net", Projects: []string{"ABC"}}}}
	for in, want := range map[string][2]string{
		"https://github.com/globex-org/api/pull/609":                            {KindGitHubPR, "PR #609"},
		"https://github.com/globex-org/api/issues/12":                           {KindGitHubIssue, "#12"},
		"https://bitbucket.org/team/salt/pull-requests/431/diff":                {KindBitbucketPR, "PR #431"},
		"https://gitlab.com/globex/infra/charts/-/merge_requests/77":            {KindGitLabMR, "MR !77"},
		"https://globex.atlassian.net/browse/ABC-1317":                          {KindJira, "ABC-1317"},
		"https://x.atlassian.net/jira/software/c/projects/A?selectedIssue=AB-3": {KindJira, "AB-3"},
		"ABC-1291":                                        {KindJira, "ABC-1291"},
		"https://docs.example.com/page/":                  {KindURL, "docs.example.com/page"},
		"https://github.com/acme/api/commit/abc":          {KindGitHubCommit, "commit abc"},
		"https://gitlab.example.com/grp/app/-/commit/abc": {KindGitLabCommit, "commit abc"},
		// each of these matches all but one of a kind's conditions
		"https://example.com/acme/api/issues/1":               {KindURL, "example.com/acme/api/issues/1"},
		"https://example.com/acme/api/commit/abc":             {KindURL, "example.com/acme/api/commit/abc"},
		"https://github.com/acme/api/tree/main":               {KindURL, "github.com/acme/api/tree/main"},
		"https://gitlab.example.com/grp/commit/-/tree":        {KindURL, "gitlab.example.com/grp/commit/-/tree"},
		"https://example.com/team/salt/pull-requests/4":       {KindURL, "example.com/team/salt/pull-requests/4"},
		"https://bitbucket.org/team/salt/src/main":            {KindURL, "bitbucket.org/team/salt/src/main"},
		"https://gitlab.example.com/grp/app/-/tree/main":      {KindURL, "gitlab.example.com/grp/app/-/tree/main"},
		"https://gitlab.example.com/grp/app/merge_requests/1": {KindURL, "gitlab.example.com/grp/app/merge_requests/1"},
		"https://gitlab.example.com/grp/app/-/merge_requests": {KindURL, "gitlab.example.com/grp/app/-/merge_requests"},
		"https://acme.atlassian.net/wiki/home":                {KindURL, "acme.atlassian.net/wiki/home"},
		"https://acme.atlassian.net/ABC-1":                    {KindURL, "acme.atlassian.net/ABC-1"},
		"https://gitlab.example.com/grp/app/-/commit":         {KindURL, "gitlab.example.com/grp/app/-/commit"},
		"https://acme.atlassian.net/browse":                   {KindURL, "acme.atlassian.net/browse"},
	} {
		l, ok := Parse(in, cfg)
		if !ok || l.Kind != want[0] || l.Ref != want[1] {
			t.Errorf("%s: %+v %v", in, l, ok)
		}
	}
	if l, _ := Parse("ABC-1291", cfg); l.URL != "https://globex.atlassian.net/browse/ABC-1291" {
		t.Errorf("bare key URL: %s", l.URL)
	}
	for _, bad := range []string{"not a link", "ftp://x/y", "NOPE-1"} {
		if _, ok := Parse(bad, orgs.Config{}); ok {
			t.Errorf("%q parsed", bad)
		}
	}
}

func TestCheckGitHub(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Header.Get("Authorization") != "Bearer tok":
			http.Error(w, "wrong account", 401)
		case r.URL.Path == "/repos/globex-org/api/pulls/609":
			fmt.Fprint(w, `{"state":"open","merged":false,"draft":false}`)
		case r.URL.Path == "/repos/globex-org/api/pulls/609/reviews": // ana changed her mind; bo only commented
			fmt.Fprint(w, `[{"user":{"login":"ana"},"state":"CHANGES_REQUESTED"},{"user":{"login":"ana"},"state":"APPROVED"},{"user":{"login":"bo"},"state":"COMMENTED"}]`)
		case r.URL.Path == "/repos/globex-org/api/issues/12":
			fmt.Fprint(w, `{"state":"closed"}`)
		default:
			http.Error(w, "unexpected "+r.URL.String(), 404)
		}
	}))
	defer srv.Close()
	githubAPI = srv.URL
	t.Setenv("GH_GLOBEX", "tok")
	cfg := orgs.Config{GitHub: []orgs.GitHub{{Owners: []string{"other"}}, {Owners: []string{"globex-org"}, Secret: orgs.Secret{TokenEnv: "GH_GLOBEX"}}}}
	for in, want := range map[string][2]string{
		"https://github.com/globex-org/api/pull/609":  {"approved — merge", ToneGood},
		"https://github.com/globex-org/api/issues/12": {"closed", ToneDone},
	} {
		l, _ := Parse(in, cfg)
		state, tone, err := Check(context.Background(), l, cfg)
		if err != nil || state != want[0] || tone != want[1] {
			t.Errorf("%s: %q %q %v", in, state, tone, err)
		}
	}
}

// TestVaultSecret: a token in a vault that has no password yet comes back
// locked, for the window to ask; a vault desk.toml doesn't have is an error.
func TestAssigned(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/api/3/search/jql":
			fmt.Fprint(w, `{"issues":[{"key":"ABC-7","fields":{"summary":"Fix login","status":{"name":"In Progress"},"project":{"key":"ABC"}}}]}`)
		case "/issues": // a PR and another owner's issue are left out
			fmt.Fprint(w, `[{"number":3,"title":"Docs","html_url":"u3","repository":{"full_name":"globex-org/api","owner":{"login":"globex-org"}}},
				{"number":4,"title":"PR","pull_request":{},"repository":{"full_name":"globex-org/api","owner":{"login":"globex-org"}}},
				{"number":5,"title":"Theirs","repository":{"full_name":"x/y","owner":{"login":"x"}}}]`)
		case "/api/v4/issues":
			fmt.Fprint(w, `[{"iid":9,"title":"Deploy","web_url":"u9","references":{"full":"globex/ops#9"}}]`)
		default:
			http.Error(w, "unexpected "+r.URL.String(), 404)
		}
	}))
	defer srv.Close()
	githubAPI = srv.URL
	tok := orgs.Secret{Token: "t"}
	cfg := orgs.Config{Jira: []orgs.Jira{{URL: srv.URL, Email: "me", Secret: tok}}, GitHub: []orgs.GitHub{{Owners: []string{"globex-org"}, Secret: tok}},
		GitLab: []orgs.GitLab{{URL: srv.URL, Secret: tok}}}
	ts, err := Assigned(context.Background(), cfg)
	var got []string
	for _, x := range ts {
		got = append(got, x.Project+" "+x.Key+" "+x.Title)
	}
	if want := []string{"ABC ABC-7 Fix login", "globex-org/api #3 Docs", "globex/ops #9 Deploy"}; err != nil || !slices.Equal(got, want) {
		t.Errorf("%q %v", got, err)
	}
}

func TestDescribe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rest/api/2/issue/ABC-5":
			fmt.Fprint(w, `{"fields":{"summary":"Export","description":"Steps to reproduce","status":{"name":"Blocked"},"issuetype":{"name":"Task"},"priority":{"name":"Medium"},"labels":["ui"]}}`)
		case "/repos/acme/api/issues/3":
			fmt.Fprint(w, `{"title":"Fix login","state":"open","body":"It fails"}`)
		default:
			http.Error(w, "unexpected "+r.URL.String(), 404)
		}
	}))
	defer srv.Close()
	githubAPI = srv.URL
	cfg := orgs.Config{Jira: []orgs.Jira{{URL: srv.URL, Projects: []string{"ABC"}, Secret: orgs.Secret{Token: "t"}}}, GitHub: []orgs.GitHub{{Secret: tokT}}}
	for in, want := range map[string]string{
		"ABC-5":                              "ABC-5 (Task, Blocked, priority Medium, labels ui): Export\nSteps to reproduce",
		"https://github.com/acme/api/pull/3": "acme/api PR #3 (open): Fix login\nIt fails",
	} {
		l, _ := Parse(in, cfg)
		if got, err := Describe(context.Background(), l, cfg); err != nil || got != want {
			t.Errorf("%s: %q %v", in, got, err)
		}
	}
}

func TestVaultSecret(t *testing.T) {
	cfg := orgs.Config{
		Vaults: []orgs.Vault{{Name: "globex", Kind: "bitwarden", Email: "me@globex.com"}},
		GitHub: []orgs.GitHub{{Secret: orgs.Secret{Vault: "globex", Item: "GitHub token"}}},
	}
	l, _ := Parse("https://github.com/globex-org/api/pull/609", cfg)
	var locked *vault.LockedError
	if _, _, err := Check(context.Background(), l, cfg); !errors.As(err, &locked) || locked.Vault.Name != "globex" {
		t.Fatalf("want the globex vault locked, got %v", err)
	}
	cfg.GitHub[0].Vault = "other"
	if _, _, err := Check(context.Background(), l, cfg); err == nil || !strings.Contains(err.Error(), `no [[vault]] named "other"`) {
		t.Fatalf("unknown vault: %v", err)
	}
}

// TestCheckHTTP drives Jira, Bitbucket and GitLab against one fake server.
func TestCheckHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, _ := r.BasicAuth()
		switch {
		case r.URL.Path == "/rest/api/3/issue/TB-1" && user == "me@x" && pass == "jt":
			fmt.Fprint(w, `{"fields":{"status":{"name":"In Review","statusCategory":{"key":"indeterminate"}}}}`)
		case r.URL.Path == "/bb/repositories/ws/repo/pullrequests/4" && r.Header.Get("Authorization") == "Bearer bt":
			fmt.Fprint(w, `{"state":"OPEN","participants":[{"approved":false},{"approved":true}]}`)
		case r.URL.EscapedPath() == "/api/v4/projects/grp%2Fsub%2Fproj/merge_requests/7" && r.Header.Get("PRIVATE-TOKEN") == "gt":
			fmt.Fprint(w, `{"state":"merged"}`)
		default:
			http.Error(w, "unexpected "+r.URL.String(), 404)
		}
	}))
	defer srv.Close()
	bitbucketAPI = srv.URL + "/bb"
	cfg := orgs.Config{
		Jira:      []orgs.Jira{{URL: srv.URL, Email: "me@x", Secret: orgs.Secret{Token: "jt"}}},
		Bitbucket: []orgs.Bitbucket{{Workspace: "ws", Secret: orgs.Secret{Token: "bt"}}},
		GitLab:    []orgs.GitLab{{URL: srv.URL, Secret: orgs.Secret{Token: "gt"}}},
	}
	for _, c := range []struct {
		link        kanban.Link
		state, tone string
	}{
		{kanban.Link{URL: srv.URL + "/browse/TB-1", Kind: KindJira, Ref: "TB-1"}, "In Review", ToneWait},
		{kanban.Link{URL: "https://bitbucket.org/ws/repo/pull-requests/4", Kind: KindBitbucketPR}, "approved — merge", ToneGood},
		{kanban.Link{URL: srv.URL + "/grp/sub/proj/-/merge_requests/7", Kind: KindGitLabMR}, "merged", ToneDone},
	} {
		state, tone, err := Check(context.Background(), c.link, cfg)
		if err != nil || state != c.state || tone != c.tone {
			t.Errorf("%s: %q %q %v", c.link.Kind, state, tone, err)
		}
	}
	if _, _, err := Check(context.Background(), kanban.Link{URL: srv.URL + "/browse/TB-9", Kind: KindJira, Ref: "TB-9"}, cfg); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("a missing issue should error: %v", err)
	}
}
