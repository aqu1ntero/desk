package sshc

import (
	"bytes"
	"crypto"
	_ "crypto/sha1" // FingerprintHash sha1
	_ "crypto/sha512"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/lfdominguez/desk/internal/sshconfig"
)

// ErrHostKey is a host key ssh would refuse; the error wrapping it says why.
var ErrHostKey = errors.New("Host key verification failed")

// isLocal is sshconnect.c's sockaddr_is_local (CheckHostIP and
// NoHostAuthenticationForLocalhost leave loopback alone); tests replace it.
var isLocal = func(ip net.IP) bool { return ip.IsLoopback() }

const (
	stNew = iota
	stOK
	stChanged
	stRevoked
)

// hostCheck is sshconnect.c's check_host_key for one connection.
type hostCheck struct {
	cfg   *sshconfig.Config
	o     Options
	host  string // what known_hosts is searched for: HostKeyAlias, else host or [host]:port
	ip    string // the address searched for with CheckHostIP, "" when off
	shown string // the address as questions show it
	alias bool
	local bool
	user  []string
	db    []known
	clear func()

	// unsafe is a changed (or revoked) key taken under
	// StrictHostKeyChecking=no: no passwords, agent or forwards then.
	unsafe bool
}

func hostPort(h string, port int) string {
	if port == 22 {
		return h
	}
	return "[" + h + "]:" + strconv.Itoa(port)
}

func newHostCheck(cfg *sshconfig.Config, o Options, ip net.IP) *hostCheck {
	h := &hostCheck{cfg: cfg, o: o, user: cfg.UserKnownHostsFiles(), clear: func() {}}
	port := cfg.Port()
	h.host = hostPort(cfg.HostName(), port)
	if a := cfg.Get("hostkeyalias"); a != "" {
		h.host, h.alias = a, true
	}
	h.shown = "<no hostip for proxy command>"
	if ip != nil {
		h.local = isLocal(ip)
		h.shown = hostPort(ip.String(), port)
		if cfg.Bool("checkhostip") && !h.local && ip.String() != cfg.HostName() {
			h.ip = h.shown
		}
	}
	h.db = readKnown(slices.Concat(h.user, cfg.GlobalKnownHostsFiles()), o.Logf)
	return h
}

// underlying is a certificate algorithm's signature algorithm, others as they are.
func underlying(alg string) string {
	if c, ok := strings.CutSuffix(alg, "-cert-v01@openssh.com"); ok {
		if strings.HasPrefix(c, "sk-") {
			c += "@openssh.com"
		}
		return c
	}
	return alg
}

// keyTypeOf is the plain key type an algorithm signs with.
func keyTypeOf(alg string) string {
	if a := underlying(alg); !strings.HasPrefix(a, "rsa-sha2-") {
		return a
	}
	return ssh.KeyAlgoRSA
}

// order is sshconnect2.c's order_hostkeyalgs: the algorithms of the key
// types known for the host first (certificates when a CA is), so a host
// known by its ECDSA key isn't asked for its Ed25519 one.
func (h *hostCheck) order(algs []string) []string {
	var first, rest []string
	for _, a := range algs {
		cert := strings.HasSuffix(a, "-cert-v01@openssh.com")
		want := false
		for _, k := range h.db {
			if matchHosts(k.hosts, h.host) && (cert && k.marker == "cert-authority" ||
				!cert && k.marker == "" && k.key.Type() == keyTypeOf(a)) {
				want = true
			}
		}
		if want {
			first = append(first, a)
		} else {
			rest = append(rest, a)
		}
	}
	return append(first, rest...)
}

// status is hostfile.c's check_key_in_hostkeys over db's lines for name:
// revoked over known over changed over new; found is the lines it read so.
func status(db []known, name string, key ssh.PublicKey) (int, []known) {
	cert, isCert := key.(*ssh.Certificate)
	revoked := false
	var ok, changed []known
	for _, k := range db {
		if !matchHosts(k.hosts, name) {
			continue
		}
		switch {
		case k.marker == "revoked":
			revoked = revoked || keyEqual(k.key, key) || isCert && (keyEqual(k.key, cert.Key) || keyEqual(k.key, cert.SignatureKey))
		case isCert:
			if k.marker == "cert-authority" && keyEqual(k.key, cert.SignatureKey) {
				ok = append(ok, k)
			}
		case k.marker != "":
		case keyEqual(k.key, key):
			ok = append(ok, k)
		default:
			changed = append(changed, k)
		}
	}
	switch {
	case revoked:
		return stRevoked, nil
	case ok != nil:
		return stOK, ok
	case changed != nil:
		return stChanged, changed
	}
	return stNew, nil
}

