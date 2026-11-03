package vault

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lfdominguez/desk/internal/orgs"
)

// fakeVault is an unlocked vault in memory: its items by "name field", or
// err for every read; reads counts item calls.
type fakeVault struct {
	items   map[string]string
	entries []Entry
	err     error
	reads   int
}

func (f *fakeVault) item(_ context.Context, name, field string) (string, error) {
	f.reads++
	if f.err != nil {
		return "", f.err
	}
	s, ok := f.items[name+" "+field]
	if !ok {
		return "", errors.New("no item")
	}
	return s, nil
}

func (f *fakeVault) list(context.Context) ([]Entry, error) { return f.entries, f.err }

// opens puts f in place as v's unlocked vault; v.URL makes its Key unique.
func opens(t *testing.T, f *fakeVault) orgs.Vault {
	v := orgs.Vault{Name: "acme", Kind: "bitwarden", URL: "https://" + t.Name() + ".example.com", Email: "me@example.com"}
	mu.Lock()
	open[Key(v)] = f
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		delete(open, Key(v))
		mu.Unlock()
	})
	return v
}

// TestGetCached: a second read within the cache's life is not asked of the
// vault.
func TestGetCached(t *testing.T) {
	f := &fakeVault{items: map[string]string{"mail ": "pw"}}
	v := opens(t, f)
	ctx := context.Background()
	for range 2 {
		if s, err := Get(ctx, v, "mail", ""); err != nil || s != "pw" {
			t.Fatalf("%q %v", s, err)
		}
	}
	if f.reads != 1 {
		t.Errorf("reads = %d, want 1 (cached)", f.reads)
	}
	if !Unlocked(v) {
		t.Error("an open vault should say unlocked")
	}
	if Unlocked(orgs.Vault{Kind: "bitwarden", URL: "https://none.example.com"}) {
		t.Error("an unknown vault should say locked")
	}
}

// TestGetErrors: a vault wanting a one-time code is a LockedError with
// Code; any other error names the vault.
func TestGetErrors(t *testing.T) {
	f := &fakeVault{err: errNeedCode}
	v := opens(t, f)
	ctx := context.Background()
	var locked *LockedError
	if _, err := Get(ctx, v, "mail", ""); !errors.As(err, &locked) || !locked.Code {
		t.Fatalf("need code: %v", err)
	}
	if _, err := List(ctx, v); !errors.As(err, &locked) || !locked.Code {
		t.Fatalf("list, need code: %v", err)
	}
	f.err = errors.New("boom")
	if _, err := Get(ctx, v, "mail", ""); err == nil || err.Error() != `vault "acme": boom` {
		t.Fatalf("get: %v", err)
	}
	if _, err := List(ctx, v); err == nil || err.Error() != `vault "acme": boom` {
		t.Fatalf("list: %v", err)
	}
}

// TestList: an open vault lists its entries; a locked one with no
// password is a LockedError, not a crash.
func TestList(t *testing.T) {
	f := &fakeVault{entries: []Entry{{ID: "1", Name: "mail"}}}
	v := opens(t, f)
	ctx := context.Background()
	if es, err := List(ctx, v); err != nil || len(es) != 1 || es[0].Name != "mail" {
		t.Fatalf("%v %v", es, err)
	}
	var locked *LockedError
	if _, err := List(ctx, orgs.Vault{Name: "globex", Kind: "bitwarden", URL: "https://locked.example.com"}); !errors.As(err, &locked) || locked.Code {
		t.Fatalf("locked: %v", err)
	}
}

// TestResolve: a secret without a vault is its token; one with a vault is
// read from the vault of that name, among several.
func TestResolve(t *testing.T) {
	f := &fakeVault{items: map[string]string{"mail username": "me"}}
	v := opens(t, f)
	v.Name = "globex"
	vaults := []orgs.Vault{{Name: "acme", Kind: "passbolt", URL: "https://other.example.com"}, v}
	ctx := context.Background()
	for _, c := range []struct {
		s    orgs.Secret
		want string
		err  string
	}{
		{orgs.Secret{Token: "literal"}, "literal", ""},
		{orgs.Secret{Vault: "globex", Item: "mail", Field: "username"}, "me", ""},
		{orgs.Secret{Vault: "initech", Item: "mail"}, "", `no [[vault]] named "initech"`},
	} {
		got, err := Resolve(ctx, vaults, c.s)
		if got != c.want || (err == nil) != (c.err == "") || err != nil && !strings.Contains(err.Error(), c.err) {
			t.Errorf("%+v: %q %v", c.s, got, err)
		}
	}
}

