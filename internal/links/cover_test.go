package links

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/md5"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/lfdominguez/desk/internal/kanban"
	"github.com/lfdominguez/desk/internal/orgs"
)

// routed answers routes: "METHOD /path" or "/path" (escaped) → body; a body
// "!N" answers status N; anything else 404.
func routed(routes map[string]string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := routes[r.Method+" "+r.URL.EscapedPath()]
		if !ok {
			body, ok = routes[r.URL.EscapedPath()]
		}
		if !ok {
			body = "!404"
		}
		if code, isCode := strings.CutPrefix(body, "!"); isCode {
			n, _ := strconv.Atoi(code)
			w.WriteHeader(n)
			return
		}
		fmt.Fprint(w, body)
	})
}

func fake(t *testing.T, routes map[string]string) *httptest.Server {
	srv := httptest.NewServer(routed(routes))
	t.Cleanup(srv.Close)
	return srv
}

// fakeTLS is a fake on https, with the package's client trusting it, for
// the account hosts that become https://host/api/v3.
func fakeTLS(t *testing.T, routes map[string]string) *httptest.Server {
	srv := httptest.NewTLSServer(routed(routes))
	old := client
	client = srv.Client()
	t.Cleanup(func() { client = old; srv.Close() })
	return srv
}

// dead is a URL nothing answers on.
func dead() string {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	return srv.URL
}

func useGitHub(t *testing.T, api string) {
	old := githubAPI
	githubAPI = api
	t.Cleanup(func() { githubAPI = old })
}

// badSecret names a vault desk.toml doesn't have: every read of it fails.
var badSecret = orgs.Secret{Vault: "missing", Item: "x"}

// tokT is an account's token: GitHub, GitLab and Bitbucket are asked only
// with one.
var tokT = orgs.Secret{Token: "t"}

// ghT is an org with a github.com account.
var ghT = orgs.Config{GitHub: []orgs.GitHub{{Secret: tokT}}}

var ctx = context.Background()

func noAuth(*http.Request) {}

func TestDoJSONErrors(t *testing.T) {
	if err := doJSON(ctx, "POST", "http://127.0.0.1:1", noAuth, make(chan int), nil); err == nil {
		t.Error("an unmarshalable body sent")
	}
	if err := doJSON(ctx, "GET", ":", noAuth, nil, nil); err == nil {
		t.Error("a bad URL requested")
	}
	if err := doJSON(ctx, "GET", dead(), noAuth, nil, nil); err == nil {
		t.Error("no server, no error")
	}
}

func TestGitHubRepo(t *testing.T) {
	u, _ := url.Parse("https://ghe.example.com/acme/api/pull/1")
	p := []string{"acme", "api", "pull", "1"}
	if api, _, err := githubRepo(ctx, u, p, ghT); err != nil || api != "https://ghe.example.com/api/v3/repos/acme/api" {
		t.Errorf("enterprise: %q %v", api, err)
	}
	if _, _, err := githubRepo(ctx, u, p, orgs.Config{GitHub: []orgs.GitHub{{Secret: badSecret}}}); err == nil {
		t.Error("a failing secret: no error")
	}
}

func TestCheckGitHubStates(t *testing.T) {
	srv := fake(t, map[string]string{
		"/repos/acme/api/issues/1":        `{"state":"open"}`,
		"/repos/acme/api/pulls/2":         `{"merged":true,"state":"closed"}`,
		"/repos/acme/api/pulls/3":         `{"state":"closed"}`,
		"/repos/acme/api/pulls/4":         `{"state":"open","draft":true}`,
		"/repos/acme/api/pulls/5":         `{"state":"open"}`,
		"/repos/acme/api/pulls/5/reviews": `[{"user":{"login":"ana"},"state":"APPROVED"},{"user":{"login":"bo"},"state":"CHANGES_REQUESTED"}]`,
		"/repos/acme/api/pulls/6":         `{"state":"open"}`,
		"/repos/acme/api/pulls/6/reviews": `[{"user":{"login":"bo"},"state":"COMMENTED"},{"user":{"login":"cy"},"state":"PENDING"}]`,
		"/repos/acme/api/pulls/7":         `{"state":"open"}`,
	})
	useGitHub(t, srv.URL)
	cfg := ghT
	for in, want := range map[string][2]string{
		"https://github.com/acme/api/issues/1": {"open", ToneWait},
		"https://github.com/acme/api/pull/2":   {"merged", ToneDone},
		"https://github.com/acme/api/pull/3":   {"closed", ToneBad},
		"https://github.com/acme/api/pull/4":   {"draft", ToneWait},
		"https://github.com/acme/api/pull/5":   {"changes requested", ToneBad},
		"https://github.com/acme/api/pull/6":   {"waiting", ToneWait},
	} {
		l, _ := Parse(in, cfg)
		if state, tone, err := check(ctx, l, cfg); err != nil || state != want[0] || tone != want[1] {
			t.Errorf("%s: %q %q %v", in, state, tone, err)
		}
	}
	for _, in := range []string{"https://github.com/acme/api/pull/7", "https://github.com/acme/api/issues/8", "https://github.com/acme/api/pull/9"} {
		l, _ := Parse(in, cfg)
		if _, _, err := check(ctx, l, cfg); err == nil || !strings.Contains(err.Error(), "404") {
			t.Errorf("%s: %v", in, err)
		}
	}
	l, _ := Parse("https://github.com/acme/api/pull/2", cfg)
	if _, _, err := check(ctx, l, orgs.Config{GitHub: []orgs.GitHub{{Secret: badSecret}}}); err == nil {
		t.Error("a failing secret: no error")
	}
}

func TestCheckJiraMore(t *testing.T) {
	srv := fake(t, map[string]string{
		"/rest/api/3/issue/ABC-1": `{"fields":{"status":{"name":"Done","statusCategory":{"key":"done"}}}}`,
	})
	// no URL: the link's host is the Jira
	cfg := orgs.Config{Jira: []orgs.Jira{{Projects: []string{"ABC"}, Secret: orgs.Secret{Token: "t"}}}}
	l := kanban.Link{URL: srv.URL + "/browse/ABC-1", Kind: KindJira, Ref: "ABC-1"}
	if state, tone, err := check(ctx, l, cfg); err != nil || state != "Done" || tone != ToneDone {
		t.Errorf("done: %q %q %v", state, tone, err)
	}
	cfg.Jira[0].Secret = badSecret
	if _, _, err := check(ctx, l, cfg); err == nil {
		t.Error("a failing secret: no error")
	}
	if _, _, err := check(ctx, l, orgs.Config{}); err == nil || !strings.Contains(err.Error(), "no [[jira]]") {
		t.Errorf("no account: %v", err)
	}
}

