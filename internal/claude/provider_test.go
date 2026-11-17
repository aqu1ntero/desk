package claude

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/lfdominguez/desk/internal/orgs"
)

func providerCfg() orgs.Config {
	return orgs.Config{
		Providers: []orgs.Provider{{Name: "router", BaseURL: "https://openrouter.ai/api", Secret: orgs.Secret{TokenEnv: "PROV_TOKEN"}, Sonnet: "z-ai/glm-5.3", Haiku: "qwen/qwen3.8-flash"}},
		Models:    map[string]string{"card": "router", "lead": "gone"},
	}
}

// TestProviderEnv: a task on a provider gets its endpoint, token and the
// models it set, in place of the login; one on the subscription nothing;
// one naming a provider not there, or whose token doesn't resolve, an error.
func TestProviderEnv(t *testing.T) {
	t.Setenv("PROV_TOKEN", "sk-1")
	env, name, err := ProviderEnv(context.Background(), providerCfg(), "card")
	want := []string{"ANTHROPIC_BASE_URL=https://openrouter.ai/api", "ANTHROPIC_AUTH_TOKEN=sk-1", "ANTHROPIC_API_KEY=",
		"ANTHROPIC_DEFAULT_SONNET_MODEL=z-ai/glm-5.3", "ANTHROPIC_DEFAULT_HAIKU_MODEL=qwen/qwen3.8-flash"}
	if err != nil || name != "router" || !slices.Equal(env, want) {
		t.Fatalf("%q %q %v", env, name, err)
	}
	cfg := providerCfg()
	cfg.Providers[0].Opus = "openai/gpt-6.1-sol"
	if env, _, _ := ProviderEnv(context.Background(), cfg, "card"); !slices.Contains(env, "ANTHROPIC_DEFAULT_OPUS_MODEL=openai/gpt-6.1-sol") {
		t.Fatalf("opus: %q", env)
	}
	if env, name, err := ProviderEnv(context.Background(), providerCfg(), "explain"); env != nil || name != "" || err != nil {
		t.Fatalf("subscription: %q %q %v", env, name, err)
	}
	if _, _, err := ProviderEnv(context.Background(), providerCfg(), "lead"); err == nil || !strings.Contains(err.Error(), `"gone"`) {
		t.Fatalf("missing provider: %v", err)
	}
	t.Setenv("PROV_TOKEN", "")
	if _, name, err := ProviderEnv(context.Background(), providerCfg(), "card"); name != "router" || err == nil || !strings.Contains(err.Error(), "router's token") {
		t.Fatalf("no token: %q %v", name, err)
	}
}

// TestStartEnv: Options.Env reaches the session's process.
func TestStartEnv(t *testing.T) {
	fakeClaude(t, false)
	got := make(chan Event, 10)
	p, err := Start(Options{Home: t.TempDir(), Dir: t.TempDir(), Env: []string{"ANTHROPIC_BASE_URL=https://example.com"}}, func(e Event) { got <- e })
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(p.cmd.Env, "ANTHROPIC_BASE_URL=https://example.com") {
		t.Fatalf("%q", p.cmd.Env)
	}
	for e := range got {
		if e.Kind == KindExit {
			break
		}
	}
}

// TestOptimizeExplainEnv: Improve's and Explain's processes get their env
// on top of Command's.
func TestOptimizeExplainEnv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake is a shell script")
	}
	bin := fakeClaude(t, false)
	writeFake(t, bin, "claude", "#!/bin/sh\nprintf '%s' \"$ANTHROPIC_BASE_URL\" > \"$FAKE_ENV\"\nexit 1\n", "")
	for name, run := range map[string]func(string){
		"optimize": func(u string) {
			Optimize(context.Background(), Ask{Home: t.TempDir(), Dir: t.TempDir(), Env: []string{"ANTHROPIC_BASE_URL=" + u}}, nil)
		},
		"explain": func(u string) {
			Explain(context.Background(), t.TempDir(), "", t.TempDir(), "ls", "ANTHROPIC_BASE_URL="+u)
		},
	} {
		f := filepath.Join(t.TempDir(), "env")
		t.Setenv("FAKE_ENV", f)
		run("https://" + name + ".example.com")
		if b, _ := os.ReadFile(f); string(b) != "https://"+name+".example.com" {
			t.Errorf("%s: %q", name, b)
		}
	}
}
