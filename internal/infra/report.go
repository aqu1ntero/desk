package infra

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Incident reports: a tab exported as markdown (the conversation, then every
// command run in its org since the tab opened, from the audit log), saved in
// $XDG_STATE_HOME/desk/infra/reports/. "Draft a postmortem" asks the tab's Claude for
// one first, so the export carries it.

const postmortemPrompt = "(desk) Write a postmortem of this session for the team, in markdown: summary, impact, timeline " +
	"(times and what happened), root cause, what fixed it, and follow-up actions with owners left blank. Use only what " +
	"happened in this session; mark anything uncertain."

// auditSince reads org's audit entries from since on.
func auditSince(org string, since time.Time) []auditEntry {
	var out []auditEntry
	for d := time.Date(since.Year(), since.Month(), since.Day(), 0, 0, 0, 0, since.Location()); !d.After(time.Now()); d = d.AddDate(0, 0, 1) {
		f, err := os.Open(filepath.Join(auditDir(), orgDirName(org), d.Format("2006-01-02")+".jsonl"))
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			var e auditEntry
			if json.Unmarshal(sc.Bytes(), &e) == nil && !e.Time.Before(since) {
				out = append(out, e)
			}
		}
		f.Close()
	}
	return out
}

// incidentReport renders t as markdown.
func incidentReport(t *Tab, now time.Time) string {
	var b strings.Builder
	title := t.org + " · " + t.name
	if t.orgOnly {
		title = t.org
	}
	fmt.Fprintf(&b, "# Session report: %s\n\n%s → %s\n\n## Conversation\n\n", title, t.opened.Format("2006-01-02 15:04"), now.Format("15:04"))
	for _, e := range t.chat {
		switch e.who {
		case "you":
			fmt.Fprintf(&b, "**You:** %s\n\n", e.text)
		case "claude":
			fmt.Fprintf(&b, "**Claude:**\n\n%s\n\n", e.text)
		case "tool":
			fmt.Fprintf(&b, "- ▸ `%s`\n\n", e.text)
		case "error":
			fmt.Fprintf(&b, "> ✗ %s\n\n", e.text)
		default:
			fmt.Fprintf(&b, "_%s_\n\n", e.text)
		}
	}
	b.WriteString("## Commands and approvals\n\n")
	entries := auditSince(t.org, t.opened)
	if len(entries) == 0 {
		b.WriteString("_none recorded_\n")
	}
	for _, e := range entries {
		at := e.Time.Format("15:04:05")
		if e.Event == "approval" {
			fmt.Fprintf(&b, "- %s · %s · approval **%s**: `%s`\n", at, e.Host, e.Verdict, e.Cmd)
			continue
		}
		exit := 0
		if e.Exit != nil {
			exit = *e.Exit
		}
		fmt.Fprintf(&b, "- %s · %s · %s · exit %d · %s: `%s`\n", at, e.Host, e.By, exit, time.Duration(e.DurMS)*time.Millisecond, e.Cmd)
		if e.Output != "" && exit != 0 {
			fmt.Fprintf(&b, "\n  ```\n  %s\n  ```\n", strings.ReplaceAll(tailLines(e.Output, 8, 1500), "\n", "\n  "))
		}
	}
	return b.String()
}

// saveReport writes t's report and returns its path.
func saveReport(t *Tab) (string, error) {
	now := time.Now()
	dir := filepath.Join(stateDir(), "reports")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	p := filepath.Join(dir, orgDirName(t.org)+"-"+now.Format("2006-01-02-1504")+".md")
	return p, os.WriteFile(p, []byte(redact(incidentReport(t, now))), 0o600)
}
