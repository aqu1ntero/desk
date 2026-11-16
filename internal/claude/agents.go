package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Agent is a Claude Code agent file (.claude/agents/<name>.md): its
// frontmatter and, below it, the prompt.
type Agent struct {
	Name        string   `json:"-"`
	Description string   `json:"description"`
	Model       string   `json:"model,omitempty"`
	Tools       []string `json:"tools,omitempty"`
	Prompt      string   `json:"prompt"`
}

// Agents reads the agent files in dir, by name; none when it doesn't exist.
func Agents(dir string) []Agent {
	fs, _ := filepath.Glob(filepath.Join(dir, "*.md"))
	var out []Agent
	for _, f := range fs {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if a, ok := ParseAgent(string(b)); ok {
			out = append(out, a)
		}
	}
	slices.SortFunc(out, func(a, b Agent) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// ParseAgent reads "---\nname: x\n…\n---\nprompt"; ok needs a name.
// ponytail: one "key: value" per line, enough for agent files; a YAML parser if one needs lists or folding.
func ParseAgent(s string) (Agent, bool) {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	rest, ok := strings.CutPrefix(s, "---\n")
	if !ok {
		return Agent{}, false
	}
	head, body, ok := strings.Cut(rest, "\n---")
	if !ok {
		return Agent{}, false
	}
	var a Agent
	for _, l := range strings.Split(head, "\n") {
		k, v, _ := strings.Cut(l, ":")
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		switch strings.TrimSpace(k) {
		case "name":
			a.Name = v
		case "description":
			a.Description = v
		case "model":
			a.Model = v
		case "tools":
			for _, t := range strings.Split(v, ",") {
				if t = strings.TrimSpace(t); t != "" {
					a.Tools = append(a.Tools, t)
				}
			}
		}
	}
	a.Prompt = strings.TrimSpace(strings.TrimPrefix(body, "\n"))
	return a, a.Name != ""
}

// AgentArgs defines every agent of as (--agents), so the session finds
// them wherever Claude starts and can hand work to them, and runs it as
// agent name, unless "". None when as is empty.
func AgentArgs(as []Agent, name string) ([]string, error) {
	if name != "" && !slices.ContainsFunc(as, func(a Agent) bool { return a.Name == name }) {
		return nil, fmt.Errorf("no agent %q", name)
	}
	if len(as) == 0 {
		return nil, nil
	}
	m := map[string]Agent{}
	for _, a := range as {
		m[a.Name] = a
	}
	j, _ := json.Marshal(m) // strings only: it can't fail
	args := []string{"--agents", string(j)}
	if name != "" {
		args = append(args, "--agent", name)
	}
	return args, nil
}