// check is the host key callback.
func (h *hostCheck) check(key ssh.PublicKey) error {
	cfg := h.cfg
	if h.local && !h.alias && cfg.Bool("nohostauthenticationforlocalhost") {
		h.o.Logf("Forcing accepting of host key for loopback/localhost.")
		return nil
	}
	if err := h.revokedByFile(key); err != nil {
		return err
	}
	db := append(slices.Clone(h.db), h.command("HOSTNAME", h.host, key)...)
	if cert, ok := key.(*ssh.Certificate); ok {
		st, _ := status(db, h.host, cert)
		if st == stRevoked {
			return h.revoked(cert)
		}
		if st == stOK {
			err := h.certOK(cert)
			if err == nil {
				return nil
			}
			h.o.Logf("%v", err)
		}
		h.o.Logf("No matching CA found. Retry with plain key")
		key = cert.Key
	}
	kind := keyKind(key.Type())
	hs, found := status(db, h.host, key)
	is, ifound := stNew, []known(nil)
	if h.ip != "" {
		is, ifound = status(append(slices.Clone(h.db), h.command("ADDRESS", h.ip, key)...), h.ip, key)
	}
	strict := cfg.Get("stricthostkeychecking")
	switch hs {
	case stOK:
		if h.ip != "" && is == stNew {
			h.add([]string{h.ip}, key, "the "+kind+" host key for IP address '"+h.ip+"'")
		}
	case stNew:
		if !h.alias && cfg.Port() != 22 {
			if s, _ := status(h.db, cfg.HostName(), key); s == stOK {
				break // known without the port
			}
		}
		if err := h.newKey(key, kind, strict, is); err != nil {
			return err
		}
	case stRevoked:
		if err := h.revoked(key); err != nil {
			return err
		}
	case stChanged:
		e := &HostKeyChangedError{Host: h.host, Type: kind, Fingerprint: h.fingerprint(key), Key: key, Known: h.lines(found, h.host)}
		if is == stChanged {
			e.Known = append(e.Known, h.lines(ifound, h.ip)...)
		}
		if len(h.user) > 0 {
			e.file, e.hash = h.user[0], cfg.Bool("hashknownhosts")
			e.names = []string{h.host}
			if h.ip != "" && is != stOK {
				e.names = append(e.names, h.ip)
			}
		}
		if strict != "false" {
			return e
		}
		h.o.Logf("%v", e)
		h.goUnsafe()
	}
	if h.ip != "" && hs != stChanged && is == stChanged {
		msg := fmt.Sprintf("Warning: the %s host key for '%s' differs from the key for the IP address '%s'\nOffending key for IP in %s:%d",
			kind, h.host, h.ip, ifound[0].file, ifound[0].line)
		switch strict {
		case "ask":
			if ok, err := h.confirm(msg+"\nAre you sure you want to continue connecting (yes/no)? ", ""); !ok {
				return fmt.Errorf("%w: %s", ErrHostKey, refusal(err))
			}
		case "false":
			h.o.Logf("%s", msg)
		default:
			return fmt.Errorf("%w: %s\nExiting, you have requested strict checking.", ErrHostKey, msg)
		}
	}
	return nil
}

func refusal(err error) string {
	if err != nil {
		return err.Error()
	}
	return "not accepted"
}

// newKey is check_host_key's HOST_NEW: refused, asked or taken per
// StrictHostKeyChecking, then recorded (with the address, when new too).
func (h *hostCheck) newKey(key ssh.PublicKey, kind, strict string, ipStatus int) error {
	if len(h.user) == 0 || strings.ContainsAny(h.cfg.HostName(), "@?*#[]|'\"\\") {
		return fmt.Errorf("%w: no %s host key is known for %s, and it can't be recorded", ErrHostKey, kind, h.host)
	}
	switch strict {
	case "true":
		return fmt.Errorf("%w: No %s host key is known for %s and you have requested strict checking.", ErrHostKey, kind, h.host)
	case "ask":
		fp := h.fingerprint(key)
		msg := fmt.Sprintf("The authenticity of host '%s (%s)' can't be established.\n%s key fingerprint is: %s\nAre you sure you want to continue connecting (yes/no/[fingerprint])? ",
			h.host, h.shown, kind, fp)
		if ok, err := h.confirm(msg, fp); !ok {
			return fmt.Errorf("%w: %s", ErrHostKey, refusal(err))
		}
	}
	names := []string{h.host}
	if h.ip != "" && ipStatus == stNew {
		names = append(names, h.ip)
	}
	h.add(names, key, "'"+strings.Join(names, ",")+"' ("+kind+")")
	return nil
}

