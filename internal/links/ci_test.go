package links

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lfdominguez/desk/internal/kanban"
	"github.com/lfdominguez/desk/internal/orgs"
)

// TestCI: a PR's Actions runs by its head commit, with jobs, the chip says
// its CI; a GitLab MR's latest pipeline and its jobs; job logs; commit links.
func TestCI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.EscapedPath() {
		case "/repos/acme/api/pulls/9":
			fmt.Fprint(w, `{"state":"open","head":{"sha":"abc123"}}`)
		case "/repos/acme/api/pulls/9/reviews":
			fmt.Fprint(w, `[{"user":{"login":"ana"},"state":"APPROVED"}]`)
		case "/repos/acme/api/actions/runs":
			if r.URL.Query().Get("head_sha") != "abc123" {
				http.Error(w, "wrong sha", 400)
				return
			}
			fmt.Fprint(w, `{"workflow_runs":[{"id":5,"name":"test","status":"completed","conclusion":"failure","html_url":"https://github.com/acme/api/actions/runs/5"}]}`)
		case "/repos/acme/api/actions/runs/5/jobs":
			fmt.Fprint(w, `{"jobs":[{"id":51,"name":"lint","status":"completed","conclusion":"success"},{"id":52,"name":"unit","status":"completed","conclusion":"failure"}]}`)
		case "/repos/acme/api/actions/jobs/52/logs":
			fmt.Fprint(w, "setup\nFAIL TestLogin\n")
		case "/api/v4/projects/grp%2Fapp/merge_requests/3/pipelines":
			fmt.Fprint(w, `[{"id":70,"status":"running","web_url":"x"}]`)
		case "/api/v4/projects/grp%2Fapp/pipelines/70/jobs":
			fmt.Fprint(w, `[{"id":702,"name":"deploy","stage":"deploy","status":"created"},{"id":701,"name":"build","stage":"build","status":"success"}]`)
		case "/api/v4/projects/grp%2Fapp/pipelines":
			if r.URL.Query().Get("sha") != "0123abc" {
				http.Error(w, "wrong sha", 400)
				return
			}
			fmt.Fprint(w, `[]`)
		case "/api/v4/projects/grp%2Fapp/jobs/702/trace":
			fmt.Fprint(w, "waiting")
		default:
			http.Error(w, "unexpected "+r.URL.String(), 404)
		}
	}))
	defer srv.Close()
	githubAPI = srv.URL
	cfg := orgs.Config{GitHub: []orgs.GitHub{{Secret: tokT}}, GitLab: []orgs.GitLab{{URL: srv.URL, Secret: tokT}}}
	ctx := context.Background()

	pr, _ := Parse("https://github.com/acme/api/pull/9", cfg)
	runs, err := CI(ctx, pr, cfg)
	if err != nil || len(runs) != 1 || runs[0].Status != CIFailed || len(runs[0].Jobs) != 2 || runs[0].Jobs[1].Status != CIFailed {
		t.Fatalf("github runs %+v %v", runs, err)
	}
	if state, tone, err := Check(ctx, pr, cfg); state != "approved — merge · CI failed" || tone != ToneBad || err != nil {
		t.Errorf("chip %q %q %v", state, tone, err)
	}
	if log, err := JobLog(ctx, pr, cfg, 52, 10); err != nil || log != "…TestLogin\n" {
		t.Errorf("log %q %v", log, err)
	}

	mr := kanban.Link{URL: srv.URL + "/grp/app/-/merge_requests/3", Kind: KindGitLabMR}
	runs, err = CI(ctx, mr, cfg)
	if err != nil || len(runs) != 1 || runs[0].Status != CIRunning || runs[0].Jobs[0].Name != "build" || runs[0].Jobs[1].Status != CIPending {
		t.Fatalf("gitlab runs %+v %v", runs, err)
	}
	if log, err := JobLog(ctx, mr, cfg, 702, 100); err != nil || log != "waiting" {
		t.Errorf("trace %q %v", log, err)
	}
	if _, err := CI(ctx, kanban.Link{URL: srv.URL + "/grp/app/-/commit/0123abc", Kind: KindGitLabCommit}, cfg); err != nil {
		t.Errorf("a GitLab commit's pipelines by its sha: %v", err)
	}

	for in, want := range map[string][2]string{
		"https://github.com/acme/api/commit/abc123def456":      {KindGitHubCommit, "commit abc123d"},
		"https://gitlab.com/grp/app/-/commit/0123456789abcdef": {KindGitLabCommit, "commit 0123456"},
		"https://gitlab.com/grp/app/-/merge_requests/3/diffs":  {KindGitLabMR, "MR !3"},
	} {
		if l, ok := Parse(in, cfg); !ok || l.Kind != want[0] || l.Ref != want[1] {
			t.Errorf("%s: %+v", in, l)
		}
	}
}
