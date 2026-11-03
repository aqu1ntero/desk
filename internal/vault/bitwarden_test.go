package vault

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/argon2"

	"github.com/lfdominguez/desk/internal/orgs"
)

// encrypt makes a type 2 EncString, as Bitwarden clients do.
func (k symKey) encrypt(plain []byte) string {
	n := aes.BlockSize - len(plain)%aes.BlockSize
	data := append(append([]byte{}, plain...), strings.Repeat(string(rune(n)), n)...)
	iv := make([]byte, aes.BlockSize)
	rand.Read(iv)
	blk, _ := aes.NewCipher(k.enc)
	cipher.NewCBCEncrypter(blk, iv).CryptBlocks(data, data)
	h := hmac.New(sha256.New, k.mac)
	h.Write(iv)
	h.Write(data)
	b := base64.StdEncoding.EncodeToString
	return "2." + b(iv) + "|" + b(data) + "|" + b(h.Sum(nil))
}

func randKey() (symKey, []byte) {
	raw := make([]byte, 64)
	rand.Read(raw)
	k, _ := split(raw)
	return k, raw
}

// TestBitwarden unlocks a fake Vaultwarden account and reads a personal
// login, a custom field, a secure note and an organization's item that has
// its own key; a deleted item of the same name is skipped.
func TestBitwarden(t *testing.T) {
	const email, password, iter = "me@x.com", "hunter2", 5000
	master, _ := pbkdf2.Key(sha256.New, password, []byte(email), iter, 32)
	hash, _ := pbkdf2.Key(sha256.New, string(master), []byte(password), 1, 32)
	enc, _ := hkdf.Expand(sha256.New, master, "enc", 32)
	mac, _ := hkdf.Expand(sha256.New, master, "mac", 32)
	user, userRaw := randKey()
	priv, _ := rsa.GenerateKey(rand.Reader, 2048)
	der, _ := x509.MarshalPKCS8PrivateKey(priv)
	org, orgRaw := randKey()
	wrapped, _ := rsa.EncryptOAEP(sha1.New(), rand.Reader, &priv.PublicKey, orgRaw, nil)
	item, itemRaw := randKey()
	s := func(k symKey, v string) string { return k.encrypt([]byte(v)) }
	deleted := "2024-01-01T00:00:00Z"
	sync := map[string]any{
		"profile": map[string]any{
			"key": symKey{enc, mac}.encrypt(userRaw), "privateKey": user.encrypt(der),
			"organizations": []map[string]string{{"id": "o1", "key": "4." + base64.StdEncoding.EncodeToString(wrapped)}},
		},
		"ciphers": []map[string]any{
			{"id": "c0", "name": s(user, "GitHub token"), "login": map[string]string{"password": s(user, "old")}, "deletedDate": deleted},
			{"id": "c1", "name": s(user, "GitHub token"), "login": map[string]string{"username": s(user, "me"), "password": s(user, "ghp_1")},
				"fields": []map[string]string{{"name": s(user, "api"), "value": s(user, "f1")}}},
			{"id": "c2", "name": s(user, "note"), "notes": s(user, "the note")},
			{"id": "c3", "organizationId": "o1", "key": org.encrypt(itemRaw), "name": s(item, "Jira"), "login": map[string]string{"password": s(item, "jt")}},
		},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		switch {
		case r.URL.Path == "/identity/accounts/prelogin":
			json.NewEncoder(w).Encode(map[string]int{"kdf": 0, "kdfIterations": iter})
		case r.URL.Path == "/identity/connect/token" && r.Form.Get("username") == email && r.Form.Get("password") == base64.StdEncoding.EncodeToString(hash):
			json.NewEncoder(w).Encode(map[string]any{"access_token": "T", "expires_in": 3600})
		case r.URL.Path == "/identity/connect/token":
			http.Error(w, `{"error":"invalid_grant","error_description":"Username or password is incorrect. Try again"}`, 400)
		case r.URL.Path == "/api/sync" && r.Header.Get("Authorization") == "Bearer T":
			json.NewEncoder(w).Encode(sync)
		default:
			http.Error(w, "unexpected "+r.URL.String(), 404)
		}
	}))
	defer srv.Close()
	ctx := context.Background()
	v := orgs.Vault{Name: "me", Kind: "vaultwarden", URL: srv.URL, Email: "Me@X.com"}

	var locked *LockedError
	if _, err := Get(ctx, v, "GitHub token", ""); !errors.As(err, &locked) {
		t.Fatalf("no password yet: want locked, got %v", err)
	}
	if err := Unlock(ctx, v, "wrong", ""); err == nil || !strings.Contains(err.Error(), "incorrect") {
		t.Fatalf("wrong password: %v", err)
	}
	if err := Unlock(ctx, v, password, ""); err != nil {
		t.Fatal(err)
	}
	for _, c := range [][3]string{
		{"GitHub token", "", "ghp_1"}, {"GitHub token", "username", "me"}, {"c1", "api", "f1"},
		{"note", "", "the note"}, {"Jira", "password", "jt"},
	} {
		if got, err := Get(ctx, v, c[0], c[1]); err != nil || got != c[2] {
			t.Errorf("%s %s: %q %v", c[0], c[1], got, err)
		}
	}
	if _, err := Get(ctx, v, "nope", ""); err == nil || !strings.Contains(err.Error(), `no such item "nope"`) {
		t.Errorf("missing item: %v", err)
	}
}

