package infra

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path"
	"strings"
)

// Vaults besides desk's (org.go: Passbolt and Bitwarden through
// internal/vault), behind the same search/get interface: HashiCorp Vault's KV
// v2 over HTTP (token from VAULT_TOKEN or token_env). desk uses no CLIs for
// providers.

func matches(q string, fields ...string) bool {
	q = strings.ToLower(q)
	for _, f := range fields {
		if strings.Contains(strings.ToLower(f), q) {
			return true
		}
	}
	return q == ""
}

// hashiVault: KV v2 under mount (default "secret"); an item is a secret's
// path, its username/password/url/description keys fill the item.
type hashiVault struct{ cfg VaultCfg }

func (h hashiVault) call(ctx context.Context, p string, v any) error {
	tok := os.Getenv("VAULT_TOKEN")
	if h.cfg.TokenEnv != "" {
		tok = os.Getenv(h.cfg.TokenEnv)
	}
	if tok == "" {
		return fmt.Errorf("vault %s: no token (set VAULT_TOKEN, or token_env in desk.toml, before starting desk)", h.cfg.Name)
	}
	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(h.cfg.URL, "/")+"/v1/"+p, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Vault-Token", tok)
	res, err := httpClient(h.cfg.Insecure).Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("vault %s: %s for %s", h.cfg.Name, res.Status, p)
	}
	return json.NewDecoder(res.Body).Decode(v)
}

func (h hashiVault) mount() string {
	if h.cfg.Mount != "" {
		return strings.Trim(h.cfg.Mount, "/")
	}
	return "secret"
}

// list walks the KV tree, three levels deep at most.
func (h hashiVault) list(ctx context.Context, dir string, depth int, out *[]string) error {
	var r struct {
		Data struct{ Keys []string } `json:"data"`
	}
	if err := h.call(ctx, h.mount()+"/metadata/"+dir+"?list=true", &r); err != nil {
		return err
	}
	for _, k := range r.Data.Keys {
		if strings.HasSuffix(k, "/") {
			if depth < 3 {
				h.list(ctx, dir+k, depth+1, out)
			}
			continue
		}
		*out = append(*out, dir+k)
	}
	return nil
}

func (h hashiVault) search(ctx context.Context, q string) ([]vaultItem, error) {
	var paths []string
	if err := h.list(ctx, "", 0, &paths); err != nil {
		return nil, err
	}
	var out []vaultItem
	for _, p := range paths {
		if matches(q, p) {
			out = append(out, vaultItem{ID: p, Name: p})
		}
	}
	return out, nil
}

func (h hashiVault) get(ctx context.Context, id string) (vaultItem, error) {
	var r struct {
		Data struct{ Data map[string]any } `json:"data"`
	}
	if err := h.call(ctx, h.mount()+"/data/"+strings.TrimLeft(path.Clean("/"+id), "/"), &r); err != nil {
		return vaultItem{}, err
	}
	s := func(k string) string { v, _ := r.Data.Data[k].(string); return v }
	return vaultItem{ID: id, Name: id, Username: s("username"), Password: s("password"), URI: s("url"), Description: s("description")}, nil
}
