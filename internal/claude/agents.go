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
		if a, ok := parseAgent(string(b)); ok {
			out = append(out, a)
		}
	}
	slices.SortFunc(out, func(a, b Agent) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// parseAgent reads "---\nname: x\n…\n---\nprompt"; ok needs a name.
// ponytail: one "key: value" per line, enough for agent files; a YAML parser if one needs lists or folding.
func parseAgent(s string) (Agent, bool) {
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

// AgentArgs runs the session as agent name, with every agent in dir
// defined (--agents) so it finds them wherever Claude starts, and the ones
// it delegates to are there too.
func AgentArgs(dir, name string) ([]string, error) {
	as := Agents(dir)
	if !slices.ContainsFunc(as, func(a Agent) bool { return a.Name == name }) {
		return nil, fmt.Errorf("no agent %q in %s", name, dir)
	}
	m := map[string]Agent{}
	for _, a := range as {
		m[a.Name] = a
	}
	j, _ := json.Marshal(m) // strings only: it can't fail
	return []string{"--agents", string(j), "--agent", name}, nil
}
