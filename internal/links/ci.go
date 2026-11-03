package links

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/lfdominguez/desk/internal/kanban"
	"github.com/lfdominguez/desk/internal/orgs"
)

// CI: what a pull or merge request's or a commit's continuous integration
// says. GitHub Actions' workflow runs and their jobs (by the head commit),
// GitLab's pipelines and their jobs.

// Job statuses, the same for both providers.
const (
	CISuccess  = "success"
	CIFailed   = "failed"
	CIRunning  = "running"
	CIPending  = "pending"
	CICanceled = "canceled"
	CISkipped  = "skipped"
)

type Job struct {
	ID                int64
	Name, Status, URL string
	Stage             string // GitLab's
}

type Run struct {
	ID                int64
	Name, Status, URL string
	Jobs              []Job
}

// HasCI is whether l is a kind CI runs for.
func HasCI(l kanban.Link) bool {
	return l.Kind == KindGitHubPR || l.Kind == KindGitHubCommit || l.Kind == KindGitLabMR || l.Kind == KindGitLabCommit
}

// CI is l's latest runs, with their jobs.
func CI(ctx context.Context, l kanban.Link, cfg orgs.Config) ([]Run, error) {
	u, p, err := split(l)
	if err != nil {
		return nil, err
	}
	switch l.Kind {
	case KindGitHubPR, KindGitHubCommit:
		return githubCI(ctx, l, u, p, cfg)
	case KindGitLabMR, KindGitLabCommit:
		return gitlabCI(ctx, l, u, p, cfg)
	}
	return nil, fmt.Errorf("%s has no CI desk reads", l.Ref)
}

// JobLog is the end of job id's log (l's provider), at most max bytes.
func JobLog(ctx context.Context, l kanban.Link, cfg orgs.Config, id int64, max int) (string, error) {
	u, p, err := split(l)
	if err != nil {
		return "", err
	}
	var at string
	var a func(*http.Request)
	switch l.Kind {
	case KindGitHubPR, KindGitHubCommit:
		var repo string
		if repo, a, err = githubRepo(ctx, u, p, cfg); err == nil {
			at = fmt.Sprintf("%s/actions/jobs/%d/logs", repo, id)
		}
	case KindGitLabMR, KindGitLabCommit:
		var project string
		if project, a, err = gitlabProject(ctx, u, p, cfg); err == nil {
			at = fmt.Sprintf("%s/jobs/%d/trace", project, id)
		}
	default:
		err = fmt.Errorf("%s has no CI desk reads", l.Ref)
	}
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", at, nil)
	if err != nil {
		return "", err
	}
	a(req) // GitHub redirects to a signed URL; Go drops the token for the other host
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode/100 != 2 {
		return "", fmt.Errorf("%s: %s", req.URL.Host, res.Status)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, 64<<20))
	if len(b) > max {
		b = append([]byte("…"), b[len(b)-max:]...)
	}
	return string(b), err
}

// Summary is runs' worst status, for a link's chip: "" without runs.
func Summary(runs []Run) (state, tone string) {
	st := make([]string, len(runs))
	for i, r := range runs {
		st[i] = r.Status
	}
	switch {
	case len(st) == 0:
		return "", ""
	case slices.Contains(st, CIFailed):
		return "CI failed", ToneBad
	case slices.Contains(st, CIRunning) || slices.Contains(st, CIPending):
		return "CI running", ToneWait
	}
	return "CI passed", ToneGood
}

func split(l kanban.Link) (*url.URL, []string, error) {
	u, err := url.Parse(l.URL)
	if err != nil {
		return nil, nil, err
	}
	return u, strings.Split(strings.Trim(u.Path, "/"), "/"), nil
}

func githubCI(ctx context.Context, l kanban.Link, u *url.URL, p []string, cfg orgs.Config) ([]Run, error) {
	repo, a, err := githubRepo(ctx, u, p, cfg)
	if err != nil {
		return nil, err
	}
	sha := p[3]
	if l.Kind == KindGitHubPR {
		var v struct{ Head struct{ SHA string } }
		if err := getJSON(ctx, repo+"/pulls/"+p[3], a, &v); err != nil {
			return nil, err
		}
		sha = v.Head.SHA
	}
	var v struct {
		Runs []struct {
			ID                       int64
			Name, Status, Conclusion string
			URL                      string `json:"html_url"`
		} `json:"workflow_runs"`
	}
	if err := getJSON(ctx, repo+"/actions/runs?per_page=20&head_sha="+sha, a, &v); err != nil {
		return nil, err
	}
	var runs []Run
	for _, r := range v.Runs {
		var jv struct {
			Jobs []struct {
				ID                       int64
				Name, Status, Conclusion string
				URL                      string `json:"html_url"`
			}
		}
		if err := getJSON(ctx, fmt.Sprintf("%s/actions/runs/%d/jobs?per_page=100", repo, r.ID), a, &jv); err != nil {
			return nil, err
		}
		run := Run{ID: r.ID, Name: r.Name, Status: githubStatus(r.Status, r.Conclusion), URL: r.URL}
		for _, j := range jv.Jobs {
			run.Jobs = append(run.Jobs, Job{ID: j.ID, Name: j.Name, Status: githubStatus(j.Status, j.Conclusion), URL: j.URL})
		}
		runs = append(runs, run)
	}
	return runs, nil
}

