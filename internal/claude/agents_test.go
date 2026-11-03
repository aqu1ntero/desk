package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// TestAgentArgs: agent files are read from their frontmatter, and a session
// runs as one with all of them defined, for the ones it delegates to.
func TestAgentArgs(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "planner.md"), []byte("---\nname: planner\ndescription: Tech lead: plans and delegates\nmodel: opus\ntools: Read, Agent, mcp__tracker__*\n---\n\n# Planner\n\nYou orchestrate.\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "api.md"), []byte("---\nname: api\ndescription: \"API work\"\n---\nYou implement.\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "notes.md"), []byte("no frontmatter"), 0o644)
	as := Agents(dir)
	if len(as) != 2 || as[0].Name != "api" || as[1].Description != "Tech lead: plans and delegates" || as[1].Model != "opus" ||
		!slices.Equal(as[1].Tools, []string{"Read", "Agent", "mcp__tracker__*"}) || as[1].Prompt != "# Planner\n\nYou orchestrate." || as[0].Description != "API work" {
		t.Fatalf("%+v", as)
	}
	args, err := AgentArgs(dir, "planner")
	if err != nil || len(args) != 4 || args[0] != "--agents" || args[2] != "--agent" || args[3] != "planner" {
		t.Fatalf("%q %v", args, err)
	}
	var m map[string]map[string]any
	if err := json.Unmarshal([]byte(args[1]), &m); err != nil || m["api"]["prompt"] != "You implement." || m["planner"]["model"] != "opus" {
		t.Fatalf("--agents %s: %v", args[1], err)
	}
	if _, err := AgentArgs(dir, "qa"); err == nil {
		t.Fatal("an agent that isn't there: no error")
	}
}
