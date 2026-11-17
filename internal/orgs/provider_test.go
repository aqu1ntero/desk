package orgs

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestProviderFor: a task named in [models] runs on that provider, one not
// named on the subscription, one naming a provider not there is an error;
// desk.toml reads them.
func TestProviderFor(t *testing.T) {
	root := t.TempDir()
	writeAt(filepath.Join(root, "Acme", ".desk", "desk.toml"), []byte("[models]\ncard = \"router\"\nlead = \"gone\"\n\n[[provider]]\nname = \"router\"\nbase_url = \"https://openrouter.ai/api\"\ntoken_env = \"X\"\nhaiku = \"qwen/qwen3.8-flash\"\n\n[company.models]\ntl = \"haiku\"\n"), 0o600)
	c, err := Load(root, "Acme")
	if err != nil {
		t.Fatal(err)
	}
	if p, err := c.ProviderFor("card"); err != nil || p.BaseURL != "https://openrouter.ai/api" || p.TokenEnv != "X" || p.Haiku != "qwen/qwen3.8-flash" {
		t.Fatalf("%+v %v", p, err)
	}
	if p, err := c.ProviderFor("explain"); p != nil || err != nil {
		t.Fatalf("subscription: %+v %v", p, err)
	}
	if _, err := c.ProviderFor("lead"); err == nil || !strings.Contains(err.Error(), "lead") {
		t.Fatalf("missing: %v", err)
	}
	if c.Company.Models["tl"] != "haiku" {
		t.Fatalf("roles: %v", c.Company.Models)
	}
}