func TestCheckBitbucketStates(t *testing.T) {
	srv := fake(t, map[string]string{
		"/repositories/ws/repo/pullrequests/1": `{"state":"MERGED"}`,
		"/repositories/ws/repo/pullrequests/2": `{"state":"DECLINED"}`,
		"/repositories/ws/repo/pullrequests/3": `{"state":"SUPERSEDED"}`,
		"/repositories/ws/repo/pullrequests/4": `{"state":"OPEN","participants":[{"approved":true},{"state":"changes_requested"}]}`,
		"/repositories/ws/repo/pullrequests/5": `{"state":"OPEN","participants":[{"approved":false}]}`,
	})
	old := bitbucketAPI
	bitbucketAPI = srv.URL
	t.Cleanup(func() { bitbucketAPI = old })
	cfg := orgs.Config{Bitbucket: []orgs.Bitbucket{{Workspace: "other", Secret: badSecret}, {Workspace: "ws", Secret: tokT}}}
	for n, want := range map[string][2]string{
		"1": {"merged", ToneDone}, "2": {"declined", ToneBad}, "3": {"superseded", ToneBad},
		"4": {"changes requested", ToneBad}, "5": {"waiting", ToneWait},
	} {
		l := kanban.Link{URL: "https://bitbucket.org/ws/repo/pull-requests/" + n, Kind: KindBitbucketPR}
		if state, tone, err := check(ctx, l, cfg); err != nil || state != want[0] || tone != want[1] {
			t.Errorf("%s: %q %q %v", n, state, tone, err)
		}
	}
	l := kanban.Link{URL: "https://bitbucket.org/ws/repo/pull-requests/6", Kind: KindBitbucketPR}
	if _, _, err := check(ctx, l, cfg); err == nil {
		t.Error("a missing PR: no error")
	}
	l.URL = "https://bitbucket.org/other/repo/pull-requests/1" // the other workspace's account
	if _, _, err := check(ctx, l, cfg); err == nil || !strings.Contains(err.Error(), "no [[vault]]") {
		t.Errorf("a failing secret: %v", err)
	}
}

func TestCheckGitLabStates(t *testing.T) {
	const p = "/api/v4/projects/grp%2Fapp/merge_requests/"
	srv := fake(t, map[string]string{
		p + "1": `{"state":"closed"}`,
		p + "2": `{"state":"opened","draft":true}`,
		p + "3": `{"state":"opened"}`, p + "3/approvals": `{"approved":true}`,
		p + "4": `{"state":"opened"}`, // approvals 404: waiting
		p + "6": `{"state":"opened"}`, p + "6/approvals": `{"approved":false}`,
	})
	cfg := orgs.Config{GitLab: []orgs.GitLab{{URL: srv.URL, Secret: tokT}}}
	for n, want := range map[string][2]string{
		"1": {"closed", ToneBad}, "2": {"draft", ToneWait}, "3": {"approved — merge", ToneGood},
		"4": {"waiting", ToneWait}, "6": {"waiting", ToneWait},
	} {
		l := kanban.Link{URL: srv.URL + "/grp/app/-/merge_requests/" + n, Kind: KindGitLabMR}
		if state, tone, err := check(ctx, l, cfg); err != nil || state != want[0] || tone != want[1] {
			t.Errorf("%s: %q %q %v", n, state, tone, err)
		}
	}
	if _, _, err := check(ctx, kanban.Link{URL: srv.URL + "/grp/app/-/merge_requests/5", Kind: KindGitLabMR}, cfg); err == nil {
		t.Error("a missing MR: no error")
	}
	if _, _, err := check(ctx, kanban.Link{URL: srv.URL + "/grp/app/merge_requests/1", Kind: KindGitLabMR}, cfg); err == nil || !strings.Contains(err.Error(), "not a GitLab project URL") {
		t.Errorf("no /-/: %v", err)
	}
	bad := orgs.Config{GitLab: []orgs.GitLab{{URL: srv.URL, Secret: badSecret}}}
	if _, _, err := check(ctx, kanban.Link{URL: srv.URL + "/grp/app/-/merge_requests/1", Kind: KindGitLabMR}, bad); err == nil {
		t.Error("a failing secret: no error")
	}
}

func TestParseMore(t *testing.T) {
	long := "https://docs.example.com/" + strings.Repeat("a", 60)
	if l, ok := Parse(long, orgs.Config{}); !ok || !strings.HasSuffix(l.Ref, "…") || len([]rune(l.Ref)) != 48 {
		t.Errorf("long ref %q", l.Ref)
	}
	ghe := orgs.Config{GitHub: []orgs.GitHub{{Host: "git.example.com"}}}
	if l, _ := Parse("https://git.example.com/acme/api/pull/3", ghe); l.Kind != KindGitHubPR {
		t.Errorf("an account's GitHub host: %+v", l)
	}
	if l, _ := Parse("https://other.example.com/acme/api/pull/3", ghe); l.Kind != KindURL {
		t.Errorf("another host taken for GitHub: %+v", l)
	}
	one := orgs.Config{Jira: []orgs.Jira{{URL: "https://acme.atlassian.net"}}}
	if l, ok := Parse("XYZ-1", one); !ok || l.URL != "https://acme.atlassian.net/browse/XYZ-1" {
		t.Errorf("the only Jira: %+v %v", l, ok)
	}
	two := orgs.Config{Jira: []orgs.Jira{{URL: "https://acme.atlassian.net"}, {URL: "https://globex.atlassian.net"}}}
	if _, ok := Parse("XYZ-1", two); ok {
		t.Error("a key no account owns, among two, parsed")
	}
	owner := orgs.Config{Jira: []orgs.Jira{{}, {URL: "https://globex.atlassian.net", Projects: []string{"XYZ"}}}}
	if l, ok := Parse("XYZ-1", owner); !ok || l.URL != "https://globex.atlassian.net/browse/XYZ-1" {
		t.Errorf("the project's Jira, not the first: %+v %v", l, ok)
	}
}