// TestLockedErrorText: a locked vault and one needing a code say which.
func TestLockedErrorText(t *testing.T) {
	v := orgs.Vault{Name: "acme"}
	if s := (&LockedError{Vault: v}).Error(); s != `vault "acme" is locked` {
		t.Error(s)
	}
	if s := (&LockedError{Vault: v, Code: true}).Error(); s != `vault "acme" needs a one-time code` {
		t.Error(s)
	}
}

// TestUnlockErrors: no item, a password source that fails and an unknown
// kind are errors that name the vault.
func TestUnlockErrors(t *testing.T) {
	ctx := context.Background()
	if _, err := Get(ctx, orgs.Vault{Name: "acme"}, "", ""); err == nil || !strings.Contains(err.Error(), "which item") {
		t.Errorf("no item: %v", err)
	}
	bad := orgs.Vault{Name: "acme", Kind: "bitwarden", Password: orgs.Secret{TokenEnv: "DESK_TEST_UNSET_PASSWORD"}}
	if err := Unlock(ctx, bad, "", ""); err == nil || !strings.HasPrefix(err.Error(), `vault "acme" password:`) {
		t.Errorf("password source: %v", err)
	}
	if err := Unlock(ctx, orgs.Vault{Name: "acme", Kind: "keepass"}, "pw", ""); err == nil || !strings.Contains(err.Error(), `kind "keepass"`) {
		t.Errorf("unknown kind: %v", err)
	}
}

// TestUnlockOnRead: Get and List of a locked vault with a configured
// password unlock it themselves, then read.
func TestUnlockOnRead(t *testing.T) {
	key, armored := pbKey(t, "secret")
	keyFile := filepath.Join(t.TempDir(), "key.asc")
	os.WriteFile(keyFile, []byte(armored), 0o600)
	ctx := context.Background()
	vault := func() orgs.Vault {
		return orgs.Vault{Name: "globex", Kind: "passbolt", URL: newFakePassbolt(t, key, false).URL, KeyFile: keyFile, Password: orgs.Secret{Token: "secret"}}
	}
	v := vault()
	if s, err := Get(ctx, v, "Globex VPN", ""); err != nil || s != "pw2" || !Unlocked(v) {
		t.Errorf("get: %q %v", s, err)
	}
	v = vault()
	if es, err := List(ctx, v); err != nil || len(es) != 5 || !Unlocked(v) {
		t.Errorf("list: %v %v", es, err)
	}
}

// TestHome: a leading ~/ is the home folder; other paths stay.
func TestHome(t *testing.T) {
	h, _ := os.UserHomeDir()
	if got := home("~/a/b"); got != filepath.Join(h, "a/b") {
		t.Error(got)
	}
	if got := home("/a/~/b"); got != "/a/~/b" {
		t.Error(got)
	}
}

// TestConfigured: only a passbolt vault with a config file falls back to
// it, and a password source's error is reported, not skipped.
func TestConfigured(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(path, []byte("userpassword = 'from config'\n"), 0o600)
	for _, c := range []struct {
		v    orgs.Vault
		want string
		err  bool
	}{
		{orgs.Vault{Kind: "passbolt", ConfigFile: path}, "from config", false},
		{orgs.Vault{Kind: "bitwarden", ConfigFile: path}, "", false},
		{orgs.Vault{Kind: "passbolt"}, "", false},
		{orgs.Vault{Kind: "passbolt", ConfigFile: path, Password: orgs.Secret{TokenEnv: "DESK_TEST_UNSET_PASSWORD"}}, "", true},
	} {
		got, err := configured(c.v)
		if got != c.want || (err != nil) != c.err {
			t.Errorf("%+v: %q %v", c.v, got, err)
		}
	}
}
