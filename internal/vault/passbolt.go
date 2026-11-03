package vault

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/passbolt/go-passbolt/api"
	"github.com/passbolt/go-passbolt/helper"

	"github.com/lfdominguez/desk/internal/orgs"
)

// passbolt is a logged-in Passbolt client; its session is renewed on a
// failure, since the key and passphrase stay in it.
type passbolt struct {
	mu sync.Mutex
	c  *api.Client
}

// cliConfig is the part of a go-passbolt-cli config desk uses (the CLI's
// viper writes its keys lowercased).
type cliConfig struct {
	ServerAddress  string `toml:"serveraddress"`
	UserPrivateKey string `toml:"userprivatekey"`
	UserPassword   string `toml:"userpassword"`
	MFATotpToken   string `toml:"mfatotptoken"`
	MFATotpOffset  string `toml:"mfatotpoffset"`
}

func loadCLIConfig(path string) (cliConfig, error) {
	var c cliConfig
	_, err := toml.DecodeFile(home(path), &c)
	return c, err
}

func openPassbolt(ctx context.Context, v orgs.Vault, passphrase, code string) (opened, error) {
	var cli cliConfig
	if v.ConfigFile != "" {
		var err error
		if cli, err = loadCLIConfig(v.ConfigFile); err != nil {
			return nil, fmt.Errorf("config_file: %w", err)
		}
	}
	url, key := cmp.Or(v.URL, cli.ServerAddress), cli.UserPrivateKey
	if v.KeyFile != "" {
		b, err := os.ReadFile(home(v.KeyFile))
		if err != nil {
			return nil, err
		}
		key = string(b)
	}
	if url == "" || key == "" {
		return nil, fmt.Errorf("passbolt needs url and key_file, or config_file")
	}
	c, err := api.NewClient(client, "desk", url, key, passphrase)
	if err != nil {
		return nil, err // a wrong passphrase fails here, unlocking the key
	}
	if cli.MFATotpToken != "" { // the CLI's noninteractive TOTP: desk makes the codes too
		offset, _ := time.ParseDuration(cli.MFATotpOffset)
		helper.AddMFACallbackTOTP(c, 3, 10*time.Second, offset, cli.MFATotpToken)
	} else {
		c.MFACallback = askedTOTP(&code)
	}
	if err := c.Login(ctx); err != nil { // its users/me is where Passbolt asks for MFA
		return nil, fmt.Errorf("login: %w", err)
	}
	return &passbolt{c: c}, nil
}

// askedTOTP answers Passbolt's MFA challenge with the code the user typed,
// once (a code is single-use), asking the server to remember this device;
// with no code left it fails with errNeedCode, for desk to ask.
func askedTOTP(code *string) func(context.Context, *api.Client, *api.APIResponse) (http.Cookie, error) {
	return func(ctx context.Context, c *api.Client, res *api.APIResponse) (http.Cookie, error) {
		if *code == "" {
			return http.Cookie{}, errNeedCode
		}
		body := struct {
			TOTP     string `json:"totp"`
			Remember bool   `json:"remember"`
		}{*code, true}
		*code = ""
		raw, _, err := c.DoCustomRequestAndReturnRawResponseV5(ctx, "POST", "mfa/verify/totp.json", body, nil)
		if err != nil {
			return http.Cookie{}, fmt.Errorf("one-time code: %w", err)
		}
		for _, ck := range raw.Cookies() {
			if ck.Name == "passbolt_mfa" {
				return *ck, nil
			}
		}
		return http.Cookie{}, errors.New("one-time code: no MFA cookie in Passbolt's reply")
	}
}

func (p *passbolt) item(ctx context.Context, name, field string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, err := p.find(ctx, name, field)
	if err != nil { // the session may have expired: log in again (the remembered MFA cookie skips the code)
		if lerr := p.c.Login(ctx); errors.Is(lerr, errNeedCode) {
			return "", lerr
		} else if lerr == nil {
			s, err = p.find(ctx, name, field)
		}
	}
	return s, err
}

// list is every resource's metadata; no secret is decrypted.
func (p *passbolt) list(ctx context.Context) ([]Entry, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	rs, err := p.c.GetResources(ctx, &api.GetResourcesOptions{ContainResourceType: true})
	if err != nil { // the session may have expired: log in again
		if lerr := p.c.Login(ctx); lerr != nil {
			return nil, lerr
		}
		if rs, err = p.c.GetResources(ctx, &api.GetResourcesOptions{ContainResourceType: true}); err != nil {
			return nil, err
		}
	}
	var out []Entry
	for _, r := range rs {
		_, meta, _, err := helper.GetResourceFieldMaps(p.c, r, api.Secret{}, r.ResourceType, false)
		if err != nil {
			continue // ponytail: unreadable metadata is skipped, not reported
		}
		out = append(out, Entry{ID: r.ID, Name: helper.GetStringField(meta, "name"), Username: helper.GetStringField(meta, "username"),
			URI: helper.GetStringField(meta, "uri"), Notes: helper.GetStringField(meta, "description")})
	}
	return out, nil
}

func (p *passbolt) find(ctx context.Context, name, field string) (string, error) {
	rs, err := p.c.GetResources(ctx, &api.GetResourcesOptions{ContainSecret: true, ContainResourceType: true})
	if err != nil {
		return "", err
	}
	for _, r := range rs {
		if len(r.Secrets) == 0 {
			continue
		}
		if r.ID != name { // names of v5 resources are encrypted: read them, not yet the secret
			_, meta, _, err := helper.GetResourceFieldMaps(p.c, r, r.Secrets[0], r.ResourceType, false)
			if err != nil || helper.GetStringField(meta, "name") != name {
				continue
			}
		}
		_, meta, sec, err := helper.GetResourceFieldMaps(p.c, r, r.Secrets[0], r.ResourceType, true)
		if err != nil {
			return "", err
		}
		switch field {
		case "", "password":
			return helper.GetStringField(sec, "password"), nil
		case "username", "uri", "description":
			return helper.GetStringField(meta, field), nil
		case "notes":
			return helper.GetStringField(meta, "description"), nil
		}
		if v, ok := helper.ParseCustomFields(meta, sec).Map()[field]; ok {
			return v, nil
		}
		return "", fmt.Errorf("%q has no field %q", name, field)
	}
	return "", fmt.Errorf("no item %q", name)
}