// fakeBW is a Vaultwarden that counts syncs, serves ciphers (changeable
// between syncs), fails its sync while failSync, and keeps the last
// request's headers per path.
type fakeBW struct {
	*httptest.Server
	mu       sync.Mutex
	syncs    int
	failSync bool
	ciphers  []map[string]any
	profile  map[string]any // the sync's profile: wrapped keys, organizations
	token    string         // the token endpoint's error_description, "" a token
	headers  map[string]http.Header
}

func newFakeBW(t *testing.T) *fakeBW {
	f := &fakeBW{headers: map[string]http.Header{}}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.headers[r.URL.Path] = r.Header.Clone()
		switch r.URL.Path {
		case "/identity/connect/token":
			if f.token != "" {
				w.WriteHeader(400)
				json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant", "error_description": f.token})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"access_token": "T", "expires_in": 3600})
		case "/api/sync":
			f.syncs++
			if f.failSync {
				http.Error(w, "down", 500)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"ciphers": f.ciphers, "profile": f.profile})
		default:
			http.Error(w, "unexpected", 404)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeBW) set(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn()
}

// client is an unlocked account on f with user key k, logged in by
// password, synced once.
func (f *fakeBW) client(t *testing.T, k symKey) *bitwarden {
	b := &bitwarden{api: f.URL + "/api", identity: f.URL + "/identity", user: k,
		login: url.Values{"grant_type": {"password"}, "username": {"me@example.com"}}}
	if err := b.sync(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	return b
}

// TestBitwardenResync: items are read from the last sync while it is
// fresh, a stale one syncs again, and an item not found syncs again (it
// may be new) unless the sync is seconds old; failed syncs are errors.
func TestBitwardenResync(t *testing.T) {
	k, _ := randKey()
	f := newFakeBW(t)
	f.ciphers = []map[string]any{{"id": "c1", "name": k.encrypt([]byte("mail")), "login": map[string]string{"password": k.encrypt([]byte("pw"))}}}
	b := f.client(t, k)
	ctx := context.Background()
	syncs := func() int { f.mu.Lock(); defer f.mu.Unlock(); return f.syncs }

	if s, err := b.item(ctx, "mail", ""); err != nil || s != "pw" || syncs() != 1 {
		t.Fatalf("fresh: %q %v, %d syncs", s, err, syncs())
	}
	if _, err := b.item(ctx, "vpn", ""); !errors.Is(err, errNoItem) || syncs() != 1 {
		t.Fatalf("missing right after a sync: %v, %d syncs", err, syncs())
	}
	f.set(func() {
		f.ciphers = append(f.ciphers, map[string]any{"id": "c2", "name": k.encrypt([]byte("vpn")), "login": map[string]string{"password": k.encrypt([]byte("v"))}})
	})
	b.synced = time.Now().Add(-time.Minute)
	if s, err := b.item(ctx, "vpn", ""); err != nil || s != "v" || syncs() != 2 {
		t.Fatalf("new item: %q %v, %d syncs", s, err, syncs())
	}
	b.synced = time.Now().Add(-time.Minute)
	if _, err := b.item(ctx, "mail", ""); err != nil || syncs() != 2 {
		t.Fatalf("found item shouldn't sync: %v, %d syncs", err, syncs())
	}
	b.synced = time.Now().Add(-6 * time.Minute)
	if _, err := b.item(ctx, "mail", ""); err != nil || syncs() != 3 {
		t.Fatalf("stale: %v, %d syncs", err, syncs())
	}
	if es, err := b.list(ctx); err != nil || len(es) != 2 || syncs() != 3 {
		t.Fatalf("list fresh: %v %v, %d syncs", es, err, syncs())
	}

	f.set(func() { f.failSync = true })
	b.synced = time.Now().Add(-time.Minute)
	if _, err := b.item(ctx, "gone", ""); err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("failed resync for a missing item: %v", err)
	}
	b.synced = time.Now().Add(-6 * time.Minute)
	if _, err := b.item(ctx, "mail", ""); err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("failed stale sync: %v", err)
	}
	if _, err := b.list(ctx); err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("failed stale sync, list: %v", err)
	}
	f.set(func() { f.failSync = false })
	if _, err := b.list(ctx); err != nil {
		t.Errorf("stale list should sync: %v", err)
	}
}