func TestHasCI(t *testing.T) {
	for kind, want := range map[string]bool{
		KindGitHubPR: true, KindGitHubCommit: true, KindGitLabMR: true, KindGitLabCommit: true,
		KindGitHubIssue: false, KindJira: false, KindURL: false,
	} {
		if HasCI(kanban.Link{Kind: kind}) != want {
			t.Errorf("%s: want %v", kind, want)
		}
	}
}

// TestGitLabAccount: an account answers only for its own host, gitlab.com's
// when it has no URL; without one, no account.
func TestGitLabAccount(t *testing.T) {
	u, _ := url.Parse("https://git.example.com/grp/app/-/merge_requests/1")
	p := []string{"grp", "app", "-", "merge_requests", "1"}
	for name, g := range map[string]orgs.GitLab{
		"no URL":       {Secret: badSecret},
		"another host": {URL: "https://gitlab.example.com", Secret: badSecret},
	} {
		var na *NoAccountError
		if _, _, err := gitlabProject(ctx, u, p, orgs.Config{GitLab: []orgs.GitLab{g}}); !errors.As(err, &na) {
			t.Errorf("%s: its account taken: %v", name, err)
		}
	}
	_, a, err := gitlabProject(ctx, u, p, orgs.Config{GitLab: []orgs.GitLab{{URL: "https://git.example.com", Secret: tokT}}})
	if err != nil {
		t.Fatal(err)
	}
	r, _ := http.NewRequest("GET", "https://example.com", nil)
	a(r)
	if got := r.Header.Get("PRIVATE-TOKEN"); got != "t" {
		t.Errorf("the account's token: %q", got)
	}
}

// TestGitHubAccount: an account of another host isn't github.com's, and
// GH_TOKEN doesn't stand in for one.
func TestGitHubAccount(t *testing.T) {
	t.Setenv("GH_TOKEN", "envtok")
	t.Setenv("GITHUB_TOKEN", "envtok")
	u, _ := url.Parse("https://github.com/acme/api/pull/1")
	p := []string{"acme", "api", "pull", "1"}
	var na *NoAccountError
	if _, _, err := githubRepo(ctx, u, p, orgs.Config{GitHub: []orgs.GitHub{{Host: "ghe.example.com", Secret: badSecret}}}); !errors.As(err, &na) {
		t.Fatalf("another host's account taken, or the env's token: %v", err)
	}
	_, a, _ := githubRepo(ctx, u, p, ghT)
	r, _ := http.NewRequest("GET", "https://example.com", nil)
	a(r)
	if got := r.Header.Get("Authorization"); got != "Bearer t" {
		t.Errorf("the account's token: %q", got)
	}
}

// TestNoAccount: no matching account, or one whose token resolves empty, is
// a NoAccountError naming the provider and owner, and nothing is asked.
func TestNoAccount(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer srv.Close()
	useGitHub(t, srv.URL)
	old := bitbucketAPI
	bitbucketAPI = srv.URL
	t.Cleanup(func() { bitbucketAPI = old })
	t.Setenv("GH_TOKEN", "envtok")
	empty := orgs.Secret{TokenCmd: "exit 0"} // resolves to "" (sh and cmd alike)
	gh := kanban.Link{URL: "https://github.com/acme/api/pull/1", Kind: KindGitHubPR}
	bb := kanban.Link{URL: "https://bitbucket.org/ws/repo/pull-requests/1", Kind: KindBitbucketPR}
	gl := kanban.Link{URL: srv.URL + "/grp/app/-/merge_requests/1", Kind: KindGitLabMR}
	for name, c := range map[string]struct {
		l               kanban.Link
		cfg             orgs.Config
		provider, owner string
	}{
		"github: none":       {gh, orgs.Config{GitHub: []orgs.GitHub{{Owners: []string{"globex-org"}, Secret: tokT}}}, "GitHub", "acme"},
		"github: empty":      {gh, orgs.Config{GitHub: []orgs.GitHub{{Secret: empty}}}, "GitHub", "acme"},
		"github CI: none":    {gh, orgs.Config{}, "GitHub", "acme"},
		"bitbucket: none":    {bb, orgs.Config{Bitbucket: []orgs.Bitbucket{{Workspace: "other", Secret: tokT}}}, "Bitbucket", "ws"},
		"bitbucket: empty":   {bb, orgs.Config{Bitbucket: []orgs.Bitbucket{{Workspace: "ws", User: "me", Secret: empty}}}, "Bitbucket", "ws"},
		"gitlab: none":       {gl, orgs.Config{GitLab: []orgs.GitLab{{URL: srv.URL, Groups: []string{"other"}, Secret: tokT}}}, "GitLab", "grp"},
		"gitlab: empty":      {gl, orgs.Config{GitLab: []orgs.GitLab{{URL: srv.URL, Secret: empty}}}, "GitLab", "grp"},
		"gitlab CI: no conf": {gl, orgs.Config{}, "GitLab", "grp"},
	} {
		_, _, err := Check(ctx, c.l, c.cfg)
		if strings.Contains(name, "CI") {
			_, err = CI(ctx, c.l, c.cfg)
		}
		var na *NoAccountError
		if !errors.As(err, &na) || na.Provider != c.provider || na.Owner != c.owner {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if want := "no " + c.provider + " account for " + c.owner + " in this organization's desk.toml"; err.Error() != want {
			t.Errorf("%s: %q", name, err)
		}
	}
	if hits != 0 {
		t.Errorf("%d requests made without an account", hits)
	}
}

func TestDoJSONContentType(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Method+" "+r.Header.Get("Content-Type"))
	}))
	defer srv.Close()
	doJSON(ctx, "POST", srv.URL, noAuth, map[string]int{"a": 1}, nil)
	doJSON(ctx, "GET", srv.URL, noAuth, nil, nil)
	if strings.Join(got, ",") != "POST application/json,GET " {
		t.Errorf("%q", got)
	}
}

// TestNagiosDigestOnly401: digest is answered only to a 401 asking for it,
// with a user.
func TestNagiosDigestOnly401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.Header.Get("Authorization"), "Digest ") {
			fmt.Fprint(w, `{}`)
			return
		}
		w.Header().Set("WWW-Authenticate", `Digest realm="r", nonce="n"`)
		code, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/"))
		w.WriteHeader(code)
	}))
	defer srv.Close()
	if _, err := nagiosFetch(ctx, srv.URL+"/403", "me", "pw"); err == nil {
		t.Error("a 403 answered with digest")
	}
	if _, err := nagiosFetch(ctx, srv.URL+"/401", "", "pw"); err == nil {
		t.Error("digest answered without a user")
	}
}

