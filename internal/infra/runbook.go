package infra

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Runbooks: markdown files in the org's Infra-Claude/runbooks/, with steps in
// sh fences and {{parameters}}. The palette lists them for the current tab;
// picking one hands it to Claude, which asks for the parameters and runs the
// steps with its tools (writes still wait for approval). Claude writes new
// ones with save_runbook ("save what we just did as a runbook").

type runbook struct{ title, path string }

// runbookDir is the runbooks folder in the org's Infra-Claude dir.
func runbookDir(t *Tab) string { return filepath.Join(infraOf(t.dir), "runbooks") }

// runbookCache is each runbooks folder's list as of its mtime: the palette
// asks on every snapshot while it is open. Model goroutine only.
// ponytail: a runbook edited in place (no file added, removed or renamed)
// keeps its old title until the folder changes.
var runbookCache = map[string]struct {
	mod time.Time
	rbs []runbook
}{}

// runbooks lists t's org's runbooks, titled by their first heading.
func runbooks(t *Tab) []runbook {
	dir := runbookDir(t)
	fi, err := os.Stat(dir)
	if err != nil {
		return nil
	}
	if c, ok := runbookCache[dir]; ok && c.mod.Equal(fi.ModTime()) {
		return c.rbs
	}
	rbs := readRunbooks(dir)
	runbookCache[dir] = struct {
		mod time.Time
		rbs []runbook
	}{fi.ModTime(), rbs}
	return rbs
}

func readRunbooks(dir string) []runbook {
	paths, _ := filepath.Glob(filepath.Join(dir, "*.md"))
	var out []runbook
	for _, p := range paths {
		title := strings.TrimSuffix(filepath.Base(p), ".md")
		if b, err := os.ReadFile(p); err == nil {
			for _, l := range strings.Split(string(b), "\n") {
				if h, ok := strings.CutPrefix(l, "# "); ok {
					title = strings.TrimSpace(h)
					break
				}
			}
		}
		out = append(out, runbook{title, p})
	}
	return out
}

var runbookParam = regexp.MustCompile(`\{\{\s*([\w.-]+)\s*(?:\|[^}]*)?\}\}`)

// runbookPrompt is what Claude gets when the user starts rb.
func runbookPrompt(rb runbook) (string, error) {
	b, err := os.ReadFile(rb.path)
	if err != nil {
		return "", err
	}
	var params []string
	seen := map[string]bool{}
	for _, m := range runbookParam.FindAllStringSubmatch(string(b), -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			params = append(params, m[1])
		}
	}
	ask := ""
	if len(params) > 0 {
		ask = " Its parameters are " + strings.Join(params, ", ") + " ({{name|default}} gives a default): take the values the user " +
			"already gave or that the context makes certain, and ask the user for the rest before running any step that needs them."
	}
	return fmt.Sprintf("(desk) The user started the runbook %q (%s). Follow it step by step.%s Run each sh step with run on the "+
		"right pane (writes wait for approval as usual), check each step's result before the next, and stop and report if one "+
		"fails or doesn't look right. Finish with a short summary of what was done.\n\n<runbook>\n%s\n</runbook>",
		rb.title, rb.path, ask, b), nil
}

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

// saveRunbook writes markdown as a new runbook named after name, never over
// an existing one.
func saveRunbook(t *Tab, name, markdown string) (string, error) {
	slug := strings.Trim(slugRe.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if slug == "" || strings.TrimSpace(markdown) == "" {
		return "", fmt.Errorf("a runbook needs a name and content")
	}
	dir := runbookDir(t)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	p := filepath.Join(dir, slug+".md")
	for i := 2; fileExists(p); i++ {
		p = filepath.Join(dir, fmt.Sprintf("%s-%d.md", slug, i))
	}
	return p, os.WriteFile(p, []byte(redact(markdown)), 0o644)
}
