package links

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/lfdominguez/desk/internal/kanban"
	"github.com/lfdominguez/desk/internal/orgs"
	"github.com/lfdominguez/desk/internal/vault"
)

var client = &http.Client{Timeout: 20 * time.Second}

// githubAPI is github.com's REST API (Enterprise hosts use host/api/v3);
// tests point it at a fake.
var githubAPI = "https://api.github.com"

// bitbucketAPI is Bitbucket Cloud's API; tests point it at a fake.
var bitbucketAPI = "https://api.bitbucket.org/2.0"

// getJSON GETs u with auth applied and decodes the reply into v.
func getJSON(ctx context.Context, u string, auth func(*http.Request), v any) error {
	return doJSON(ctx, "GET", u, auth, nil, v)
}

// doJSON sends method to u with auth applied, body (if any) as JSON, and
// decodes the reply into v (if any); a 2xx is success, 204 has no reply.
func doJSON(ctx context.Context, method, u string, auth func(*http.Request), body, v any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	auth(req)
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 300))
		return fmt.Errorf("%s: %s %s", req.URL.Host, res.Status, strings.TrimSpace(string(b)))
	}
	if v == nil || res.StatusCode == http.StatusNoContent {
		return nil
	}
	return json.NewDecoder(res.Body).Decode(v)
}

// secret reads s, from the org's vault when it names one.
func secret(ctx context.Context, cfg orgs.Config, s orgs.Secret) (string, error) {
	return vault.Resolve(ctx, cfg.Vaults, s)
}

// auth is basic auth when there is a user, else a bearer token; none
// without a token.
func auth(ctx context.Context, cfg orgs.Config, user string, s orgs.Secret) (func(*http.Request), error) {
	tok, err := secret(ctx, cfg, s)
	if err != nil {
		return nil, err
	}
	return func(r *http.Request) {
		switch {
		case tok == "":
		case user != "":
			r.SetBasicAuth(user, tok)
		default:
			r.Header.Set("Authorization", "Bearer "+tok)
		}
	}, nil
}

// NoAccountError: the organization has no account, with a token, for a
// link's provider and owner, so desk doesn't ask the provider at all:
// anonymous calls see no private repos and run out fast (GitHub allows 60 an
// hour per address).
type NoAccountError struct{ Provider, Owner string }

func (e *NoAccountError) Error() string {
	return fmt.Sprintf("no %s account for %s in this organization's desk.toml", e.Provider, e.Owner)
}

// tokenAuth is auth for an account that must have a token: none (no
// account, or its token resolves empty) is a NoAccountError.
func tokenAuth(ctx context.Context, cfg orgs.Config, user string, s orgs.Secret, provider, owner string) (func(*http.Request), error) {
	tok, err := secret(ctx, cfg, s)
	if err != nil {
		return nil, err
	}
	if tok == "" {
		return nil, &NoAccountError{provider, owner}
	}
	return auth(ctx, cfg, user, orgs.Secret{Token: tok})
}

// githubRepo is the API URL of u's repo (owner p[0], repo p[1]) and the
// auth of the account that answers for it.
func githubRepo(ctx context.Context, u *url.URL, p []string, cfg orgs.Config) (string, func(*http.Request), error) {
	var s orgs.Secret
	for _, g := range cfg.GitHub {
		if (g.Host == "" || g.Host == u.Hostname()) && (len(g.Owners) == 0 || slices.Contains(g.Owners, p[0])) {
			s = g.Secret
			break
		}
	}
	a, err := tokenAuth(ctx, cfg, "", s, "GitHub", p[0])
	if err != nil {
		return "", nil, err
	}
	api := githubAPI
	if h := u.Hostname(); h != "github.com" {
		api = "https://" + h + "/api/v3"
	}
	return api + "/repos/" + p[0] + "/" + p[1], a, nil
}