// TestCheckCI: a commit's chip is its CI alone; a PR's CI joins its state,
// running turning an approval to wait, passing leaving it as it is; no CI or
// a CI error leaves the state alone.
func TestCheckCI(t *testing.T) {
	srv := fake(t, map[string]string{
		"/repos/acme/run/pulls/1":             `{"state":"open"}`,
		"/repos/acme/run/pulls/1/reviews":     `[{"user":{"login":"ana"},"state":"APPROVED"}]`,
		"/repos/acme/run/actions/runs":        `{"workflow_runs":[{"id":7,"status":"in_progress"}]}`,
		"/repos/acme/run/actions/runs/7/jobs": `{"jobs":[]}`,
		"/repos/acme/ok/pulls/2":              `{"state":"open"}`,
		"/repos/acme/ok/pulls/2/reviews":      `[]`,
		"/repos/acme/ok/actions/runs":         `{"workflow_runs":[{"id":8,"status":"completed","conclusion":"success"}]}`,
		"/repos/acme/ok/actions/runs/8/jobs":  `{"jobs":[{"id":81,"status":"queued"}]}`,
		"/repos/acme/ok/pulls/4":              `{"state":"closed","merged":true}`,
		"/repos/acme/ok/pulls/5":              `{"state":"closed"}`,
		"/repos/acme/run/pulls/6":             `{"state":"open"}`,
		"/repos/acme/run/pulls/6/reviews":     `[{"user":{"login":"ana"},"state":"CHANGES_REQUESTED"}]`,
		"/repos/acme/none/pulls/3":            `{"state":"open"}`,
		"/repos/acme/none/pulls/3/reviews":    `[]`,
		"/repos/acme/none/actions/runs":       `{"workflow_runs":[]}`,
	})
	useGitHub(t, srv.URL)
	cfg := ghT
	for in, want := range map[string][2]string{
		"https://github.com/acme/run/pull/1":    {"approved — merge · CI running", ToneWait},
		"https://github.com/acme/ok/pull/2":     {"waiting · CI passed", ToneWait},
		"https://github.com/acme/none/pull/3":   {"waiting", ToneWait},
		"https://github.com/acme/ok/pull/4":     {"merged", ToneDone},
		"https://github.com/acme/ok/pull/5":     {"closed", ToneBad},
		"https://github.com/acme/run/pull/6":    {"changes requested · CI running", ToneBad},
		"https://github.com/acme/ok/commit/abc": {"CI passed", ToneGood},
		"https://github.com/acme/none/commit/a": {"no CI", ToneWait},
	} {
		l, _ := Parse(in, cfg)
		if state, tone, err := Check(ctx, l, cfg); err != nil || state != want[0] || tone != want[1] {
			t.Errorf("%s: %q %q %v", in, state, tone, err)
		}
	}
	l, _ := Parse("https://github.com/acme/err/commit/abc", cfg)
	if _, _, err := Check(ctx, l, cfg); err == nil {
		t.Error("a commit's CI error: none")
	}
	if _, _, err := Check(ctx, kanban.Link{URL: "%zz", Kind: KindJira}, cfg); err == nil {
		t.Error("a bad URL: no error")
	}
	if state, tone, err := Check(ctx, kanban.Link{URL: "https://docs.example.com/x", Kind: KindURL}, cfg); state != "" || tone != "" || err != nil {
		t.Errorf("a plain URL has no state: %q %q %v", state, tone, err)
	}
}

