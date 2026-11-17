package infra

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestClaudeCmdProvider: the infra Claude runs on the provider its org's
// [models] infra names, keeping the rest of the environment; a provider
// that isn't there is an error; a desk.toml that doesn't read, or none,
// leaves it on the subscription.
func TestClaudeCmdProvider(t *testing.T) {
	skipOnWindows(t, "the fake claude is a shell script")
	home, bin := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_PROFILE", "")
	t.Setenv("DESK_ROOT", filepath.Join(home, "Work"))
	t.Setenv("PATH", bin)
	t.Setenv("PROV_TOKEN", "sk-1")
	write(t, filepath.Join(bin, "claude"), "#!/bin/sh\n")
	os.Chmod(filepath.Join(bin, "claude"), 0o755)
	toml := func(org, s string) { write(t, filepath.Join(home, "Work", org, ".desk", "desk.toml"), s) }
	toml("Acme", "[models]\ninfra = \"router\"\n\n[[provider]]\nname = \"router\"\nbase_url = \"https://openrouter.ai/api\"\ntoken_env = \"PROV_TOKEN\"\n")
	toml("Globex", "[models]\ninfra = \"gone\"\n")
	toml("Initech", "not toml [")
	dir := func(org string) string { return filepath.Join(home, "Work", org, infraDir) }
	cmd, err := claudeCmd(dir("Acme"), "-p")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(cmd.Env, "ANTHROPIC_BASE_URL=https://openrouter.ai/api") || !slices.Contains(cmd.Env, "ANTHROPIC_AUTH_TOKEN=sk-1") ||
		!slices.ContainsFunc(cmd.Env, func(kv string) bool { return strings.HasPrefix(kv, "PATH=") }) {
		t.Errorf("provider env: %q", cmd.Env)
	}
	if _, err := claudeCmd(dir("Globex"), "-p"); err == nil || !strings.Contains(err.Error(), "gone") {
		t.Errorf("missing provider: %v", err)
	}
	for _, org := range []string{"Initech", "Umbrella"} {
		if cmd, err := claudeCmd(dir(org), "-p"); err != nil || cmd.Env != nil {
			t.Errorf("%s: %q %v", org, cmd.Env, err)
		}
	}
	t.Setenv("PATH", t.TempDir())
	if _, err := claudeCmd(dir("Acme"), "-p"); err == nil {
		t.Error("no claude on PATH must fail")
	}
}
