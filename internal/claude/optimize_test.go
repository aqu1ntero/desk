package claude

import (
	"encoding/json"
	"testing"
)

// TestParseAsk: a can_use_tool request for AskUserQuestion is an ask with
// its questions; Answered puts the answers into its input.
func TestParseAsk(t *testing.T) {
	ev := Parse([]byte(`{"type":"control_request","request_id":"r1","request":{"subtype":"can_use_tool","tool_name":"AskUserQuestion",
		"input":{"questions":[{"question":"Color?","header":"Color","multiSelect":false,"options":[{"label":"Red","description":"warm"},{"label":"Blue"}]}]}}}`))
	if len(ev) != 1 || ev[0].Kind != KindAsk || ev[0].Ask.ID != "r1" || len(ev[0].Ask.Questions) != 1 || ev[0].Ask.Questions[0].Options[1].Label != "Blue" {
		t.Fatalf("%+v", ev)
	}
	var in struct {
		Questions []Question
		Answers   map[string]string
	}
	json.Unmarshal(Answered(ev[0].Ask.Input, map[string]string{"Color?": "Blue"}), &in)
	if len(in.Questions) != 1 || in.Answers["Color?"] != "Blue" {
		t.Fatalf("answered %+v", in)
	}
	if ev := Parse([]byte(`{"type":"control_request","request_id":"r2","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"rm -rf build"}}}`)); len(ev) != 1 || ev[0].Ask.Line != "Bash  rm -rf build" {
		t.Fatalf("%+v", ev)
	}
}

func TestParseDraft(t *testing.T) {
	d, err := parseDraft([]byte(`{"type":"result","is_error":false,"result":"{}","structured_output":{"title":" Fix login ","prompt":"p","labels":["auth"],"priority":"high","folder":"","links":[],"due":""}}`))
	if err != nil || d.Title != "Fix login" || d.Labels[0] != "auth" || d.Priority != "high" {
		t.Fatalf("%+v %v", d, err)
	}
	if _, err := parseDraft([]byte(`{"is_error":true,"result":"Credit balance is too low"}`)); err == nil || err.Error() != "Credit balance is too low" {
		t.Fatalf("error reply: %v", err)
	}
}
