package company

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestLog: entries append to the organization's log, read back oldest
// first, timed and with the verdict read off the text; a line that isn't
// JSON is skipped.
func TestLog(t *testing.T) {
	org := t.TempDir()
	if Read(org) != nil {
		t.Fatal("no log yet: nothing")
	}
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	Log(org, Entry{Kind: "brief", From: "ABC-1", To: "desk-qa", Text: "check it", At: at})
	Log(org, Entry{Kind: "result", From: "desk-qa", To: "ABC-1", Text: "all good\nVERDICT: BLOCKED tests fail"})
	f, _ := os.OpenFile(filepath.Join(Dir(org), "log.jsonl"), os.O_APPEND|os.O_WRONLY, 0)
	f.WriteString("not json\n")
	f.Close()
	es := Read(org)
	if len(es) != 2 || !es[0].At.Equal(at) || es[0].Verdict != "" || es[1].At.IsZero() || es[1].Verdict != "BLOCKED" {
		t.Fatalf("%+v", es)
	}
	os.RemoveAll(Dir(org))
	os.WriteFile(Dir(org), nil, 0o644) // a file where the folder goes
	if Log(org, Entry{}) == nil {
		t.Error("an unwritable log: an error")
	}
	os.Remove(Dir(org))
	os.MkdirAll(filepath.Join(Dir(org), "log.jsonl"), 0o755) // a folder where the log goes
	if Log(org, Entry{}) == nil {
		t.Error("a log that can't open: an error")
	}
}

// TestVerdict: the last line's VERDICT word, or none.
func TestVerdict(t *testing.T) {
	for text, want := range map[string]string{
		"done\nVERDICT: READY\n":            "READY",
		"VERDICT: PARTIAL no tests":         "PARTIAL",
		"VERDICT: READY\nthen more":         "",
		"":                                  "",
		"  VERDICT:   BLOCKED   no access ": "BLOCKED",
	} {
		if got := Verdict(text); got != want {
			t.Errorf("%q: %q, want %q", text, got, want)
		}
	}
}

// TestLeads: departments by name, the lead's prompt, a brief laid out with
// only what it has, and the leads' sessions kept per organization.
func TestLeads(t *testing.T) {
	d, ok := DepartmentOf(" Security ")
	if !ok || d.Lead != "Security lead" {
		t.Fatalf("%+v %v", d, ok)
	}
	if _, ok := DepartmentOf("legal"); ok {
		t.Error("no such department")
	}
	if p := LeadPrompt("Acme", d); !strings.Contains(p, "Security lead of Acme") || !strings.Contains(p, "VERDICT: READY") {
		t.Errorf("prompt %q", p)
	}
	b := Brief{Objective: " Rotate the key ", DoneWhen: "a plan", Decision: "which vault"}
	if got := b.Text("ABC-1"); got != "Brief from ABC-1's Claude\nObjective: Rotate the key\nDone when: a plan\nDecision needed: which vault" {
		t.Errorf("brief %q", got)
	}
	org := t.TempDir()
	if len(Sessions(org)) != 0 {
		t.Fatal("none yet")
	}
	SetSession(org, "security", "s1")
	SetSession(org, "finance", "s2")
	if s := Sessions(org); s["security"] != "s1" || s["finance"] != "s2" {
		t.Errorf("sessions %v", s)
	}
	os.RemoveAll(Dir(org))
	os.WriteFile(Dir(org), nil, 0o644)
	if SetSession(org, "security", "s3") == nil {
		t.Error("an unwritable folder: an error")
	}
}