func TestCIErrors(t *testing.T) {
	srv := fake(t, map[string]string{
		"/repos/acme/nojobs/actions/runs":             `{"workflow_runs":[{"id":1,"status":"completed"}]}`,
		"/api/v4/projects/grp%2Fapp/pipelines":        `[{"id":9,"status":"failed"}]`,
		"/api/v4/projects/grp%2Fapp/pipelines/9/jobs": `[{"id":91,"status":"failed"}]`,
		"/api/v4/projects/grp%2Fnojobs/pipelines":     `[{"id":9,"status":"failed"}]`,
	})
	useGitHub(t, srv.URL)
	cfg := orgs.Config{GitHub: []orgs.GitHub{{Secret: tokT}}, GitLab: []orgs.GitLab{{URL: srv.URL, Secret: tokT}}}
	bad := orgs.Config{GitHub: []orgs.GitHub{{Secret: badSecret}}, GitLab: []orgs.GitLab{{URL: srv.URL, Secret: badSecret}}}
	gl := func(path string) kanban.Link { return kanban.Link{URL: srv.URL + path, Kind: KindGitLabCommit} }

	runs, err := CI(ctx, gl("/grp/app/-/commit/abc"), cfg)
	if err != nil || len(runs) != 1 || runs[0].Status != CIFailed || runs[0].Jobs[0].ID != 91 {
		t.Errorf("a GitLab commit's pipeline: %+v %v", runs, err)
	}
	for name, c := range map[string]struct {
		l   kanban.Link
		cfg orgs.Config
	}{
		"bad URL":         {kanban.Link{URL: "%zz", Kind: KindGitHubPR}, cfg},
		"no CI kind":      {kanban.Link{URL: "https://example.com", Kind: KindJira, Ref: "ABC-1"}, cfg},
		"github secret":   {kanban.Link{URL: "https://github.com/acme/api/pull/1", Kind: KindGitHubPR}, bad},
		"github PR":       {kanban.Link{URL: "https://github.com/acme/api/pull/1", Kind: KindGitHubPR}, cfg},
		"github jobs":     {kanban.Link{URL: "https://github.com/acme/nojobs/commit/abc", Kind: KindGitHubCommit}, cfg},
		"gitlab secret":   {gl("/grp/app/-/commit/abc"), bad},
		"gitlab pipeline": {gl("/grp/none/-/commit/abc"), cfg},
		"gitlab jobs":     {gl("/grp/nojobs/-/commit/abc"), cfg},
	} {
		if _, err := CI(ctx, c.l, c.cfg); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if _, err := CI(ctx, kanban.Link{URL: "https://example.com", Kind: KindJira, Ref: "ABC-1"}, cfg); err == nil || !strings.Contains(err.Error(), "ABC-1 has no CI") {
		t.Errorf("no CI kind: %v", err)
	}
}

func TestJobLogErrors(t *testing.T) {
	srv := fake(t, nil)
	pr := kanban.Link{URL: "https://github.com/acme/api/pull/1", Kind: KindGitHubPR}
	for name, c := range map[string]struct {
		l   kanban.Link
		api string
		cfg orgs.Config
	}{
		"bad URL":      {kanban.Link{URL: "%zz", Kind: KindGitHubPR}, srv.URL, orgs.Config{}},
		"no CI kind":   {kanban.Link{URL: "https://example.com", Kind: KindJira, Ref: "ABC-1"}, srv.URL, orgs.Config{}},
		"secret":       {pr, srv.URL, orgs.Config{GitHub: []orgs.GitHub{{Secret: badSecret}}}},
		"bad API":      {pr, "http://[::1", ghT},
		"no server":    {pr, dead(), ghT},
		"404":          {pr, srv.URL, ghT},
		"gitlab: no -": {kanban.Link{URL: srv.URL + "/grp/app/commit/a", Kind: KindGitLabCommit}, srv.URL, orgs.Config{GitLab: []orgs.GitLab{{URL: srv.URL, Secret: tokT}}}},
	} {
		useGitHub(t, c.api)
		if _, err := JobLog(ctx, c.l, c.cfg, 1, 10); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestSummary(t *testing.T) {
	for want, st := range map[string][]string{
		"":           nil,
		"CI failed":  {CISuccess, CIFailed, CIRunning},
		"CI running": {CISuccess, CIPending},
		"CI passed":  {CISuccess, CISkipped, CICanceled},
	} {
		var runs []Run
		for _, s := range st {
			runs = append(runs, Run{Status: s})
		}
		if got, _ := Summary(runs); got != want {
			t.Errorf("%v: %q, want %q", st, got, want)
		}
	}
	if _, tone := Summary([]Run{{Status: CIRunning}}); tone != ToneWait {
		t.Errorf("running tone %q", tone)
	}
}

func TestStatuses(t *testing.T) {
	for in, want := range map[[2]string]string{
		{"in_progress", ""}: CIRunning, {"queued", ""}: CIPending,
		{"completed", "success"}: CISuccess, {"completed", "neutral"}: CISuccess,
		{"completed", "cancelled"}: CICanceled, {"completed", "skipped"}: CISkipped, {"completed", "stale"}: CISkipped,
		{"completed", "action_required"}: CIPending, {"completed", "timed_out"}: CIFailed,
	} {
		if got := githubStatus(in[0], in[1]); got != want {
			t.Errorf("github %v: %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"success": CISuccess, "failed": CIFailed, "running": CIRunning, "canceled": CICanceled,
		"canceling": CICanceled, "skipped": CISkipped, "manual": CISkipped, "created": CIPending,
	} {
		if got := gitlabStatus(in); got != want {
			t.Errorf("gitlab %s: %q, want %q", in, got, want)
		}
	}
}

func TestCreateMore(t *testing.T) {
	if got := Tracker(orgs.Config{Jira: []orgs.Jira{{URL: "https://acme.atlassian.net"}}, GitHub: []orgs.GitHub{{Repo: "acme"}, {Repo: "acme/api"}}}); got != "acme/api (GitHub)" {
		t.Errorf("tracker %q", got)
	}
	var posted map[string]any
	meta := `{"values":[{"id":"1","name":"Sub","subtask":true},{"id":"2","name":"Bug"}]}`
	jiraSrv := func(routes map[string]string) *httptest.Server {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "POST" && r.URL.Path == "/rest/api/3/issue" && routes["POST /rest/api/3/issue"] == "" {
				json.NewDecoder(r.Body).Decode(&posted)
				fmt.Fprint(w, `{"key":"ABC-2"}`)
				return
			}
			routed(routes).ServeHTTP(w, r)
		}))
		t.Cleanup(srv.Close)
		return srv
	}
	jira := func(srv *httptest.Server, typ string) orgs.Config {
		return orgs.Config{Jira: []orgs.Jira{{URL: srv.URL, Projects: []string{"ABC"}, IssueType: typ, Secret: orgs.Secret{Token: "t"}}}}
	}
	ok := map[string]string{"/rest/api/3/issue/createmeta/ABC/issuetypes": meta, "/rest/api/3/myself": `{"accountId":"me-1"}`}
	srv := jiraSrv(ok)
	if l, err := Create(ctx, jira(srv, ""), "T", "b"); err != nil || l.Ref != "ABC-2" || posted["fields"].(map[string]any)["issuetype"].(map[string]any)["id"] != "2" {
		t.Errorf("no Task: the first non-subtask type: %+v %v %v", l, err, posted)
	}
	if _, err := Create(ctx, jira(srv, "Story"), "T", "b"); err == nil || !strings.Contains(err.Error(), `no issue type "Story"`) {
		t.Errorf("a type it lacks: %v", err)
	}
	var deep strings.Builder // lists nested past ADF's depth limit
	for i := range 80 {
		deep.WriteString(strings.Repeat("  ", i) + "- x\n")
	}
	if _, err := Create(ctx, jira(srv, ""), "T", deep.String()); err == nil || !strings.Contains(err.Error(), "description") {
		t.Errorf("a description ADF refuses: %v", err)
	}
	bad := jira(srv, "")
	bad.Jira[0].Secret = badSecret
	if _, err := Create(ctx, bad, "T", "b"); err == nil {
		t.Error("a failing secret: no error")
	}
	for name, routes := range map[string]map[string]string{
		"createmeta": {"/rest/api/3/myself": `{"accountId":"me-1"}`},
		"myself":     {"/rest/api/3/issue/createmeta/ABC/issuetypes": meta},
		"post":       {"/rest/api/3/issue/createmeta/ABC/issuetypes": meta, "/rest/api/3/myself": `{}`, "POST /rest/api/3/issue": "!500"},
	} {
		if _, err := Create(ctx, jira(jiraSrv(routes), ""), "T", "b"); err == nil {
			t.Errorf("%s fails: no error", name)
		}
	}

	gh := fakeTLS(t, map[string]string{
		"/api/v3/user":                        `{"login":"me"}`,
		"POST /api/v3/repos/acme/api/issues":  `{"html_url":"https://ghe.example.com/acme/api/issues/5"}`,
		"POST /api/v3/repos/acme/link/issues": `{"html_url":""}`,
		"POST /api/v3/repos/acme/fail/issues": "!500",
	})
	host := strings.TrimPrefix(gh.URL, "https://")
	ghCfg := func(repo string, s orgs.Secret) orgs.Config {
		return orgs.Config{GitHub: []orgs.GitHub{{Host: host, Repo: repo, Secret: s}}}
	}
	if l, err := Create(ctx, ghCfg("acme/api", orgs.Secret{Token: "t"}), "T", "b"); err != nil || l.URL != "https://ghe.example.com/acme/api/issues/5" {
		t.Errorf("enterprise: %+v %v", l, err)
	}
	if _, err := Create(ctx, ghCfg("acme/link", orgs.Secret{Token: "t"}), "T", "b"); err == nil || !strings.Contains(err.Error(), "isn't a link") {
		t.Errorf("no URL back: %v", err)
	}
	if _, err := Create(ctx, ghCfg("acme/fail", orgs.Secret{Token: "t"}), "T", "b"); err == nil {
		t.Error("a failing POST: no error")
	}
	if _, err := Create(ctx, ghCfg("acme/api", badSecret), "T", "b"); err == nil {
		t.Error("a failing secret: no error")
	}
	useGitHub(t, fake(t, nil).URL)
	if _, err := Create(ctx, orgs.Config{GitHub: []orgs.GitHub{{Repo: "acme/api"}}}, "T", "b"); err == nil {
		t.Error("a failing /user: no error")
	}
}

func TestDescribeMore(t *testing.T) {
	srv := fake(t, map[string]string{
		"/rest/api/2/issue/ABC-1":  `{"fields":{"summary":"S","status":{"name":"Open"},"issuetype":{"name":"Bug"}}}`,
		"/repos/acme/api/issues/2": `{"title":"T","state":"open","body":"` + strings.Repeat("é", 4100) + `"}`,
	})
	useGitHub(t, srv.URL)
	cfg := orgs.Config{Jira: []orgs.Jira{{Projects: []string{"ABC"}, Secret: orgs.Secret{Token: "t"}}}, GitHub: []orgs.GitHub{{Secret: tokT}}}
	jl := kanban.Link{URL: srv.URL + "/browse/ABC-1", Kind: KindJira, Ref: "ABC-1"}
	if got, err := Describe(ctx, jl, cfg); err != nil || got != "ABC-1 (Bug, Open, labels ): S\n" {
		t.Errorf("no priority, the link's host: %q %v", got, err)
	}
	gl, _ := Parse("https://github.com/acme/api/issues/2", cfg)
	if got, err := Describe(ctx, gl, cfg); err != nil || !strings.HasSuffix(got, strings.Repeat("é", 4000)+"…") || strings.Contains(got, strings.Repeat("é", 4001)) {
		t.Errorf("a long body clipped: %d %v", len(got), err)
	}
	if got, err := Describe(ctx, kanban.Link{URL: "https://example.com", Kind: KindURL}, cfg); got != "" || err != nil {
		t.Errorf("a plain URL: %q %v", got, err)
	}
	bad := orgs.Config{Jira: []orgs.Jira{{Projects: []string{"ABC"}, Secret: badSecret}}, GitHub: []orgs.GitHub{{Secret: badSecret}}}
	for name, c := range map[string]struct {
		l   kanban.Link
		cfg orgs.Config
	}{
		"bad URL":       {kanban.Link{URL: "%zz", Kind: KindJira}, cfg},
		"no jira":       {jl, orgs.Config{}},
		"jira secret":   {jl, bad},
		"jira 404":      {kanban.Link{URL: srv.URL + "/browse/ABC-9", Kind: KindJira, Ref: "ABC-9"}, cfg},
		"github secret": {gl, bad},
		"github 404":    {kanban.Link{URL: "https://github.com/acme/api/issues/9", Kind: KindGitHubIssue}, cfg},
	} {
		if _, err := Describe(ctx, c.l, c.cfg); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

// fakeBitwarden is a Vaultwarden with one login, "Nagios" (password pw),
// whose username is user's encryption, or a broken one; its password is in
// the returned vault's config, so it unlocks by itself.
func fakeBitwarden(t *testing.T, user string, broken bool) orgs.Vault {
	const email, pw = "me@example.com", "hunter2"
	master, _ := pbkdf2.Key(sha256.New, pw, []byte(email), 1, 32)
	enc, _ := hkdf.Expand(sha256.New, master, "enc", 32)
	mac, _ := hkdf.Expand(sha256.New, master, "mac", 32)
	raw := make([]byte, 64)
	rand.Read(raw)
	encrypt := func(e, m, plain []byte) string {
		n := aes.BlockSize - len(plain)%aes.BlockSize
		data := append(append([]byte{}, plain...), strings.Repeat(string(rune(n)), n)...)
		iv := make([]byte, aes.BlockSize)
		rand.Read(iv)
		blk, _ := aes.NewCipher(e)
		cipher.NewCBCEncrypter(blk, iv).CryptBlocks(data, data)
		h := hmac.New(sha256.New, m)
		h.Write(iv)
		h.Write(data)
		b := base64.StdEncoding.EncodeToString
		return "2." + b(iv) + "|" + b(data) + "|" + b(h.Sum(nil))
	}
	s := func(v string) string { return encrypt(raw[:32], raw[32:], []byte(v)) }
	username := s(user)
	if broken {
		username = "2.broken"
	}
	sync, _ := json.Marshal(map[string]any{
		"profile": map[string]any{"key": encrypt(enc, mac, raw)},
		"ciphers": []map[string]any{{"id": "c1", "name": s("Nagios"), "login": map[string]string{"username": username, "password": s("pw")}}},
	})
	srv := fake(t, map[string]string{
		"/identity/accounts/prelogin": `{"kdf":0,"kdfIterations":1}`,
		"/identity/connect/token":     `{"access_token":"T","expires_in":3600}`,
		"/api/sync":                   string(sync),
	})
	return orgs.Vault{Name: "bw", Kind: "vaultwarden", URL: srv.URL, Email: email, Password: orgs.Secret{Token: pw}}
}

// TestNagiosLoginVault: an item with no field and no user is the whole
// login: its username too.
func TestNagiosLoginVault(t *testing.T) {
	n := orgs.Nagios{Secret: orgs.Secret{Vault: "bw", Item: "Nagios"}}
	cfg := orgs.Config{Vaults: []orgs.Vault{fakeBitwarden(t, "nagiosadmin", false)}}
	if user, pass, err := NagiosLogin(ctx, cfg, n); err != nil || user != "nagiosadmin" || pass != "pw" {
		t.Errorf("login %q %q %v", user, pass, err)
	}
	n.User = "me"
	if user, _, _ := NagiosLogin(ctx, cfg, n); user != "me" {
		t.Errorf("desk.toml's user lost: %q", user)
	}
	n.User = ""
	cfg = orgs.Config{Vaults: []orgs.Vault{fakeBitwarden(t, "", true)}}
	if _, _, err := NagiosLogin(ctx, cfg, n); err == nil {
		t.Error("an unreadable username: no error")
	}
	if _, _, err := NagiosLogin(ctx, orgs.Config{}, orgs.Nagios{Secret: badSecret}); err == nil {
		t.Error("a failing secret: no error")
	}
}

func TestDigestAuthNoQop(t *testing.T) {
	h := func(s string) string { return fmt.Sprintf("%x", md5.Sum([]byte(s))) }
	want := h(h("me:r:pw") + ":n1:" + h("GET:/x"))
	got := DigestAuth(`Digest realm="r", nonce=n1`, "GET", "/x", "me", "pw", "c")
	if got != `Digest username="me", realm="r", nonce="n1", uri="/x", algorithm=MD5, response="`+want+`"` {
		t.Error(got)
	}
}

// TestAlertsErrors: each way a Nagios fails is its error, named by its
// host when it has no name; one without a URL is nothing.
func TestAlertsErrors(t *testing.T) {
	bearer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(401)
			return
		}
		fmt.Fprint(w, `{"data":{}}`)
	}))
	defer bearer.Close()
	if as, err := Alerts(ctx, orgs.Config{Nagios: []orgs.Nagios{{URL: bearer.URL, Secret: orgs.Secret{Token: "tok"}}, {}}}); err != nil || len(as) != 0 {
		t.Errorf("bearer, and one without URL: %v %v", as, err)
	}
	hijack := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Digest ") {
			w.Header().Set("WWW-Authenticate", `Digest realm="r", nonce="n"`)
			w.WriteHeader(401)
			return
		}
		c, _, _ := w.(http.Hijacker).Hijack()
		c.Close()
	}))
	defer hijack.Close()
	half := fake(t, map[string]string{"/cgi-bin/statusjson.cgi": "!404", "/cgi-bin/status.cgi": "!500"})
	svcFail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("query") == "servicelist" {
			w.WriteHeader(500)
			return
		}
		fmt.Fprint(w, `{"data":{}}`)
	}))
	defer svcFail.Close()
	for name, n := range map[string]orgs.Nagios{
		"bad URL":        {Name: "x", URL: "http://[::1"},
		"no server":      {Name: "x", URL: dead()},
		"digest dropped": {Name: "x", URL: hijack.URL, User: "me", Secret: orgs.Secret{Token: "pw"}},
		"nagios3 500":    {Name: "x", URL: half.URL},
		"servicelist":    {Name: "x", URL: svcFail.URL},
	} {
		if _, err := Alerts(ctx, orgs.Config{Nagios: []orgs.Nagios{n}}); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if _, err := Alerts(ctx, orgs.Config{Nagios: []orgs.Nagios{{URL: "nagios-box", Secret: badSecret}}}); err == nil || !strings.HasPrefix(err.Error(), "Nagios nagios-box: ") {
		t.Errorf("named by its URL: %v", err)
	}
}

