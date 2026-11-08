package infra

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	deskorgs "github.com/lfdominguez/desk/internal/orgs"
	deskvault "github.com/lfdominguez/desk/internal/vault"
)

// orgConfig is an organization's infra config: the external systems Claude may
// use there. It comes from the org's desk.toml (<root>/<Org>/.desk/desk.toml, see
// internal/orgs), the same file desk's links read:
//
//	record = false # top-level, before any [table]: don't record panes as asciicasts (on by default)
//	clear_login = false # top-level: keep what a pane printed before its first prompt (the motd)
//
//	[[vault]]                  # passbolt and bitwarden open through internal/vault (shared with desk's links)
//	name = "hv"
//	kind = "hashicorp"         # KV v2 over HTTP
//	url = "https://vault.example.com"
//	mount = "secret"
//	token_env = "VAULT_TOKEN"
//
//	[[nagios]]
//	name = "nagios1"
//	url = "https://nagios.example.com/nagios4/"
//	user = "nagiosadmin"
//	vault = "acme"
//	item = "nagios web"
//	alerts = true # poll every minute: desktop alerts, "investigate" in the palette
//
//	[[metrics]]
//	name = "prometheus"
//	type = "victoriametrics" # or prometheus; same HTTP API
//	url = "https://vm.example.com/"
//	token_env = "VM_TOKEN"   # a bearer token; with user, basic auth
//
//	[schedule]
//	health = "08:30" # daily read-only health check; a notification when it's ready
//
//	[approvals]
//	allow = ["systemctl status *", "journalctl *"] # never ask (single commands only)
//	deny = ["* --force*", "reboot*"]               # always ask, even for reads
type orgConfig struct {
	Vaults    []VaultCfg
	Nagios    []SourceCfg
	Metrics   []SourceCfg
	Approvals deskorgs.Approvals
	Record    *bool // record every pane as an asciicast (audit.go); unset means on
	// ClearLogin: false keeps a pane's login output past the shell
	// integration's clear (term.go's keepLogin); unset means clear.
	ClearLogin *bool
	Schedule   deskorgs.Schedule

	mu     sync.Mutex
	vaults map[string]vault
	logins map[string][2]string // source name -> user, password
}

type VaultCfg struct {
	Name     string
	Type     string
	URL      string
	Insecure bool
	Mount    string // hashicorp: the KV v2 mount (default "secret")
	TokenEnv string // hashicorp: env var with the token (default VAULT_TOKEN)

	desk deskorgs.Vault // passbolt, bitwarden: the vault internal/vault opens
}

type SourceCfg struct {
	Name     string
	Type     string
	URL      string
	Insecure bool
	Alerts   bool // nagios: poll it and raise alerts (alerts.go)

	user   string           // the login (basic auth); none, the secret is a bearer token
	secret *deskorgs.Secret // where its password or token comes from; nil, no auth
}

// SecretRef points at a vault item by id or exact name; Field "" is its
// password.
type SecretRef struct {
	Vault, Item, Field string
	Raw                string // #@ password-raw: the password itself, no vault
}

var (
	orgsMu sync.Mutex
	orgs   = map[string]*orgConfig{} // by infra dir, shared by the org's tabs
)

// reloadOrgs drops the cached configs, so the next orgFor rereads them.
func reloadOrgs() {
	orgsMu.Lock()
	orgs = map[string]*orgConfig{}
	orgsMu.Unlock()
}

// infraDir is where an org's infra lives inside its work folder, out of sight
// beside the hosts-cmd cache: the dir of every tab and pane, the infra
// Claude's working folder (its CLAUDE.md, runbooks). The org folder also
// decides which Claude account runs (see claudeCmd).
var infraDir = filepath.Join(deskorgs.DeskDir, "infra")

// oldInfraDir is where infraDir was before it moved (makeInfra, movedInfra).
const oldInfraDir = "Infra-Claude"

// orgOfInfra is the org folder of an infra dir.
func orgOfInfra(dir string) string { return filepath.Dir(filepath.Dir(dir)) }