// TestBitwardenList: a list has names, notes and usernames, a secure note
// (no login) too; deleted items and ones whose key doesn't open are left out.
func TestBitwardenList(t *testing.T) {
	k, _ := randKey()
	other, _ := randKey()
	s := func(v string) string { return k.encrypt([]byte(v)) }
	f := newFakeBW(t)
	f.ciphers = []map[string]any{
		{"id": "c1", "name": s("mail"), "notes": s("n"), "login": map[string]string{"username": s("me")}},
		{"id": "c2", "name": s("note"), "notes": s("text")},
		{"id": "c3", "name": s("old"), "deletedDate": "2024-01-01T00:00:00Z"},
		{"id": "c4", "name": s("foreign"), "key": other.encrypt(make([]byte, 64))},
		{"id": "c5", "name": s("org"), "organizationId": "o9"},
	}
	es, err := f.client(t, k).list(context.Background())
	want := []Entry{{ID: "c1", Name: "mail", Username: "me", Notes: "n"}, {ID: "c2", Name: "note", Notes: "text"}}
	if err != nil || len(es) != len(want) || es[0] != want[0] || es[1] != want[1] {
		t.Fatalf("%+v %v", es, err)
	}
}

// TestBitwardenRequests: the API token goes to the API, never to the
// identity server; Auth-Email goes only with a password grant's token
// request.
func TestBitwardenRequests(t *testing.T) {
	k, _ := randKey()
	f := newFakeBW(t)
	b := &bitwarden{api: f.URL + "/api", identity: f.URL + "/identity", user: k,
		login: url.Values{"grant_type": {"password"}, "username": {"me@example.com"}}}
	ctx := context.Background()
	if err := b.sync(ctx, nil); err != nil {
		t.Fatal(err)
	}
	b.expires = time.Time{}
	if err := b.sync(ctx, nil); err != nil {
		t.Fatal(err)
	}
	tok, sync := f.headers["/identity/connect/token"], f.headers["/api/sync"]
	if tok.Get("Authorization") != "" {
		t.Errorf("token request carries %q", tok.Get("Authorization"))
	}
	if want := base64.RawURLEncoding.EncodeToString([]byte("me@example.com")); tok.Get("Auth-Email") != want {
		t.Errorf("Auth-Email %q, want %q", tok.Get("Auth-Email"), want)
	}
	if sync.Get("Authorization") != "Bearer T" || sync.Get("Auth-Email") != "" {
		t.Errorf("sync headers: %v", sync)
	}
	if !b.expires.Before(time.Now().Add(time.Hour)) || !b.expires.After(time.Now().Add(50*time.Minute)) {
		t.Errorf("expires %v: want a minute before the token's hour", time.Until(b.expires))
	}

	b.token = ""
	if err := b.do(ctx, "GET", b.api+"/sync", "", nil, &struct{}{}); err != nil {
		t.Fatal(err)
	}
	if h := f.headers["/api/sync"].Get("Authorization"); h != "" {
		t.Errorf("no token yet, but Authorization %q", h)
	}
}