var now3 = time.Date(2026, 10, 29, 21, 40, 0, 0, time.Local)

func TestParseStatus3Edges(t *testing.T) {
	if _, err := ParseStatus3(iotest.ErrReader(errors.New("boom")), now3); err == nil {
		t.Error("a read error: none")
	}
	page := `<table>
		<tr><td></td><td>CRITICAL</td><td>no host anywhere</td></tr>
		<tr><td><a href="extinfo.cgi?type=1&amp;host=web1">web1</a></td><td>DOWN</td><td><img src="x.png"></td><td>gone</td></tr>
	</table>`
	cs, err := ParseStatus3(strings.NewReader(page), now3)
	if err != nil || len(cs) != 1 || cs[0].Host != "web1" || cs[0].Status != 4 || cs[0].Acked || cs[0].Output != "gone" {
		t.Errorf("rows %+v %v", cs, err)
	}
	const web1 = `<td><a href="extinfo.cgi?type=1&amp;host=web1&amp;service=disk">web1</a></td>`
	for name, c := range map[string]struct {
		row string
		ok  func(Check3) bool
	}{
		"ack icon, then another": {`<tr>` + web1 + `<td>OK</td><td><img src="ack.gif"><img src="x.png"></td><td>out</td></tr>`, func(c Check3) bool { return c.Acked }},
		"a host link's service":  {`<tr>` + web1 + `<td>OK</td><td>out</td></tr>`, func(c Check3) bool { return c.Service == "" }},
		"a th is no cell":        {`<tr>` + web1 + `<th>OK</th><td>CRITICAL</td><td>out</td></tr>`, func(c Check3) bool { return c.Status == 16 }},
		"cells before the state": {`<tr>` + web1 + `<td>1/3</td><td>CRITICAL</td><td>0d 0h 1m 0s</td><td>out</td></tr>`, func(c Check3) bool { return c.Attempt == 0 && c.Output == "out" }},
	} {
		cs, err := ParseStatus3(strings.NewReader("<table>"+c.row+"</table>"), now3)
		if err != nil || len(cs) != 1 || !c.ok(cs[0]) {
			t.Errorf("%s: %+v %v", name, cs, err)
		}
	}
	if cs, _ := ParseStatus3(strings.NewReader(`<template>`+web1+`<td>OK</td></template>`), now3); len(cs) != 0 {
		t.Errorf("cells outside a tr: %+v", cs)
	}
}

