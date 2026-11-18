package orgs

import "testing"

// TestWorkflowColumnOf: Follow first, by any case, then Status read
// backwards: of columns sharing a status, the card's own when it is one,
// else the first in the board's order; none "". On with rules or a mapping.
func TestWorkflowColumnOf(t *testing.T) {
	cols := []string{"Backlog", "To do", "In progress", "Blocked", "Review", "Done"}
	w := Workflow{Status: map[string]string{"Review": "Code Review", "Blocked": "In Progress", "In progress": "In Progress", "Backlog": "To Do", "To do": "To Do"},
		Follow: map[string]string{"Test": "Review"}}
	for _, c := range []struct{ status, cur, want string }{
		{"test", "In progress", "Review"},
		{"code review", "In progress", "Review"},
		{"In Progress", "In progress", "In progress"}, // its own column shares it: it stays
		{"in progress", "Blocked", "Blocked"},
		{"In Progress", "Review", "In progress"}, // board order, not the name's: Blocked comes later
		{"To Do", "Done", "Backlog"},
		{"Deployed", "To do", ""},
	} {
		if got := w.ColumnOf(c.status, c.cur, cols); got != c.want {
			t.Errorf("%s from %s: %q, want %q", c.status, c.cur, got, c.want)
		}
	}
	if got := w.ColumnOf("Code Review", "To do", nil); got != "" {
		t.Errorf("a column off the board: %q", got)
	}
	if (Workflow{}).On() || !(Workflow{Rules: "x"}).On() || !w.On() {
		t.Error("On")
	}
}