// TestBitwardenTwoFactor: a login refused for 2FA or a new device says to
// use the API key; other refusals don't.
func TestBitwardenTwoFactor(t *testing.T) {
	f := newFakeBW(t)
	b := &bitwarden{identity: f.URL + "/identity", login: url.Values{}}
	for _, c := range []struct {
		desc string
		hint bool
	}{
		{"Two factor required.", true},
		{"New device verification required.", true},
		{"Username or password is incorrect.", false},
	} {
		f.set(func() { f.token = c.desc })
		err := b.auth(context.Background())
		if err == nil || strings.Contains(err.Error(), "client_secret") != c.hint {
			t.Errorf("%s: %v", c.desc, err)
		}
	}
}

// TestBitwardenClientSecret: an API key whose secret can't be read fails
// the unlock, naming client_secret.
func TestBitwardenClientSecret(t *testing.T) {
	f := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]int{"kdf": 0, "kdfIterations": 1})
	}))
	defer f.Close()
	v := orgs.Vault{Name: "acme", Kind: "vaultwarden", URL: f.URL, Email: "me@example.com",
		ClientID: "user.x", ClientSecret: orgs.Secret{TokenEnv: "DESK_TEST_UNSET_SECRET"}}
	if _, err := openBitwarden(context.Background(), v, "pw"); err == nil || !strings.HasPrefix(err.Error(), "client_secret:") {
		t.Fatalf("%v", err)
	}
}

// TestOrgKeyNeedsPrivateKey: an organization key without the account's
// private key is an error, not a crash.
func TestOrgKeyNeedsPrivateKey(t *testing.T) {
	if _, err := (&bitwarden{}).orgKey("4." + base64.StdEncoding.EncodeToString([]byte("x"))); err == nil {
		t.Fatal("want an error")
	}
}

// encRaw is a type 2 EncString of data as is (no padding added), MACed
// with k, for malformed ciphertexts.
func encRaw(k symKey, iv, data []byte) string {
	if len(iv) == aes.BlockSize && len(data) > 0 && len(data)%aes.BlockSize == 0 {
		blk, _ := aes.NewCipher(k.enc)
		data = append([]byte{}, data...)
		cipher.NewCBCEncrypter(blk, iv).CryptBlocks(data, data)
	}
	h := hmac.New(sha256.New, k.mac)
	h.Write(iv)
	h.Write(data)
	b := base64.StdEncoding.EncodeToString
	return "2." + b(iv) + "|" + b(data) + "|" + b(h.Sum(nil))
}

