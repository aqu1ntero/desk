// Package vault reads secrets out of password managers, in Go: Bitwarden
// (and Vaultwarden), by its API and its client-side crypto, and Passbolt, by
// its SDK. A vault is unlocked once, with its configured password or the one
// desk asks for, and stays unlocked, in memory only, while desk runs.
package vault

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/lfdominguez/desk/internal/orgs"
)

var client = &http.Client{Timeout: 30 * time.Second}

// opened is an unlocked vault.
type opened interface {
	item(ctx context.Context, name, field string) (string, error)
	list(ctx context.Context) ([]Entry, error)
}

// Entry is an item as a list shows it: no password.
type Entry struct{ ID, Name, Username, URI, Notes string }

// List is every item of v, unlocking v as Get does.
func List(ctx context.Context, v orgs.Vault) ([]Entry, error) {
	mu.Lock()
	o := open[Key(v)]
	mu.Unlock()
	if o == nil {
		if err := Unlock(ctx, v, "", ""); err != nil {
			return nil, err
		}
		mu.Lock()
		o = open[Key(v)]
		mu.Unlock()
	}
	es, err := o.list(ctx)
	if errors.Is(err, errNeedCode) {
		return nil, &LockedError{Vault: v, Code: true}
	}
	if err != nil {
		return nil, fmt.Errorf("vault %q: %w", v.Name, err)
	}
	return es, nil
}

// Resolve reads secret s: an item of one of vaults (by name), or its
// variable, command or literal token.
func Resolve(ctx context.Context, vaults []orgs.Vault, s orgs.Secret) (string, error) {
	if s.Vault == "" {
		return s.Get()
	}
	i := slices.IndexFunc(vaults, func(v orgs.Vault) bool { return v.Name == s.Vault })
	if i < 0 {
		return "", fmt.Errorf("no [[vault]] named %q in desk.toml", s.Vault)
	}
	return Get(ctx, vaults[i], s.Item, s.Field)
}

type cached struct {
	v  string
	at time.Time
}

var (
	mu     sync.Mutex
	open   = map[string]opened{} // by Key
	values = map[string]cached{} // by Key, item and field; links poll every few minutes
)

// Key names v's account: vaults of different orgs with the same Key are one.
func Key(v orgs.Vault) string {
	return strings.Join([]string{v.Kind, strings.TrimRight(v.URL, "/"), strings.ToLower(v.Email), v.KeyFile, v.ConfigFile}, " ")
}

// LockedError is a vault that needs its password from the user or, with
// Code, a one-time code (its password is configured).
type LockedError struct {
	Vault orgs.Vault
	Code  bool
}

func (e *LockedError) Error() string {
	if e.Code {
		return fmt.Sprintf("vault %q needs a one-time code", e.Vault.Name)
	}
	return fmt.Sprintf("vault %q is locked", e.Vault.Name)
}

// Get reads field of item from v, unlocking v with its configured password
// if it has one, else failing with a *LockedError.
func Get(ctx context.Context, v orgs.Vault, item, field string) (string, error) {
	if item == "" {
		return "", fmt.Errorf("vault %q: which item? (item = \"…\")", v.Name)
	}
	ck := Key(v) + "\x00" + item + "\x00" + field
	mu.Lock()
	o, c := open[Key(v)], values[ck]
	mu.Unlock()
	if o != nil && time.Since(c.at) < 10*time.Minute {
		return c.v, nil
	}
	if o == nil {
		if err := Unlock(ctx, v, "", ""); err != nil {
			return "", err
		}
		mu.Lock()
		o = open[Key(v)]
		mu.Unlock()
	}
	s, err := o.item(ctx, item, field)
	if errors.Is(err, errNeedCode) { // logged in again, and the server wants a code
		return "", &LockedError{Vault: v, Code: true}
	}
	if err != nil {
		return "", fmt.Errorf("vault %q: %w", v.Name, err)
	}
	mu.Lock()
	values[ck] = cached{s, time.Now()}
	mu.Unlock()
	return s, nil
}

// Unlocked says whether v is open.
func Unlocked(v orgs.Vault) bool {
	mu.Lock()
	defer mu.Unlock()
	return open[Key(v)] != nil
}

// errNeedCode is a login that needs a one-time code nobody gave.
var errNeedCode = errors.New("needs a one-time code")

// Unlock opens v: logs in and keeps its keys in memory. password ""
// is v's configured one, else a *LockedError; code is a one-time code, for
// a login that asks for one (else a *LockedError with Code).
func Unlock(ctx context.Context, v orgs.Vault, password, code string) error {
	if password == "" {
		var err error
		if password, err = configured(v); err != nil {
			return fmt.Errorf("vault %q password: %w", v.Name, err)
		}
		if password == "" {
			return &LockedError{Vault: v}
		}
	}
	var o opened
	var err error
	switch v.Kind {
	case "bitwarden", "vaultwarden":
		o, err = openBitwarden(ctx, v, password)
	case "passbolt":
		o, err = openPassbolt(ctx, v, password, code)
	default:
		return fmt.Errorf("vault %q: kind %q is not bitwarden or passbolt", v.Name, v.Kind)
	}
	if errors.Is(err, errNeedCode) {
		return &LockedError{Vault: v, Code: true}
	}
	if err != nil {
		return fmt.Errorf("vault %q: %w", v.Name, err)
	}
	mu.Lock()
	open[Key(v)] = o
	mu.Unlock()
	return nil
}

// configured is v's password as desk.toml says, else its Passbolt CLI
// config's; "" none.
func configured(v orgs.Vault) (string, error) {
	p, err := v.Password.Get()
	if p != "" || err != nil || v.Kind != "passbolt" || v.ConfigFile == "" {
		return p, err
	}
	c, err := loadCLIConfig(v.ConfigFile)
	return c.UserPassword, err
}

// home expands a leading ~ in p.
func home(p string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		h, _ := os.UserHomeDir()
		return filepath.Join(h, rest)
	}
	return p
}