func TestPushMore(t *testing.T) {
	if _, err := Push(ctx, kanban.Link{URL: "%zz", Kind: KindJira}, "Done", orgs.Config{}); err == nil {
		t.Error("a bad URL: no error")
	}
	if got, err := Push(ctx, kanban.Link{URL: "https://example.com", Kind: KindURL}, "Done", orgs.Config{}); got != "" || err != nil {
		t.Errorf("a plain URL: %q %v", got, err)
	}
	const issue = "/rest/api/3/issue/ABC-1"
	is := func(status, cat string) string {
		return `{"fields":{"status":{"name":"` + status + `","statusCategory":{"key":"` + cat + `"}},"assignee":{"accountId":"me-1"}}}`
	}
	me := `{"accountId":"me-1"}`
	toNew := `{"transitions":[{"id":"11","to":{"name":"To Do","statusCategory":{"key":"new"}}}]}`
	toDone := `{"transitions":[{"id":"41","to":{"name":"Done","statusCategory":{"key":"done"}}}]}`
	for name, c := range map[string]struct {
		routes  map[string]string
		column  string
		want    string
		wantErr bool
	}{
		"issue 404":        {map[string]string{}, "Done", "", true},
		"myself 404":       {map[string]string{issue: is("To Do", "new")}, "Done", "", true},
		"transitions 404":  {map[string]string{issue: is("To Do", "new"), "/rest/api/3/myself": me}, "Done", "", true},
		"blocked already":  {map[string]string{issue: is("Blocked", "indeterminate"), "/rest/api/3/myself": me, issue + "/transitions": toNew}, "Blocked", "", false},
		"no way to done":   {map[string]string{issue: is("To Do", "new"), "/rest/api/3/myself": me, issue + "/transitions": toNew}, "Done", "", true},
		"transition fails": {map[string]string{issue: is("To Do", "new"), "/rest/api/3/myself": me, issue + "/transitions": toDone, "POST " + issue + "/transitions": "!500"}, "Done", "", true},
		"transition works": {map[string]string{issue: is("To Do", "new"), "/rest/api/3/myself": me, issue + "/transitions": toDone, "POST " + issue + "/transitions": "!204"}, "Done", "Done", false},
	} {
		srv := fake(t, c.routes)
		cfg := orgs.Config{Jira: []orgs.Jira{{Projects: []string{"ABC"}, Secret: orgs.Secret{Token: "t"}}}} // no URL: the link's host
		got, err := Push(ctx, kanban.Link{URL: srv.URL + "/browse/ABC-1", Kind: KindJira, Ref: "ABC-1"}, c.column, cfg)
		if got != c.want || (err != nil) != c.wantErr {
			t.Errorf("%s: %q %v", name, got, err)
		}
	}
	bad := orgs.Config{Jira: []orgs.Jira{{Projects: []string{"ABC"}, Secret: badSecret}}}
	if _, err := Push(ctx, kanban.Link{URL: "https://acme.atlassian.net/browse/ABC-1", Kind: KindJira, Ref: "ABC-1"}, "Done", bad); err == nil {
		t.Error("jira: a failing secret: no error")
	}

	gi := "/repos/acme/api/issues/"
	srv := fake(t, map[string]string{
		gi + "2": `{"state":"open","assignees":[{"login":"me"}]}`, // /user 404
	})
	useGitHub(t, srv.URL)
	cfg := orgs.Config{GitHub: []orgs.GitHub{{Secret: orgs.Secret{Token: "t"}}}}
	link := func(n string) kanban.Link {
		return kanban.Link{URL: "https://github.com/acme/api/issues/" + n, Kind: KindGitHubIssue}
	}
	for _, n := range []string{"1", "2"} {
		if _, err := Push(ctx, link(n), "Done", cfg); err == nil {
			t.Errorf("issue %s: no error", n)
		}
	}
	srv2 := fake(t, map[string]string{
		gi + "3":            `{"state":"open","assignees":[{"login":"me"}]}`,
		"PATCH " + gi + "3": "!500",
		"/user":             `{"login":"me"}`,
	})
	useGitHub(t, srv2.URL)
	if _, err := Push(ctx, link("3"), "Done", cfg); err == nil {
		t.Error("a failing PATCH: no error")
	}
	if _, err := Push(ctx, link("3"), "Done", orgs.Config{GitHub: []orgs.GitHub{{Secret: badSecret}}}); err == nil {
		t.Error("github: a failing secret: no error")
	}
	if got, err := Push(ctx, link("1"), "Done", orgs.Config{GitHub: []orgs.GitHub{{Host: "ghe.example.com", Secret: orgs.Secret{Token: "t"}}}}); got != "" || err != nil {
		t.Errorf("another host's account wrote: %q %v", got, err)
	}
}