// TestDecryptRejects: EncStrings of another type or shape, a bad MAC, a
// bad IV or length and bad padding are errors, not crashes or garbage.
func TestDecryptRejects(t *testing.T) {
	k, _ := randKey()
	other, _ := randKey()
	iv := make([]byte, aes.BlockSize)
	block := func(last ...byte) []byte {
		b := bytes.Repeat([]byte{'a'}, aes.BlockSize)
		copy(b[len(b)-len(last):], last)
		return b
	}
	b := base64.StdEncoding.EncodeToString
	for _, c := range []struct{ name, s, err string }{
		{"type 0", "0." + b(iv) + "|" + b(block()) + "|" + b(iv), "type 0"},
		{"two parts", "2." + b(iv) + "|" + b(block()), "type 2"},
		{"other key", other.encrypt([]byte("x")), "wrong key"},
		{"short iv", encRaw(k, iv[:8], block()), "bad ciphertext"},
		{"no data", encRaw(k, iv, nil), "bad ciphertext"},
		{"partial block", encRaw(k, iv, block()[:15]), "bad ciphertext"},
		{"pad 0", encRaw(k, iv, block(0)), "bad padding"},
		{"pad 17", encRaw(k, iv, block(17)), "bad padding"},
		{"pad mismatch", encRaw(k, iv, block(1, 2)), "bad padding"},
	} {
		if out, err := k.decrypt(c.s); err == nil || !strings.Contains(err.Error(), c.err) {
			t.Errorf("%s: %q %v", c.name, out, err)
		}
	}
	if out, err := k.decrypt(encRaw(k, iv, block(2, 2))); err != nil || string(out) != strings.Repeat("a", 14) {
		t.Errorf("good padding: %q %v", out, err)
	}
	if _, err := k.decrypt("2.!!|" + b(block()) + "|" + b(iv)); err == nil {
		t.Error("bad base64: want an error")
	}
	if _, err := (symKey{enc: []byte("short"), mac: k.mac}).decrypt(encRaw(k, iv, block(2, 2))); err == nil || !strings.Contains(err.Error(), "key size") {
		t.Errorf("bad AES key: %v", err)
	}
}

// TestEndpoints: Bitwarden's US and EU clouds have their own API and
// identity hosts; any other base serves both under itself.
func TestEndpoints(t *testing.T) {
	for _, c := range [][3]string{
		{"", "https://api.bitwarden.com", "https://identity.bitwarden.com"},
		{"https://vault.bitwarden.com/", "https://api.bitwarden.com", "https://identity.bitwarden.com"},
		{"https://vault.bitwarden.eu", "https://api.bitwarden.eu", "https://identity.bitwarden.eu"},
		{"https://vw.example.com/", "https://vw.example.com/api", "https://vw.example.com/identity"},
	} {
		if api, id := endpoints(c[0]); api != c[1] || id != c[2] {
			t.Errorf("%q: %s %s", c[0], api, id)
		}
	}
}

// TestMasterKey: KDF 1 is Argon2id salted with the email's SHA-256 (memory
// in MiB), unlike PBKDF2's; an unknown KDF is an error.
func TestMasterKey(t *testing.T) {
	salt := sha256.Sum256([]byte("me@example.com"))
	want := argon2.IDKey([]byte("pw"), salt[:], 2, 1024, 1, 32)
	got, err := masterKey("pw", "me@example.com", 1, 2, 1, 1)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("argon2: %x %v", got, err)
	}
	if p, _ := masterKey("pw", "me@example.com", 0, 2, 1, 1); bytes.Equal(p, got) {
		t.Error("PBKDF2 and Argon2 gave the same key")
	}
	if _, err := masterKey("pw", "me@example.com", 2, 1, 1, 1); err == nil || !strings.Contains(err.Error(), "unknown KDF 2") {
		t.Errorf("unknown KDF: %v", err)
	}
}