// isInfraDir says dir is an org's infra dir.
func isInfraDir(dir string) bool {
	return filepath.Join(filepath.Base(filepath.Dir(dir)), filepath.Base(dir)) == infraDir
}

// makeInfra creates org folder's infra dir and returns it. An old
// <Org>/Infra-Claude moves there, once (its old group folders come along).
func makeInfra(org string) string {
	dir := filepath.Join(org, infraDir)
	os.MkdirAll(filepath.Dir(dir), 0o755)
	os.Rename(filepath.Join(org, oldInfraDir), dir) // changes nothing once dir exists (Go refuses an existing target) or without an old one
	os.MkdirAll(dir, 0o755)
	return dir
}

// movedInfra maps a dir saved before the move (<Org>/Infra-Claude or one of
// its group dirs) to the org's infra dir; any other dir stays.
func movedInfra(dir string) string {
	for _, d := range []string{dir, filepath.Dir(dir)} {
		if filepath.Base(d) == oldInfraDir {
			return filepath.Join(filepath.Dir(d), infraDir)
		}
	}
	return dir
}

// orgFor loads (once) the config of the org owning a pane or tab dir (the
// org's infra dir). A missing file is an empty Org; a broken one is an error.
func orgFor(dir string) (*orgConfig, error) {
	orgsMu.Lock()
	defer orgsMu.Unlock()
	if o, ok := orgs[dir]; ok {
		return o, nil
	}
	o, err := loadOrg(dir)
	if err != nil {
		return nil, err
	}
	orgs[dir] = o
	return o, nil
}

// loadOrg reads the desk.toml next to dir (the org's work folder's).
func loadOrg(dir string) (*orgConfig, error) {
	work := orgOfInfra(dir)
	c, err := deskorgs.Load(filepath.Dir(work), filepath.Base(work))
	if err != nil {
		return nil, err
	}
	return fromDesk(c), nil
}

// fromDesk is the Org desk.toml describes.
func fromDesk(c deskorgs.Config) *orgConfig {
	o := &orgConfig{Record: c.Record, ClearLogin: c.ClearLogin, Approvals: c.Approvals, Schedule: c.Schedule}
	for _, v := range c.Vaults {
		o.Vaults = append(o.Vaults, VaultCfg{Name: v.Name, Type: v.Kind, URL: v.URL, Insecure: v.Insecure, Mount: v.Mount, TokenEnv: v.TokenEnv, desk: v})
	}
	for _, n := range c.Nagios {
		s := n.Secret
		o.Nagios = append(o.Nagios, SourceCfg{Name: sourceName(n.Name, n.URL), URL: n.URL, Insecure: n.Insecure, Alerts: n.Alerts, user: n.User, secret: &s})
	}
	for _, m := range c.Metrics {
		s := m.Secret
		o.Metrics = append(o.Metrics, SourceCfg{Name: sourceName(m.Name, m.URL), Type: m.Type, URL: m.URL, Insecure: m.Insecure, user: m.User, secret: &s})
	}
	return o
}

// sourceName is a source's name, else its URL's host (desk.toml's names are
// optional; Claude's tools pick sources by name).
func sourceName(name, raw string) string {
	if name != "" {
		return name
	}
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u.Host
	}
	return raw
}

// records: panes are recorded unless the org's config says record = false
// (no config at all records too).
func (o *orgConfig) records() bool { return o == nil || o.Record == nil || *o.Record }

// keepsLogin: a pane keeps its login output only when the org's config
// says clear_login = false.
func (o *orgConfig) keepsLogin() bool { return o != nil && o.ClearLogin != nil && !*o.ClearLogin }

// toolGroups names the optional tool sets this org has sources for.
func (o *orgConfig) toolGroups() []string {
	var g []string
	if len(o.Vaults) > 0 {
		g = append(g, "secrets")
	}
	if len(o.Nagios) > 0 {
		g = append(g, "nagios")
	}
	if len(o.Metrics) > 0 {
		g = append(g, "metrics")
	}
	return g
}

