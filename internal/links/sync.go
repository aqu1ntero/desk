package links

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/lfdominguez/desk/internal/kanban"
	"github.com/lfdominguez/desk/internal/orgs"
)

// Push moves ticket l to where a card in desk's column (its title) is:
// a Jira issue takes the transition to that column's status category
// (Blocked: a status named so, if its workflow has one), a GitHub issue
// closes in Done and reopens out of it. It says what the ticket is now,
// or "" when there was nothing to do: in step already, not a ticket, or an
// account that opts out (sync = false) or can't be told apart.
func Push(ctx context.Context, l kanban.Link, column string, cfg orgs.Config) (string, error) {
	u, err := url.Parse(l.URL)
	if err != nil {
		return "", err
	}
	switch l.Kind {
	case KindJira:
		j, ok := jiraFor(cfg, u.Hostname(), l.Ref)
		if !ok || off(j.Sync) {
			return "", nil
		}
		return pushJira(ctx, u, l.Ref, column, j, cfg)
	case KindGitHubIssue:
		p := strings.Split(strings.Trim(u.Path, "/"), "/")
		i := slices.IndexFunc(cfg.GitHub, func(g orgs.GitHub) bool {
			return (g.Host == "" || g.Host == u.Hostname()) && (len(g.Owners) == 0 || slices.Contains(g.Owners, p[0]))
		})
		if len(p) < 4 || i < 0 || off(cfg.GitHub[i].Sync) { // no account of the user's: nothing to write with
			return "", nil
		}
		return pushGitHub(ctx, u, p, column, cfg)
	}
	return "", nil
}

func off(b *bool) bool { return b != nil && !*b }

// blocked tells a Blocked status by its name (English or Spanish).
func blocked(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "block") || strings.Contains(n, "bloque")
}

// category is the Jira status category a desk column stands for.
func category(column string) string {
	switch column {
	case "Backlog", "To do":
		return "new"
	case "Done":
		return "done"
	}
	return "indeterminate" // In progress, Blocked
}

type jiraStatus struct {
	Name     string `json:"name"`
	Category struct {
		Key string `json:"key"`
	} `json:"statusCategory"`
}

type jiraTransition struct {
	ID string     `json:"id"`
	To jiraStatus `json:"to"`
}

func pushJira(ctx context.Context, u *url.URL, key, column string, j orgs.Jira, cfg orgs.Config) (string, error) {
	a, err := auth(ctx, cfg, j.Email, j.Secret)
	if err != nil {
		return "", err
	}
	base := strings.TrimRight(j.URL, "/")
	if base == "" {
		base = u.Scheme + "://" + u.Host
	}
	issue := base + "/rest/api/3/issue/" + url.PathEscape(key)
	var now struct {
		Fields struct {
			Status   jiraStatus `json:"status"`
			Assignee *struct {
				AccountID string `json:"accountId"`
			} `json:"assignee"`
		} `json:"fields"`
	}
	if err := getJSON(ctx, issue+"?fields=status,assignee", a, &now); err != nil {
		return "", err
	}
	if now.Fields.Assignee == nil || slices.ContainsFunc(j.Ignore, func(s string) bool { return strings.EqualFold(s, now.Fields.Status.Name) }) {
		return "", nil // not the user's, or in someone else's hands now
	}
	me, err := jiraMe(ctx, base, a)
	if err != nil {
		return "", err
	}
	if now.Fields.Assignee.AccountID != me {
		return "", nil
	}
	var ts struct {
		Transitions []jiraTransition `json:"transitions"`
	}
	if err := getJSON(ctx, issue+"/transitions", a, &ts); err != nil {
		return "", err
	}
	cur, want := now.Fields.Status, category(column)
	pick := -1
	if name := j.Status[column]; name != "" { // the account says which status, by name
		if strings.EqualFold(cur.Name, name) {
			return "", nil
		}
		pick = slices.IndexFunc(ts.Transitions, func(t jiraTransition) bool { return strings.EqualFold(t.To.Name, name) })
		if pick < 0 {
			return "", fmt.Errorf("%s: no transition from %s to %s", key, cur.Name, name)
		}
	} else if column == "Blocked" { // its own status when the workflow has one
		if blocked(cur.Name) {
			return "", nil
		}
		pick = slices.IndexFunc(ts.Transitions, func(t jiraTransition) bool { return blocked(t.To.Name) })
	}
	if pick < 0 {
		if cur.Category.Key == want && !(column != "Blocked" && blocked(cur.Name)) {
			return "", nil
		}
		for i, t := range ts.Transitions {
			if t.To.Category.Key == want && !blocked(t.To.Name) {
				pick = i
				break
			}
		}
	}
	if pick < 0 {
		return "", fmt.Errorf("%s: no transition from %s to a %s status", key, cur.Name, want)
	}
	t := ts.Transitions[pick]
	if err := doJSON(ctx, "POST", issue+"/transitions", a, map[string]any{"transition": map[string]string{"id": t.ID}}, nil); err != nil {
		return "", err
	}
	return t.To.Name, nil
}

func pushGitHub(ctx context.Context, u *url.URL, p []string, column string, cfg orgs.Config) (string, error) {
	repo, a, err := githubRepo(ctx, u, p, cfg)
	if err != nil {
		return "", err
	}
	type user struct {
		Login string `json:"login"`
	}
	var is struct {
		State     string `json:"state"`
		Assignees []user `json:"assignees"`
	}
	issue := repo + "/issues/" + p[3]
	if err := getJSON(ctx, issue, a, &is); err != nil {
		return "", err
	}
	if len(is.Assignees) == 0 {
		return "", nil // nobody's: not the user's to move
	}
	var me user
	api, _, _ := strings.Cut(repo, "/repos/")
	if err := getJSON(ctx, api+"/user", a, &me); err != nil {
		return "", err
	}
	if !slices.ContainsFunc(is.Assignees, func(x user) bool { return strings.EqualFold(x.Login, me.Login) }) {
		return "", nil // someone else's
	}
	want := "open"
	if column == "Done" {
		want = "closed"
	}
	if is.State == want {
		return "", nil
	}
	body := map[string]string{"state": want}
	if want == "closed" {
		body["state_reason"] = "completed"
	}
	if err := doJSON(ctx, "PATCH", issue, a, body, nil); err != nil {
		return "", err
	}
	return map[string]string{"open": "reopened", "closed": "closed"}[want], nil
}