func TestAssignedMore(t *testing.T) {
	gl := fake(t, map[string]string{
		"/api/v4/issues": `[{"iid":1,"title":"Ours","references":{"full":"globex/ops#1"}},{"iid":2,"title":"Theirs","references":{"full":"initech/ops#2"}}]`,
	})
	ghe := fakeTLS(t, map[string]string{
		"/api/v3/issues": `[{"number":3,"title":"Docs","repository":{"full_name":"acme/api","owner":{"login":"acme"}}}]`,
	})
	host := strings.TrimPrefix(ghe.URL, "https://")
	tok := orgs.Secret{Token: "t"}
	cfg := orgs.Config{
		Jira:   []orgs.Jira{{URL: "https://acme.atlassian.net"}, {Secret: tok}}, // no token, no URL: skipped
		GitHub: []orgs.GitHub{{Host: host}, {Host: host, Secret: tok}},          // the first has no token
		GitLab: []orgs.GitLab{{URL: gl.URL}, {URL: gl.URL, Groups: []string{"globex"}, Secret: tok}},
	}
	ts, err := Assigned(ctx, cfg)
	var got []string
	for _, x := range ts {
		got = append(got, x.Project+" "+x.Key)
	}
	if err != nil || strings.Join(got, ",") != "acme/api #3,globex/ops #1" {
		t.Errorf("%q %v", got, err)
	}
	failing := fake(t, nil)
	bad := orgs.Config{
		Jira:   []orgs.Jira{{URL: "https://acme.atlassian.net", Secret: badSecret}, {URL: failing.URL, Secret: tok}},
		GitHub: []orgs.GitHub{{Secret: badSecret}, {Host: host + "x", Secret: tok}},
		GitLab: []orgs.GitLab{{Secret: badSecret}, {URL: failing.URL, Secret: tok}},
	}
	if _, err := Assigned(ctx, bad); err == nil || strings.Count(err.Error(), "\n") != 5 {
		t.Errorf("want six errors: %v", err)
	}
}
