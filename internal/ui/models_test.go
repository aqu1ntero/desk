package ui

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lfdominguez/desk/internal/claude"
	"github.com/lfdominguez/desk/internal/kanban"
	"github.com/lfdominguez/desk/internal/term"
)

const routerToml = `[models]
card = "router"
improve = "router"
explain = "router"
workspace = "router"
lead = "router"
drawing = "router"

[[provider]]
name = "router"
base_url = "https://openrouter.ai/api"
token_env = "ROUTER_KEY"
haiku = "qwen/qwen3.8-flash"
`

const routerURL = "ANTHROPIC_BASE_URL=https://openrouter.ai/api"

// TestProviderSession: a card of an org whose [models] card names a
// provider starts on it, and its session's foot says so instead of the
// plan's limits; a token that doesn't resolve stops the start with why.
func TestProviderSession(t *testing.T) {
	h := newHarness(t)
	h.orgToml("Acme", routerToml)
	t.Setenv("ROUTER_KEY", "sk-1")
	c := h.u.b.Add(0, kanban.Card{Title: "Fix it", Org: "Acme"})
	h.u.send(c.ID, "go")
	if len(h.claudes) != 1 || !slices.Contains(h.claudes[0].opts.Env, routerURL) || h.u.via[c.ID] != "router" {
		t.Fatalf("started %d, env %q, via %q", len(h.claudes), h.claudes[0].opts.Env, h.u.via[c.ID])
	}
	h.u.view = "sess:" + c.ID
	h.frame() // the foot: via router, no limits

	t.Setenv("ROUTER_KEY", "")
	d := h.u.b.Add(0, kanban.Card{Title: "Other", Org: "Acme"})
	h.u.send(d.ID, "go")
	if len(h.claudes) != 1 || !strings.Contains(h.u.toast, "router's token") {
		t.Fatalf("started %d, toast %q", len(h.claudes), h.u.toast)
	}
}

// TestProviderLocked: a provider's token in a locked vault asks to unlock
// it, on the UI goroutine or from another.
func TestProviderLocked(t *testing.T) {
	h := newHarness(t)
	h.orgToml("Acme", routerToml+"vault = \"pw\"\nitem = \"router\"\n\n[[vault]]\nname = \"pw\"\nkind = \"bitwarden\"\nemail = \"me@globex.com\"\n")
	if _, _, err := h.u.modelEnv("Acme", "card"); err == nil || len(h.u.locked) != 1 {
		t.Fatalf("%v, locked %d", err, len(h.u.locked))
	}
	clear(h.u.locked)
	if _, err := h.u.goEnv("Acme", "card"); err == nil {
		t.Fatal("no error")
	}
	h.pumpUntil("the unlock", func() bool { return len(h.u.locked) == 1 })
	h.orgToml("Globex", "not toml [")
	if env, name, err := h.u.modelEnv("Globex", "card"); env != nil || name != "" || err != nil {
		t.Fatalf("a broken desk.toml: %q %q %v", env, name, err)
	}
}

// TestProviderOthers: the terminal, the workspace, lead and drawing
// Claudes, Explain and Improve run on their tasks' provider, and fail with
// why when its token doesn't resolve.
func TestProviderOthers(t *testing.T) {
	h := newHarness(t)
	h.orgToml("Acme", routerToml)
	t.Setenv("ROUTER_KEY", "sk-1")
	c := h.u.b.Add(0, kanban.Card{Title: "Fix it", Org: "Acme"})
	var termEnv []string
	startTerm = func(cmd *exec.Cmd, _, _ int, _ func()) (*term.Term, error) {
		termEnv = cmd.Env
		return nil, errors.New("no terminal in tests")
	}
	defer func() { startTerm = term.Start }()
	h.u.terminal(c.ID)
	if !slices.Contains(termEnv, routerURL) || h.u.via[c.ID] != "router" {
		t.Fatalf("terminal env %q", termEnv)
	}
	if _, err := h.u.wsStart(c.ID); err != nil || !slices.Contains(h.claudes[len(h.claudes)-1].opts.Env, routerURL) {
		t.Fatalf("workspace: %v", err)
	}
	var explained []string
	explainCmd = func(_ context.Context, _, _, _, _ string, env ...string) (string, error) {
		explained = env
		return "lists", nil
	}
	h.u.explain(c.ID, "ls")
	h.pumpUntil("the explanation", func() bool { return !h.u.explains["ls"].busy })
	if !slices.Contains(explained, routerURL) {
		t.Fatalf("explain env %q", explained)
	}
	var improved []string
	optimize = func(_ context.Context, a claude.Ask, _ func(claude.Progress)) (claude.Draft, error) {
		improved = a.Env
		return claude.Draft{Title: a.Request}, nil
	}
	h.u.newCard(0)
	s := h.u.edit
	s.notes.SetText("do it @Acme")
	h.u.optimizeCard()
	h.pumpUntil("the draft", func() bool { return s.optimized })
	if !slices.Contains(improved, routerURL) {
		t.Fatalf("improve env %q", improved)
	}

	t.Setenv("ROUTER_KEY", "")
	h.u.terminal(c.ID)
	if !strings.Contains(h.u.toast, "router's token") {
		t.Fatalf("terminal toast %q", h.u.toast)
	}
	if _, err := h.u.wsStart(c.ID); err == nil {
		t.Fatal("workspace without a token")
	}
	if _, err := h.u.leadStart(&leadState{org: "Acme"}, "lead:Acme:x"); err == nil {
		t.Fatal("lead without a token")
	}
	if _, err := h.u.drawStart(&drawState{org: "Acme", path: filepath.Join(t.TempDir(), "x.excalidraw")}); err == nil {
		t.Fatal("drawing without a token")
	}
	delete(h.u.explains, "ls")
	h.u.explain(c.ID, "ls")
	h.pumpUntil("the explanation", func() bool { return !h.u.explains["ls"].busy })
	if !strings.Contains(h.u.explains["ls"].err, "router's token") {
		t.Fatalf("explain err %q", h.u.explains["ls"].err)
	}
	h.u.optimizeCard()
	h.pumpUntil("the failure", func() bool { return s.optStop == nil })
	if !strings.Contains(s.optErr, "router's token") {
		t.Fatalf("improve err %q", s.optErr)
	}
}
