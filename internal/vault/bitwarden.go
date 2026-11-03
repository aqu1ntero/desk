package vault

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"

	"github.com/lfdominguez/desk/internal/orgs"
)

// bitwarden is an unlocked Bitwarden or Vaultwarden account: the API token
// (renewed from the same login when it expires) and the keys that decrypt
// its items, which never leave memory.
type bitwarden struct {
	mu            sync.Mutex
	api, identity string
	login         url.Values // the token request, to repeat when the token expires
	token         string
	expires       time.Time
	user          symKey
	orgs          map[string]symKey
	priv          *rsa.PrivateKey
	ciphers       []bwCipher
	synced        time.Time
}

// symKey is a Bitwarden symmetric key: AES-256-CBC plus HMAC-SHA256.
type symKey struct{ enc, mac []byte }

type bwCipher struct {
	ID, OrganizationID, Name, Notes, Key string
	DeletedDate                          *string
	Login                                *struct{ Username, Password, Totp string }
	Fields                               []struct{ Name, Value string }
}

// endpoints are base's API and identity servers: Bitwarden's cloud has its
// own hosts, a self-hosted server (Vaultwarden) serves both under base.
func endpoints(base string) (api, identity string) {
	switch base = strings.TrimRight(base, "/"); base {
	case "", "https://vault.bitwarden.com":
		return "https://api.bitwarden.com", "https://identity.bitwarden.com"
	case "https://vault.bitwarden.eu":
		return "https://api.bitwarden.eu", "https://identity.bitwarden.eu"
	}
	return base + "/api", base + "/identity"
}

func openBitwarden(ctx context.Context, v orgs.Vault, password string) (opened, error) {
	email := strings.ToLower(strings.TrimSpace(v.Email))
	if email == "" {
		return nil, errors.New("bitwarden needs email")
	}
	b := &bitwarden{}
	b.api, b.identity = endpoints(v.URL)
	var pre struct{ Kdf, KdfIterations, KdfMemory, KdfParallelism int }
	if err := b.do(ctx, "POST", b.identity+"/accounts/prelogin", "application/json", strings.NewReader(fmt.Sprintf(`{"email":%q}`, email)), &pre); err != nil {
		return nil, err
	}
	master, err := masterKey(password, email, pre.Kdf, pre.KdfIterations, pre.KdfMemory, pre.KdfParallelism)
	if err != nil {
		return nil, err
	}
	b.login = url.Values{"scope": {"api"}, "deviceType": {"8"}, "deviceIdentifier": {deviceID(email)}, "deviceName": {"desk"}}
	if v.ClientID != "" {
		sec, err := v.ClientSecret.Get()
		if err != nil {
			return nil, fmt.Errorf("client_secret: %w", err)
		}
		b.login.Set("grant_type", "client_credentials")
		b.login.Set("client_id", v.ClientID)
		b.login.Set("client_secret", sec)
	} else {
		hash, _ := pbkdf2.Key(sha256.New, string(master), []byte(password), 1, 32)
		b.login.Set("grant_type", "password")
		b.login.Set("scope", "api offline_access") // the password grant refuses plain "api"
		b.login.Set("client_id", "desktop")
		b.login.Set("username", email)
		b.login.Set("password", base64.StdEncoding.EncodeToString(hash))
	}
	if err := b.sync(ctx, master); err != nil {
		return nil, err
	}
	return b, nil
}

// masterKey derives the account's master key with its KDF: 0 PBKDF2-SHA256,
// 1 Argon2id (memory in MiB).
func masterKey(password, email string, kdf, iter, mem, par int) ([]byte, error) {
	switch kdf {
	case 0:
		return pbkdf2.Key(sha256.New, password, []byte(email), iter, 32)
	case 1:
		salt := sha256.Sum256([]byte(email))
		return argon2.IDKey([]byte(password), salt[:], uint32(iter), uint32(mem*1024), uint8(par), 32), nil
	}
	return nil, fmt.Errorf("unknown KDF %d", kdf)
}

// deviceID is a stable device identifier for email, so the server sees one
// desk device rather than a new one each start.
func deviceID(email string) string {
	h := sha256.Sum256([]byte("desk " + email))
	h[6], h[8] = h[6]&0x0f|0x40, h[8]&0x3f|0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", h[0:4], h[4:6], h[6:8], h[8:10], h[10:16])
}