// TestOpenBitwardenErrors: no email, an unreachable server, an unknown KDF
// fail the unlock; an API key logs in by client credentials.
func TestOpenBitwardenErrors(t *testing.T) {
	ctx := context.Background()
	if _, err := openBitwarden(ctx, orgs.Vault{Email: " "}, "pw"); err == nil || !strings.Contains(err.Error(), "needs email") {
		t.Errorf("no email: %v", err)
	}
	down := httptest.NewServer(http.NotFoundHandler())
	down.Close()
	if _, err := openBitwarden(ctx, orgs.Vault{URL: down.URL, Email: "me@example.com"}, "pw"); err == nil {
		t.Error("unreachable server: want an error")
	}

	var kdf int
	var form url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/identity/accounts/prelogin" {
			json.NewEncoder(w).Encode(map[string]int{"kdf": kdf, "kdfIterations": 1})
			return
		}
		r.ParseForm()
		form = r.PostForm
		http.Error(w, `{"error_description":"nope"}`, 400)
	}))
	defer srv.Close()
	kdf = 9
	if _, err := openBitwarden(ctx, orgs.Vault{URL: srv.URL, Email: "me@example.com"}, "pw"); err == nil || !strings.Contains(err.Error(), "unknown KDF 9") {
		t.Errorf("unknown KDF: %v", err)
	}
	kdf = 0
	t.Setenv("DESK_TEST_BW_SECRET", "s3cret")
	v := orgs.Vault{URL: srv.URL, Email: "me@example.com", ClientID: "user.acme", ClientSecret: orgs.Secret{TokenEnv: "DESK_TEST_BW_SECRET"}}
	if _, err := openBitwarden(ctx, v, "pw"); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("token refused: %v", err)
	}
	if form.Get("grant_type") != "client_credentials" || form.Get("client_id") != "user.acme" || form.Get("client_secret") != "s3cret" || form.Get("password") != "" {
		t.Errorf("client credentials login: %v", form)
	}
}

// TestBitwardenUnlockKeys: the account's key not opening with the master
// key is a wrong password; a user key of the wrong size or a private key
// that doesn't open are errors.
func TestBitwardenUnlockKeys(t *testing.T) {
	master := make([]byte, 32)
	rand.Read(master)
	enc, _ := hkdf.Expand(sha256.New, master, "enc", 32)
	mac, _ := hkdf.Expand(sha256.New, master, "mac", 32)
	mk := symKey{enc, mac}
	other, _ := randKey()
	_, userRaw := randKey()
	for _, c := range []struct {
		profile map[string]any
		err     string
	}{
		{map[string]any{"key": other.encrypt(userRaw)}, "wrong master password"},
		{map[string]any{"key": mk.encrypt(userRaw[:32])}, "a key of 32 bytes"},
		{map[string]any{"key": mk.encrypt(userRaw), "privateKey": other.encrypt([]byte("der"))}, "private key:"},
	} {
		f := newFakeBW(t)
		f.profile = c.profile
		b := &bitwarden{api: f.URL + "/api", identity: f.URL + "/identity", login: url.Values{}}
		if err := b.sync(context.Background(), master); err == nil || !strings.Contains(err.Error(), c.err) {
			t.Errorf("%s: %v", c.err, err)
		}
	}
}

// TestOrgKeyErrors: an organization key that isn't base64 or doesn't
// decrypt with the account's private key is an error.
func TestOrgKeyErrors(t *testing.T) {
	priv, _ := rsa.GenerateKey(rand.Reader, 1024)
	b := &bitwarden{priv: priv}
	for _, s := range []string{"4.!!", "4." + base64.StdEncoding.EncodeToString([]byte("not oaep"))} {
		if k, err := b.orgKey(s); err == nil || k.enc != nil {
			t.Errorf("%q: %v", s, err)
		}
	}
}

// TestBitwardenFields: totp and notes are fields of their own; a field the
// item doesn't have names it.
func TestBitwardenFields(t *testing.T) {
	k, _ := randKey()
	s := func(v string) string { return k.encrypt([]byte(v)) }
	f := newFakeBW(t)
	f.ciphers = []map[string]any{{"id": "c1", "name": s("mail"), "notes": s("n"), "login": map[string]string{"totp": s("otpauth://x")}}}
	b := f.client(t, k)
	for field, want := range map[string]string{"totp": "otpauth://x", "notes": "n"} {
		if got, err := b.find("mail", field); err != nil || got != want {
			t.Errorf("%s: %q %v", field, got, err)
		}
	}
	if _, err := b.find("mail", "pin"); err == nil || err.Error() != `"mail" has no field "pin"` {
		t.Errorf("missing field: %v", err)
	}
}

// TestDoBadRequest: a request that can't be built is an error.
func TestDoBadRequest(t *testing.T) {
	if err := (&bitwarden{}).do(context.Background(), "GET", "://bad", "", nil, nil); err == nil {
		t.Error("want an error")
	}
}
