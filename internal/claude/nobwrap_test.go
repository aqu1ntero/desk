package claude

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// TestMain runs the package's tests with bwrap's profiles, Linux's, on every
// OS; TestNoBwrap turns macOS's on.
func TestMain(m *testing.M) {
	if fakeMain() {
		return
	}
	NoBwrap = false
	os.Exit(m.Run())
}

// noBwrap turns on macOS's profiles for t.
func noBwrap(t *testing.T) {
	NoBwrap = true
	t.Cleanup(func() { NoBwrap = false })
}

// TestNoBwrap: on macOS a profile is plain claude with CLAUDE_CONFIG_DIR at
// its folder (no bwrap, no seeded json); its account reads the folder's own
// .claude.json; its login is BROWSER=true; the default account's is as-is.
func TestNoBwrap(t *testing.T) {
	noBwrap(t)
	bin := fakeClaude(t, false)
	home := t.TempDir()
	cfg := filepath.Join(home, "Secure", "Claudes", "Globex")
	os.MkdirAll(cfg, 0o700)
	t.Setenv("CLAUDE_CONFIG_DIR", "/elsewhere")
	t.Setenv("CLAUDECODE", "1")
	c, err := Command(home, "Globex", "-p")
	if err != nil || c.Path != filepath.Join(bin, "claude"+exe) || c.Args[1] != "-p" {
		t.Fatalf("%v %v", c, err)
	}
	if env := c.Environ(); !slices.Contains(env, "CLAUDE_CONFIG_DIR="+cfg) || slices.Contains(env, "CLAUDE_CONFIG_DIR=/elsewhere") ||
		!slices.Contains(env, "CLAUDE_PROFILE=Globex") || slices.Contains(env, "CLAUDECODE=1") {
		t.Fatalf("env %v", env)
	}
	if _, err := os.Stat(filepath.Join(home, "Secure", "Claudes", "claude-globex.json")); err == nil {
		t.Error("seeded a json bwrap would bind")
	}
	os.WriteFile(filepath.Join(cfg, ".claude.json"), []byte(`{"oauthAccount":{"EmailAddress":"me@globex.com"}}`), 0o600)
	if got := AccountName(home, "Globex"); got != "Globex account · me@globex.com" {
		t.Errorf("account %q", got)
	}
	c, own, err := Login(home, "Globex")
	if err != nil || !own || !slices.Contains(c.Env, "BROWSER=true") || c.Path != filepath.Join(bin, "claude"+exe) {
		t.Fatalf("own login: %v %v %v", c, own, err)
	}
	c, own, err = Login(home, "Acme")
	if err != nil || own || slices.Contains(c.Env, "BROWSER=true") {
		t.Fatalf("default login: %v %v %v", c, own, err)
	}
}