// auth gets an API token from the login.
func (b *bitwarden) auth(ctx context.Context) error {
	var t struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := b.do(ctx, "POST", b.identity+"/connect/token", "application/x-www-form-urlencoded", strings.NewReader(b.login.Encode()), &t); err != nil {
		if strings.Contains(err.Error(), "wo factor") || strings.Contains(err.Error(), "device") {
			err = fmt.Errorf("%w (set client_id and client_secret, the account's API key, to skip 2FA)", err)
		}
		return fmt.Errorf("login: %w", err)
	}
	b.token, b.expires = t.AccessToken, time.Now().Add(time.Duration(t.ExpiresIn)*time.Second-time.Minute)
	return nil
}

// sync reads the vault; the first time, with the master key, it unlocks the
// account's keys too.
func (b *bitwarden) sync(ctx context.Context, master []byte) error {
	if time.Now().After(b.expires) {
		if err := b.auth(ctx); err != nil {
			return err
		}
	}
	var s struct {
		Profile struct {
			Key, PrivateKey string
			Organizations   []struct{ ID, Key string }
		}
		Ciphers []bwCipher
	}
	if err := b.do(ctx, "GET", b.api+"/sync?excludeDomains=true", "", nil, &s); err != nil {
		return err
	}
	if master != nil {
		enc, _ := hkdf.Expand(sha256.New, master, "enc", 32)
		mac, _ := hkdf.Expand(sha256.New, master, "mac", 32)
		raw, err := symKey{enc, mac}.decrypt(s.Profile.Key)
		if err != nil {
			return errors.New("wrong master password")
		}
		if b.user, err = split(raw); err != nil {
			return err
		}
		if s.Profile.PrivateKey != "" {
			der, err := b.user.decrypt(s.Profile.PrivateKey)
			if err != nil {
				return fmt.Errorf("private key: %w", err)
			}
			k, err := x509.ParsePKCS8PrivateKey(der)
			if rk, ok := k.(*rsa.PrivateKey); err == nil && ok {
				b.priv = rk
			}
		}
	}
	b.orgs = map[string]symKey{}
	for _, o := range s.Profile.Organizations {
		if k, err := b.orgKey(o.Key); err == nil {
			b.orgs[o.ID] = k
		}
	}
	b.ciphers, b.synced = s.Ciphers, time.Now()
	return nil
}

// orgKey unwraps an organization's key, RSA-OAEP-SHA1 to the account's key
// pair ("4." EncStrings).
func (b *bitwarden) orgKey(s string) (symKey, error) {
	typ, data, _ := strings.Cut(s, ".")
	if typ != "4" || b.priv == nil {
		return symKey{}, fmt.Errorf("organization key of type %s", typ)
	}
	ct, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return symKey{}, err
	}
	raw, err := rsa.DecryptOAEP(sha1.New(), nil, b.priv, ct, nil)
	if err != nil {
		return symKey{}, err
	}
	return split(raw)
}

func (b *bitwarden) item(ctx context.Context, name, field string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if time.Since(b.synced) > 5*time.Minute {
		if err := b.sync(ctx, nil); err != nil {
			return "", err
		}
	}
	s, err := b.find(name, field)
	if errors.Is(err, errNoItem) && time.Since(b.synced) > 10*time.Second { // made since the last sync?
		if err := b.sync(ctx, nil); err != nil {
			return "", err
		}
		s, err = b.find(name, field)
	}
	return s, err
}

var errNoItem = errors.New("no such item")

// list is every item's name, username and notes (no passwords); one whose
// key doesn't open is left out. ponytail: no URIs (the sync's login uris
// aren't kept); add them to bwCipher when a search needs them.
func (b *bitwarden) list(ctx context.Context) ([]Entry, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if time.Since(b.synced) > 5*time.Minute {
		if err := b.sync(ctx, nil); err != nil {
			return nil, err
		}
	}
	var out []Entry
	for _, c := range b.ciphers {
		k, err := b.cipherKey(c)
		if err != nil {
			continue
		}
		e := Entry{ID: c.ID}
		e.Name, _ = k.str(c.Name)
		e.Notes, _ = k.str(c.Notes)
		if c.Login != nil {
			e.Username, _ = k.str(c.Login.Username)
		}
		out = append(out, e)
	}
	return out, nil
}

// cipherKey is the key that opens c's fields: its own, wrapped in the
// account's or its organization's, or that one itself.
func (b *bitwarden) cipherKey(c bwCipher) (symKey, error) {
	if c.DeletedDate != nil {
		return symKey{}, errNoItem
	}
	k, ok := b.user, true
	if c.OrganizationID != "" {
		k, ok = b.orgs[c.OrganizationID]
	}
	if !ok {
		return symKey{}, errNoItem
	}
	if c.Key == "" {
		return k, nil
	}
	raw, err := k.decrypt(c.Key)
	if err != nil {
		return symKey{}, err
	}
	return split(raw)
}