// describe tells Claude what the org has, for the system prompt.
func (o *orgConfig) describe() string {
	var parts []string
	list := func(kind string, n int, name func(i int) string) {
		var ns []string
		for i := 0; i < n; i++ {
			ns = append(ns, name(i))
		}
		if len(ns) > 0 {
			parts = append(parts, kind+": "+strings.Join(ns, ", "))
		}
	}
	list("secret vaults (secrets_search, secret_get)", len(o.Vaults), func(i int) string { return o.Vaults[i].Name + " (" + o.Vaults[i].Type + ")" })
	list("Nagios (nagios)", len(o.Nagios), func(i int) string { return o.Nagios[i].Name })
	list("metrics, PromQL (metrics)", len(o.Metrics), func(i int) string {
		if o.Metrics[i].Type != "" {
			return o.Metrics[i].Name + " (" + o.Metrics[i].Type + ")"
		}
		return o.Metrics[i].Name
	})
	return strings.Join(parts, "; ")
}

func httpClient(insecure bool) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return &http.Client{Timeout: 30 * time.Second, Transport: tr}
}

func expandHome(p string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, rest)
	}
	return p
}

// vaultItem is one secret; Password is only filled by get.
type vaultItem struct {
	ID, Name, Username, URI, Password, Description string
}

type vault interface {
	search(ctx context.Context, q string) ([]vaultItem, error)
	get(ctx context.Context, id string) (vaultItem, error)
}

func (o *orgConfig) vault(name string) (vault, VaultCfg, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, c := range o.Vaults {
		if name != "" && c.Name != name {
			continue
		}
		if v, ok := o.vaults[c.Name]; ok {
			return v, c, nil
		}
		var v vault
		switch c.Type {
		case "passbolt", "bitwarden", "vaultwarden":
			v = deskVault{c.desk}
		case "hashicorp":
			v = hashiVault{cfg: c}
		default: // desk speaks to vaults in Go only, never through a CLI
			return nil, c, fmt.Errorf("vault %s: unknown type %q (supported: passbolt, bitwarden, hashicorp)", c.Name, c.Type)
		}
		if o.vaults == nil {
			o.vaults = map[string]vault{}
		}
		o.vaults[c.Name] = v
		return v, c, nil
	}
	return nil, VaultCfg{}, fmt.Errorf("no vault %q", name)
}

// resolve finds a vault item by id or exact (case-insensitive) name.
func resolve(ctx context.Context, v vault, item string) (vaultItem, error) {
	items, err := v.search(ctx, "")
	if err != nil {
		return vaultItem{}, err
	}
	var hit []vaultItem
	for _, it := range items {
		if it.ID == item || strings.EqualFold(it.Name, item) {
			hit = append(hit, it)
		}
	}
	switch len(hit) {
	case 0:
		return vaultItem{}, fmt.Errorf("no item %q", item)
	case 1:
		return v.get(ctx, hit[0].ID)
	}
	return vaultItem{}, fmt.Errorf("%d items named %q; use the id", len(hit), item)
}

// login returns a source's credentials, fetched once per run: its user and
// its secret.
func (o *orgConfig) login(ctx context.Context, s SourceCfg) (user, pass string, err error) {
	if s.secret == nil {
		return "", "", nil
	}
	o.mu.Lock()
	c, ok := o.logins[s.Name]
	o.mu.Unlock()
	if ok {
		return c[0], c[1], nil
	}
	user = s.user
	pass, err = unlocking(ctx, o.deskVault(s.secret.Vault), func() (string, error) { return deskvault.Resolve(ctx, o.deskVaults(), *s.secret) })
	if err != nil {
		return "", "", fmt.Errorf("login for %s: %w", s.Name, err)
	}
	o.mu.Lock()
	if o.logins == nil {
		o.logins = map[string][2]string{}
	}
	o.logins[s.Name] = [2]string{user, pass}
	o.mu.Unlock()
	return user, pass, nil
}

