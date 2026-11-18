package orgs

import (
	"path/filepath"
	"testing"
)

// TestAboutMe: the global Who am I, then the organization's; only the
// organization's when it doesn't extend; the global one for an
// organization without one, or whose desk.toml doesn't read.
func TestAboutMe(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	toml := func(org, s string) { writeAt(filepath.Join(root, org, ".desk", "desk.toml"), []byte(s), 0o600) }
	toml("Acme", "[me]\nabout = \"Backend lead here.\"\n")
	toml("Globex", "[me]\nabout = \"Only a reviewer here.\"\nextend = false\n")
	toml("Initech", "[me]\nextend = false\n")
	toml("Umbrella", "not toml [")
	if AboutMe(root, "Acme") != "Backend lead here." {
		t.Fatalf("no global: %q", AboutMe(root, "Acme"))
	}
	if err := SaveMe("I write Go."); err != nil || Me() != "I write Go." {
		t.Fatalf("%v %q", err, Me())
	}
	for org, want := range map[string]string{
		"Acme":     "I write Go.\n\nBackend lead here.",
		"Globex":   "Only a reviewer here.",
		"Initech":  "",
		"Umbrella": "I write Go.",
		"Soylent":  "I write Go.",
	} {
		if got := AboutMe(root, org); got != want {
			t.Errorf("%s: %q, want %q", org, got, want)
		}
	}
}