func checkGitHub(ctx context.Context, l kanban.Link, u *url.URL, p []string, cfg orgs.Config) (string, string, error) {
	repo, a, err := githubRepo(ctx, u, p, cfg)
	if err != nil {
		return "", "", err
	}
	if l.Kind == KindGitHubIssue {
		var v struct{ State string }
		if err := getJSON(ctx, repo+"/issues/"+p[3], a, &v); err != nil {
			return "", "", err
		}
		if v.State == "closed" {
			return "closed", ToneDone, nil
		}
		return "open", ToneWait, nil
	}
	var v struct {
		State         string
		Merged, Draft bool
	}
	if err := getJSON(ctx, repo+"/pulls/"+p[3], a, &v); err != nil {
		return "", "", err
	}
	switch {
	case v.Merged:
		return "merged", ToneDone, nil
	case v.State == "closed":
		return "closed", ToneBad, nil
	case v.Draft:
		return "draft", ToneWait, nil
	}
	// ponytail: the decision is each reviewer's latest verdict, not the branch rules' required
	// reviewers (GraphQL reviewDecision has those, but needs a token even for public repos).
	var reviews []struct {
		User  struct{ Login string }
		State string
	}
	if err := getJSON(ctx, repo+"/pulls/"+p[3]+"/reviews?per_page=100", a, &reviews); err != nil {
		return "", "", err
	}
	latest := map[string]string{}
	for _, r := range reviews {
		if r.State != "COMMENTED" && r.State != "PENDING" {
			latest[r.User.Login] = r.State
		}
	}
	verdicts := slices.Collect(maps.Values(latest))
	switch {
	case slices.Contains(verdicts, "CHANGES_REQUESTED"):
		return "changes requested", ToneBad, nil
	case slices.Contains(verdicts, "APPROVED"):
		return "approved — merge", ToneGood, nil
	}
	return "waiting", ToneWait, nil
}

func checkJira(ctx context.Context, u *url.URL, key string, j orgs.Jira, cfg orgs.Config) (string, string, error) {
	a, err := auth(ctx, cfg, j.Email, j.Secret)
	if err != nil {
		return "", "", err
	}
	base := strings.TrimRight(j.URL, "/")
	if base == "" {
		base = u.Scheme + "://" + u.Host
	}
	var v struct {
		Fields struct {
			Status struct {
				Name     string
				Category struct{ Key string } `json:"statusCategory"`
			}
		}
	}
	if err := getJSON(ctx, base+"/rest/api/3/issue/"+url.PathEscape(key)+"?fields=status", a, &v); err != nil {
		return "", "", err
	}
	tone := ToneWait
	if v.Fields.Status.Category.Key == "done" {
		tone = ToneDone
	}
	return v.Fields.Status.Name, tone, nil
}

func checkBitbucket(ctx context.Context, p []string, cfg orgs.Config) (string, string, error) {
	var b orgs.Bitbucket
	for _, c := range cfg.Bitbucket {
		if c.Workspace == "" || c.Workspace == p[0] {
			b = c
			break
		}
	}
	a, err := tokenAuth(ctx, cfg, b.User, b.Secret, "Bitbucket", p[0])
	if err != nil {
		return "", "", err
	}
	type participant struct {
		Approved bool
		State    string
	}
	var v struct {
		State        string
		Participants []participant
	}
	if err := getJSON(ctx, fmt.Sprintf("%s/repositories/%s/%s/pullrequests/%s", bitbucketAPI, p[0], p[1], p[3]), a, &v); err != nil {
		return "", "", err
	}
	switch {
	case v.State == "MERGED":
		return "merged", ToneDone, nil
	case v.State == "DECLINED" || v.State == "SUPERSEDED":
		return strings.ToLower(v.State), ToneBad, nil
	case slices.ContainsFunc(v.Participants, func(x participant) bool { return x.State == "changes_requested" }):
		return "changes requested", ToneBad, nil
	case slices.ContainsFunc(v.Participants, func(x participant) bool { return x.Approved }):
		return "approved — merge", ToneGood, nil
	}
	return "waiting", ToneWait, nil
}

func checkGitLab(ctx context.Context, u *url.URL, p []string, cfg orgs.Config) (string, string, error) {
	project, a, err := gitlabProject(ctx, u, p, cfg)
	if err != nil {
		return "", "", err
	}
	base := project + "/merge_requests/" + p[slices.Index(p, "merge_requests")+1]
	var v struct {
		State string
		Draft bool
	}
	if err := getJSON(ctx, base, a, &v); err != nil {
		return "", "", err
	}
	switch {
	case v.State == "merged":
		return "merged", ToneDone, nil
	case v.State == "closed":
		return "closed", ToneBad, nil
	case v.Draft:
		return "draft", ToneWait, nil
	}
	var ap struct{ Approved bool }
	if getJSON(ctx, base+"/approvals", a, &ap) == nil && ap.Approved {
		return "approved — merge", ToneGood, nil
	}
	return "waiting", ToneWait, nil
}