func (b *bitwarden) find(name, field string) (string, error) {
	for _, c := range b.ciphers {
		k, err := b.cipherKey(c)
		if err != nil {
			continue
		}
		if n, _ := k.str(c.Name); c.ID != name && n != name {
			continue
		}
		var login struct{ Username, Password, Totp string }
		if c.Login != nil {
			login = *c.Login
		}
		switch field {
		case "", "password":
			if c.Login == nil && field == "" { // a secure note: its text
				return k.str(c.Notes)
			}
			return k.str(login.Password)
		case "username":
			return k.str(login.Username)
		case "totp":
			return k.str(login.Totp)
		case "notes":
			return k.str(c.Notes)
		}
		for _, f := range c.Fields {
			if n, _ := k.str(f.Name); n == field {
				return k.str(f.Value)
			}
		}
		return "", fmt.Errorf("%q has no field %q", name, field)
	}
	return "", fmt.Errorf("%w %q", errNoItem, name)
}

// split makes a symKey of 64 bytes: the encryption key, then the MAC key.
func split(raw []byte) (symKey, error) {
	if len(raw) != 64 {
		return symKey{}, fmt.Errorf("a key of %d bytes", len(raw))
	}
	return symKey{raw[:32], raw[32:]}, nil
}

// decrypt opens an EncString of type 2, "2.iv|data|mac" in base64:
// AES-256-CBC, authenticated with HMAC-SHA256 over iv and data.
func (k symKey) decrypt(s string) ([]byte, error) {
	if s == "" {
		return nil, nil
	}
	typ, rest, _ := strings.Cut(s, ".")
	parts := strings.Split(rest, "|")
	if typ != "2" || len(parts) != 3 {
		return nil, fmt.Errorf("an encrypted value of type %s", typ) // ponytail: type 0 (no MAC) is only in very old accounts
	}
	var b [3][]byte
	for i, p := range parts {
		var err error
		if b[i], err = base64.StdEncoding.DecodeString(p); err != nil {
			return nil, err
		}
	}
	iv, data, mac := b[0], b[1], b[2]
	h := hmac.New(sha256.New, k.mac)
	h.Write(iv)
	h.Write(data)
	if !hmac.Equal(h.Sum(nil), mac) {
		return nil, errors.New("wrong key")
	}
	if len(iv) != aes.BlockSize || len(data) == 0 || len(data)%aes.BlockSize != 0 {
		return nil, errors.New("bad ciphertext")
	}
	blk, err := aes.NewCipher(k.enc)
	if err != nil {
		return nil, err
	}
	out := make([]byte, len(data))
	cipher.NewCBCDecrypter(blk, iv).CryptBlocks(out, data)
	n := int(out[len(out)-1])
	if n == 0 || n > aes.BlockSize || !bytes.Equal(out[len(out)-n:], bytes.Repeat([]byte{byte(n)}, n)) {
		return nil, errors.New("bad padding")
	}
	return out[:len(out)-n], nil
}

func (k symKey) str(s string) (string, error) {
	b, err := k.decrypt(s)
	return string(b), err
}

// do sends a request (with the API token, once there is one) and decodes
// the JSON reply into v.
func (b *bitwarden) do(ctx context.Context, method, u, ctype string, body io.Reader, v any) error {
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	if b.token != "" && !strings.HasPrefix(u, b.identity) {
		req.Header.Set("Authorization", "Bearer "+b.token)
	}
	if b.login.Get("grant_type") == "password" && strings.HasSuffix(u, "/connect/token") {
		req.Header.Set("Auth-Email", base64.RawURLEncoding.EncodeToString([]byte(b.login.Get("username"))))
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		var e struct {
			Message     string `json:"message"`
			Description string `json:"error_description"`
		}
		raw, _ := io.ReadAll(io.LimitReader(res.Body, 2000))
		json.Unmarshal(raw, &e)
		msg := strings.TrimSpace(e.Description + " " + e.Message)
		if msg == "" {
			msg = strings.TrimSpace(string(raw[:min(len(raw), 200)]))
		}
		return fmt.Errorf("%s: %s %s", req.URL.Host, res.Status, msg)
	}
	return json.NewDecoder(res.Body).Decode(v)
}