// add records key under names in the first UserKnownHostsFile.
func (h *hostCheck) add(names []string, key ssh.PublicKey, what string) {
	if len(h.user) == 0 {
		return
	}
	if err := addKnown(h.user[0], names, key, h.cfg.Bool("hashknownhosts")); err != nil {
		h.o.Logf("Failed to add the host to the list of known hosts (%s): %v", h.user[0], err)
		return
	}
	h.o.Logf("Warning: Permanently added %s to the list of known hosts.", what)
}

// confirm is sshconnect.c's confirm: yes, the fingerprint, or no (an
// empty answer is no); anything else asks again.
func (h *hostCheck) confirm(msg, fp string) (bool, error) {
	for {
		a, err := h.o.Prompt(msg, true)
		if err != nil {
			return false, err
		}
		a = strings.TrimRight(a, "\r\n")
		switch {
		case a == "" || strings.EqualFold(a, "no"):
			return false, nil
		case strings.EqualFold(a, "yes") || fp != "" && a == fp:
			return true, nil
		}
		msg = "Please type 'yes' or 'no': "
		if fp != "" {
			msg = "Please type 'yes', 'no' or the fingerprint: "
		}
	}
}

// revoked is a key @revoked for the host: refused, unless
// StrictHostKeyChecking is off, then taken unsafely.
func (h *hostCheck) revoked(key ssh.PublicKey) error {
	msg := fmt.Sprintf("The %s host key for %s is marked as revoked.", keyKind(key.Type()), h.host)
	if h.cfg.Get("stricthostkeychecking") != "false" {
		return fmt.Errorf("%w: %s", ErrHostKey, msg)
	}
	h.o.Logf("%s", msg)
	h.goUnsafe()
	return nil
}

// goUnsafe is check_host_key's continue_unsafe.
func (h *hostCheck) goUnsafe() {
	h.unsafe = true
	h.o.Logf("Password and keyboard-interactive authentication, agent and port forwarding are disabled to avoid man-in-the-middle attacks.")
}

// certOK is sshkey_cert_check_host: a host certificate, for the host (or
// HostKeyAlias), valid now, signed with a CASignatureAlgorithms algorithm.
func (h *hostCheck) certOK(cert *ssh.Certificate) error {
	if cert.CertType != ssh.HostCert {
		return errors.New("certificate is not a host certificate")
	}
	if !slices.Contains(h.cfg.Algorithms("casignaturealgorithms"), cert.Signature.Format) {
		return fmt.Errorf("certificate signed with %s, not in CASignatureAlgorithms", cert.Signature.Format)
	}
	name := h.cfg.HostName()
	if h.alias {
		name = h.host
	}
	return (&ssh.CertChecker{}).CheckCert(name, cert)
}

// revokedByFile is sshkey_check_revoked over RevokedHostKeys: a list of
// public keys. ponytail: a KRL is refused, not read; read KRLs if a user
// needs one.
func (h *hostCheck) revokedByFile(key ssh.PublicKey) error {
	keys := []ssh.PublicKey{key}
	if c, ok := key.(*ssh.Certificate); ok {
		keys = append(keys, c.Key, c.SignatureKey)
	}
	for _, f := range strings.Fields(h.cfg.Get("revokedhostkeys")) {
		if f == "none" {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			return fmt.Errorf("%w: revoked keys file %s: %v", ErrHostKey, f, err)
		}
		if bytes.HasPrefix(b, []byte("SSHKRL\n\x00")) {
			return fmt.Errorf("%w: RevokedHostKeys %s is a KRL, which desk can't read; list the public keys instead", ErrHostKey, f)
		}
		for len(b) > 0 {
			k, _, _, rest, err := ssh.ParseAuthorizedKey(b)
			if err != nil {
				break
			}
			for _, x := range keys {
				if keyEqual(k, x) {
					return fmt.Errorf("%w: host key %s %s revoked by file %s", ErrHostKey, key.Type(), h.fingerprint(key), f)
				}
			}
			b = rest
		}
	}
	return nil
}

// command is KnownHostsCommand's lines for name (kind HOSTNAME or
// ADDRESS), run as load_hostkeys_command does: split into words, each
// expanded, no shell.
func (h *hostCheck) command(kind, name string, key ssh.PublicKey) []known {
	kc := h.cfg.Get("knownhostscommand")
	if kc == "" {
		return nil
	}
	tok := h.cfg.Tokens()
	tok['H'], tok['I'], tok['t'], tok['f'] = name, kind, key.Type(), h.fingerprint(key)
	tok['K'] = base64.StdEncoding.EncodeToString(key.Marshal())
	args := argvSplit(kc)
	for i, a := range args {
		v, err := sshconfig.Expand(a, tok, true)
		if err != nil {
			h.o.Logf("KnownHostsCommand: %v", err)
			return nil
		}
		args[i] = v
	}
	if len(args) == 0 || !filepath.IsAbs(args[0]) {
		h.o.Logf("KnownHostsCommand must be an absolute path")
		return nil
	}
	out, err := exec.Command(args[0], args[1:]...).Output()
	if err != nil {
		h.o.Logf("KnownHostsCommand %s: %v", args[0], err)
	}
	return parseKnown(out, "")
}