func githubStatus(status, conclusion string) string {
	switch status {
	case "in_progress":
		return CIRunning
	case "completed":
	default:
		return CIPending // queued, waiting, requested, pending
	}
	switch conclusion {
	case "success", "neutral":
		return CISuccess
	case "cancelled":
		return CICanceled
	case "skipped", "stale":
		return CISkipped
	case "action_required":
		return CIPending
	}
	return CIFailed // failure, timed_out, startup_failure
}

func gitlabCI(ctx context.Context, l kanban.Link, u *url.URL, p []string, cfg orgs.Config) ([]Run, error) {
	project, a, err := gitlabProject(ctx, u, p, cfg)
	if err != nil {
		return nil, err
	}
	var at string
	if l.Kind == KindGitLabMR {
		at = project + "/merge_requests/" + p[slices.Index(p, "merge_requests")+1] + "/pipelines?per_page=1"
	} else {
		at = project + "/pipelines?per_page=1&sha=" + p[slices.Index(p, "commit")+1]
	}
	var ps []struct {
		ID     int64
		Status string
		Ref    string
		URL    string `json:"web_url"`
	}
	if err := getJSON(ctx, at, a, &ps); err != nil {
		return nil, err
	}
	var runs []Run
	for _, pl := range ps { // the latest: older ones ran on older commits
		var js []struct {
			ID                  int64
			Name, Stage, Status string
			URL                 string `json:"web_url"`
		}
		if err := getJSON(ctx, fmt.Sprintf("%s/pipelines/%d/jobs?per_page=100&include_retried=false", project, pl.ID), a, &js); err != nil {
			return nil, err
		}
		run := Run{ID: pl.ID, Name: fmt.Sprintf("pipeline #%d", pl.ID), Status: gitlabStatus(pl.Status), URL: pl.URL}
		for _, j := range js {
			run.Jobs = append(run.Jobs, Job{ID: j.ID, Name: j.Name, Stage: j.Stage, Status: gitlabStatus(j.Status), URL: j.URL})
		}
		slices.Reverse(run.Jobs) // GitLab lists them newest first: back to stage order
		runs = append(runs, run)
	}
	return runs, nil
}

func gitlabStatus(s string) string {
	switch s {
	case "success":
		return CISuccess
	case "failed":
		return CIFailed
	case "running":
		return CIRunning
	case "canceled", "canceling":
		return CICanceled
	case "skipped", "manual":
		return CISkipped
	}
	return CIPending // created, waiting_for_resource, preparing, pending, scheduled
}

// gitlabProject is the API URL of u's project (the path before "-") and
// the auth of the account that answers for it.
func gitlabProject(ctx context.Context, u *url.URL, p []string, cfg orgs.Config) (string, func(*http.Request), error) {
	var g orgs.GitLab
	for _, c := range cfg.GitLab {
		cu, _ := url.Parse(c.URL)
		if (c.URL == "" && u.Hostname() == "gitlab.com") || (cu != nil && cu.Hostname() == u.Hostname()) {
			if len(c.Groups) == 0 || slices.Contains(c.Groups, p[0]) {
				g = c
				break
			}
		}
	}
	tok, err := secret(ctx, cfg, g.Secret)
	if err != nil {
		return "", nil, err
	}
	if tok == "" {
		return "", nil, &NoAccountError{"GitLab", p[0]}
	}
	a := func(r *http.Request) { r.Header.Set("PRIVATE-TOKEN", tok) }
	dash := slices.Index(p, "-")
	if dash < 1 {
		return "", nil, fmt.Errorf("%s is not a GitLab project URL", u)
	}
	return fmt.Sprintf("%s://%s/api/v4/projects/%s", u.Scheme, u.Host, url.PathEscape(strings.Join(p[:dash], "/"))), a, nil
}
