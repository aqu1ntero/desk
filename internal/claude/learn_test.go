package claude

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLearn: Haiku's answer becomes the lesson (empty statuses dropped);
// the columns reach its schema and its prompt; a failed or empty answer is
// an error.
func TestLearn(t *testing.T) {
	fakeClaude(t, false)
	args := filepath.Join(t.TempDir(), "args")
	t.Setenv("FAKE_ARGS", args)
	t.Setenv("FAKE_OUT", `{"structured_output":{"summary":"Test is deployed to testing.","rules":"Move to Test after deploying.",
		"status":[{"column":"Review","status":"Code Review"},{"column":"Done","status":""}],"follow":[{"status":"Test","column":"Review"}]}}`)
	s := Study{Home: t.TempDir(), Dir: t.TempDir(), Instructions: "Read ./process", Columns: []string{"To do", "Review"}, Statuses: "ABC: Test (indeterminate)\n"}
	l, err := Learn(context.Background(), s)
	if err != nil || l.Summary != "Test is deployed to testing." || l.Rules != "Move to Test after deploying." ||
		len(l.Status) != 1 || l.Status["Review"] != "Code Review" || l.Follow["Test"] != "Review" {
		t.Fatalf("%+v %v", l, err)
	}
	b, _ := os.ReadFile(args)
	if a := string(b); !strings.Contains(a, `"enum":["To do","Review"]`) || !strings.Contains(a, "ABC: Test (indeterminate)") || !strings.Contains(a, "Read ./process") || !strings.Contains(a, "Read,Glob,Grep") {
		t.Fatalf("args: %s", a)
	}
	s.Statuses = ""
	Learn(context.Background(), s)
	if b, _ := os.ReadFile(args); !strings.Contains(string(b), "none read") {
		t.Fatalf("no statuses: %s", b)
	}
	for _, out := range []string{`{"is_error":true,"result":"overloaded"}`, `{"structured_output":{"summary":""}}`, `nope`} {
		t.Setenv("FAKE_OUT", out)
		if _, err := Learn(context.Background(), s); err == nil {
			t.Errorf("%s: no error", out)
		}
	}
	t.Setenv("FAKE_OUT", "")
	t.Setenv("FAKE_EXIT", "1")
	if _, err := Learn(context.Background(), s); err == nil {
		t.Error("a failed run")
	}
	t.Setenv("PATH", t.TempDir())
	if _, err := Learn(context.Background(), s); err == nil {
		t.Error("no claude")
	}
}

// TestWorkflowPrompt: rules are told with the tool to follow them; none, nothing.
func TestWorkflowPrompt(t *testing.T) {
	if WorkflowPrompt("  ") != "" {
		t.Fatal("no rules: no prompt")
	}
	if p := WorkflowPrompt("Test means deployed."); !strings.Contains(p, "Test means deployed.") || !strings.Contains(p, "ticket_status") {
		t.Fatalf("%q", p)
	}
}