// argvSplit is misc.c's argv_split: words split at blanks, '…' and "…"
// quoting, a backslash escaping a quote, a backslash or a blank.
func argvSplit(s string) []string {
	var out []string
	var cur strings.Builder
	in, q := false, byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case q == 0 && (c == ' ' || c == '\t'):
			if in {
				out = append(out, cur.String())
				cur.Reset()
				in = false
			}
			continue
		case c == '\\' && i+1 < len(s) && strings.IndexByte(`'"\ `, s[i+1]) >= 0:
			i++
			c = s[i]
		case q == 0 && (c == '\'' || c == '"'):
			q, in = c, true
			continue
		case c == q:
			q = 0
			continue
		}
		cur.WriteByte(c)
		in = true
	}
	if in {
		out = append(out, cur.String())
	}
	return out
}

// fingerprint is the key's (a certificate's own key's) fingerprint per
// FingerprintHash.
func (h *hostCheck) fingerprint(key ssh.PublicKey) string {
	if c, ok := key.(*ssh.Certificate); ok {
		key = c.Key
	}
	alg := h.cfg.Get("fingerprinthash")
	hs := map[string]crypto.Hash{"SHA1": crypto.SHA1, "SHA384": crypto.SHA384, "SHA512": crypto.SHA512}[alg]
	switch {
	case alg == "MD5":
		return "MD5:" + ssh.FingerprintLegacyMD5(key)
	case hs != 0:
		d := hs.New()
		d.Write(key.Marshal())
		return alg + ":" + base64.RawStdEncoding.EncodeToString(d.Sum(nil))
	}
	return ssh.FingerprintSHA256(key)
}

// lines is found as KnownLines for name.
func (h *hostCheck) lines(found []known, name string) []KnownLine {
	var out []KnownLine
	for _, k := range found {
		out = append(out, KnownLine{File: k.file, Line: k.line, Name: name, Type: keyKind(k.key.Type()), Fingerprint: h.fingerprint(k.key), key: k.key})
	}
	return out
}

// keyKind is a key type as ssh names it in messages: ED25519, ECDSA, RSA.
func keyKind(t string) string {
	switch {
	case strings.Contains(t, "ecdsa-"):
		return "ECDSA"
	case strings.Contains(t, "ed25519"):
		return "ED25519"
	case t == ssh.KeyAlgoRSA:
		return "RSA"
	}
	return strings.ToUpper(strings.TrimPrefix(t, "ssh-"))
}

// HostKeyChangedError is a server sending another key than the one known
// for it: what ssh's "REMOTE HOST IDENTIFICATION HAS CHANGED" says, typed.
type HostKeyChangedError struct {
	Host        string // the name known_hosts was searched for
	Type        string // the key the server sent: ED25519, ECDSA, RSA…
	Fingerprint string
	Key         ssh.PublicKey
	Known       []KnownLine // the lines holding another key for Host (or its address)

	file  string // where AcceptNewKey records Key, "" for nowhere
	names []string
	hash  bool
}

func (e *HostKeyChangedError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "REMOTE HOST IDENTIFICATION HAS CHANGED: the %s key sent by %s is %s", e.Type, e.Host, e.Fingerprint)
	for _, k := range e.Known {
		fmt.Fprintf(&b, "; Offending %s key in %s:%d", k.Type, k.File, k.Line)
	}
	return b.String()
}

func (e *HostKeyChangedError) Unwrap() error { return ErrHostKey }

// AcceptNewKey takes the key of a changed-key refusal: the known lines are
// dropped (only while they still hold the old key for that name: nothing
// is dropped otherwise) and the new key is recorded, as ssh-keygen -R and
// a reconnect would by hand.
func AcceptNewKey(err error) error {
	var e *HostKeyChangedError
	if !errors.As(err, &e) {
		return errors.New("not a changed host key")
	}
	if e.file == "" {
		return errors.New("no UserKnownHostsFile to record the new key in")
	}
	if err := forget(e.Known); err != nil {
		return err
	}
	return addKnown(e.file, e.names, e.Key, e.hash)
}