// deskVaults are the org's vaults as internal/vault knows them.
func (o *orgConfig) deskVaults() []deskorgs.Vault {
	var vs []deskorgs.Vault
	for _, c := range o.Vaults {
		vs = append(vs, c.desk)
	}
	return vs
}

func (o *orgConfig) deskVault(name string) deskorgs.Vault {
	for _, c := range o.Vaults {
		if c.Name == name {
			return c.desk
		}
	}
	return deskorgs.Vault{Name: name}
}

// authorize sets the source's credentials on a request: basic with a user,
// else bearer.
func (o *orgConfig) authorize(ctx context.Context, s SourceCfg, req *http.Request) error {
	user, pass, err := o.login(ctx, s)
	if err != nil || pass == "" && user == "" {
		return err
	}
	if user == "" {
		req.Header.Set("Authorization", "Bearer "+pass)
	} else {
		req.SetBasicAuth(user, pass)
	}
	return nil
}

// deskVault is a Passbolt or Bitwarden vault opened by desk's internal/vault:
// unlocked once for desk's links and the infra Claude alike. A locked one asks
// its password (or one-time code) in the workspace's ask sheet.
type deskVault struct{ v deskorgs.Vault }

func (d deskVault) search(ctx context.Context, q string) ([]vaultItem, error) {
	es, err := unlocking(ctx, d.v, func() ([]deskvault.Entry, error) { return deskvault.List(ctx, d.v) })
	if err != nil {
		return nil, err
	}
	var out []vaultItem
	for _, e := range es {
		if matches(q, e.Name, e.Username, e.URI) {
			out = append(out, vaultItem{ID: e.ID, Name: e.Name, Username: e.Username, URI: e.URI, Description: e.Notes})
		}
	}
	return out, nil
}

func (d deskVault) get(ctx context.Context, id string) (vaultItem, error) {
	items, err := d.search(ctx, "")
	if err != nil {
		return vaultItem{}, err
	}
	i := slices.IndexFunc(items, func(it vaultItem) bool { return it.ID == id })
	if i < 0 {
		return vaultItem{}, fmt.Errorf("no item %q", id)
	}
	it := items[i]
	it.Password, err = unlocking(ctx, d.v, func() (string, error) { return deskvault.Get(ctx, d.v, id, "password") })
	return it, err
}

// unlocking runs f, and while v is locked asks the user for its password (or
// the one-time code its server wants) and unlocks it.
func unlocking[T any](ctx context.Context, v deskorgs.Vault, f func() (T, error)) (T, error) {
	var pw string
	for range 5 {
		r, err := f()
		var le *deskvault.LockedError
		if !errors.As(err, &le) {
			return r, err
		}
		code := ""
		if le.Code {
			c, ok := askUser("One-time code · "+v.Name, false)
			if !ok {
				return r, errors.New("the user cancelled the one-time code")
			}
			code = strings.TrimSpace(c)
		} else {
			title := "Master password · " + v.Name
			if v.Kind == "passbolt" {
				title = "Passbolt passphrase · " + v.Name
			}
			p, ok := askUser(title, true)
			if !ok {
				return r, errors.New("the user cancelled the vault unlock")
			}
			pw = p
		}
		if err := deskvault.Unlock(ctx, v, pw, code); err != nil && !errors.As(err, &le) {
			return r, err
		}
	}
	var zero T
	return zero, fmt.Errorf("vault %q stayed locked", v.Name)
}

// question is a line desk asks the user for (a passphrase, a 2FA code) on
// behalf of an integration.
type question struct {
	title  string
	masked bool
	text   string
	done   chan string // closed without a value when cancelled
}

type questionMsg struct{ q *question }

// askUser blocks a tool call until the user answers in the ask sheet.
func askUser(title string, masked bool) (string, bool) {
	if dispatch == nil {
		return "", false
	}
	q := &question{title: title, masked: masked, done: make(chan string, 1)}
	dispatch(questionMsg{q})
	s, ok := <-q.done
	return s, ok
}
