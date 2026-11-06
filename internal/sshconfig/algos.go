package sshconfig

import (
	"errors"
	"slices"
	"strings"
)

// The algorithms OpenSSH 10.5 (with OpenSSL) knows, in its own order: what
// `ssh -Q cipher|mac|kex|key-sig|sig` lists. Patterns and +/-/^ lists are
// resolved against these.
var (
	allCiphers = []string{"3des-cbc", "aes128-cbc", "aes192-cbc", "aes256-cbc", "aes128-ctr", "aes192-ctr",
		"aes256-ctr", "aes128-gcm@openssh.com", "aes256-gcm@openssh.com", "chacha20-poly1305@openssh.com"}
	allMACs = []string{"hmac-sha1", "hmac-sha1-96", "hmac-sha2-256", "hmac-sha2-512", "hmac-md5", "hmac-md5-96",
		"umac-64@openssh.com", "umac-128@openssh.com", "hmac-sha1-etm@openssh.com", "hmac-sha1-96-etm@openssh.com",
		"hmac-sha2-256-etm@openssh.com", "hmac-sha2-512-etm@openssh.com", "hmac-md5-etm@openssh.com",
		"hmac-md5-96-etm@openssh.com", "umac-64-etm@openssh.com", "umac-128-etm@openssh.com"}
	allKex = []string{"diffie-hellman-group1-sha1", "diffie-hellman-group14-sha1", "diffie-hellman-group14-sha256",
		"diffie-hellman-group16-sha512", "diffie-hellman-group18-sha512", "diffie-hellman-group-exchange-sha1",
		"diffie-hellman-group-exchange-sha256", "ecdh-sha2-nistp256", "ecdh-sha2-nistp384", "ecdh-sha2-nistp521",
		"mlkem768nistp256-sha256", "curve25519-sha256", "curve25519-sha256@libssh.org", "sntrup761x25519-sha512",
		"sntrup761x25519-sha512@openssh.com", "mlkem768x25519-sha256"}
	allKeys = []string{"ssh-ed25519", "ssh-ed25519-cert-v01@openssh.com", "sk-ssh-ed25519@openssh.com",
		"sk-ssh-ed25519-cert-v01@openssh.com", "ssh-mldsa44-ed25519@openssh.com", "ssh-mldsa44-ed25519-cert-v01@openssh.com",
		"ecdsa-sha2-nistp256", "ecdsa-sha2-nistp256-cert-v01@openssh.com", "ecdsa-sha2-nistp384",
		"ecdsa-sha2-nistp384-cert-v01@openssh.com", "ecdsa-sha2-nistp521", "ecdsa-sha2-nistp521-cert-v01@openssh.com",
		"sk-ecdsa-sha2-nistp256@openssh.com", "sk-ecdsa-sha2-nistp256-cert-v01@openssh.com",
		"webauthn-sk-ecdsa-sha2-nistp256@openssh.com", "webauthn-sk-ecdsa-sha2-nistp256-cert-v01@openssh.com",
		"ssh-rsa", "ssh-rsa-cert-v01@openssh.com", "rsa-sha2-256", "rsa-sha2-256-cert-v01@openssh.com",
		"rsa-sha2-512", "rsa-sha2-512-cert-v01@openssh.com"}
)

// The defaults, from myproposal.h.
const (
	defCiphers = "chacha20-poly1305@openssh.com,aes128-gcm@openssh.com,aes256-gcm@openssh.com,aes128-ctr,aes192-ctr,aes256-ctr"
	defMACs    = "umac-64-etm@openssh.com,umac-128-etm@openssh.com,hmac-sha2-256-etm@openssh.com," +
		"hmac-sha2-512-etm@openssh.com,hmac-sha1-etm@openssh.com,umac-64@openssh.com,umac-128@openssh.com," +
		"hmac-sha2-256,hmac-sha2-512,hmac-sha1"
	defKex = "mlkem768x25519-sha256,sntrup761x25519-sha512,sntrup761x25519-sha512@openssh.com,curve25519-sha256," +
		"curve25519-sha256@libssh.org,ecdh-sha2-nistp256,ecdh-sha2-nistp384,ecdh-sha2-nistp521," +
		"diffie-hellman-group-exchange-sha256,diffie-hellman-group16-sha512,diffie-hellman-group18-sha512," +
		"diffie-hellman-group14-sha256"
	defKeys = "ssh-ed25519-cert-v01@openssh.com,ecdsa-sha2-nistp256-cert-v01@openssh.com," +
		"ecdsa-sha2-nistp384-cert-v01@openssh.com,ecdsa-sha2-nistp521-cert-v01@openssh.com," +
		"sk-ssh-ed25519-cert-v01@openssh.com,sk-ecdsa-sha2-nistp256-cert-v01@openssh.com," +
		"webauthn-sk-ecdsa-sha2-nistp256-cert-v01@openssh.com,rsa-sha2-512-cert-v01@openssh.com," +
		"rsa-sha2-256-cert-v01@openssh.com,ssh-ed25519,ecdsa-sha2-nistp256,ecdsa-sha2-nistp384,ecdsa-sha2-nistp521," +
		"sk-ssh-ed25519@openssh.com,sk-ecdsa-sha2-nistp256@openssh.com,webauthn-sk-ecdsa-sha2-nistp256@openssh.com," +
		"rsa-sha2-512,rsa-sha2-256"
	defSigs = "ssh-ed25519,ecdsa-sha2-nistp256,ecdsa-sha2-nistp384,ecdsa-sha2-nistp521,sk-ssh-ed25519@openssh.com," +
		"sk-ecdsa-sha2-nistp256@openssh.com,webauthn-sk-ecdsa-sha2-nistp256@openssh.com,rsa-sha2-512,rsa-sha2-256"
)

func isCert(k string) bool { return strings.HasSuffix(k, "-cert-v01@openssh.com") }

// plainKeys is allKeys without certificates: what CASignatureAlgorithms may name.
func plainKeys() []string {
	var out []string
	for _, k := range allKeys {
		if !isCert(k) {
			out = append(out, k)
		}
	}
	return out
}

// namesValid is cipher.c's ciphers_valid, mac.c's mac_valid and
// kex-names.c's kex_names_valid: every name up to the first empty one is known.
func namesValid(names string, known []string) bool {
	if names == "" {
		return false
	}
	for _, n := range strings.Split(names, ",") {
		if n == "" {
			break
		}
		if !slices.Contains(known, n) {
			return false
		}
	}
	return true
}

// keyNamesValid is sshkey.c's sshkey_names_valid2 with wildcards allowed: a
// name that isn't a key type passes when it matches (or negates) one.
func keyNamesValid(names string, plainOnly bool) bool {
	if names == "" {
		return false
	}
	for _, n := range strings.Split(names, ",") {
		if n == "" {
			break
		}
		if slices.Contains(allKeys, n) {
			if plainOnly && isCert(n) {
				return false
			}
			continue
		}
		if !slices.ContainsFunc(allKeys, func(k string) bool { return matchPatternList(k, n, false) != 0 }) {
			return false
		}
	}
	return true
}

// filterList is match.c's filter_list: the names of list that the pattern
// list matches (allow) or doesn't (deny).
func filterList(list, filter string, deny bool) string {
	var out []string
	for _, n := range strings.Split(list, ",") {
		r := matchPatternList(n, filter, false)
		if deny && r != 1 || !deny && r == 1 {
			out = append(out, n)
		}
	}
	return strings.Join(out, ",")
}

// namesCat is kex-names.c's kex_names_cat: a then b's names a lacks.
func namesCat(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	// match_list, which checks what a already has, stops at an empty name.
	have := strings.Split(a, ",")
	gap := slices.Index(have, "")
	if gap >= 0 {
		have = have[:gap]
	}
	for _, n := range strings.Split(b, ",") {
		if n == "" {
			break
		}
		if !slices.Contains(have, n) {
			if gap < 0 {
				have = append(have, n)
			}
			a += "," + n
		}
	}
	return a
}

// assemble is kex-names.c's kex_assemble_names: a configured list against
// the default, +appending, -removing or ^prepending, patterns resolved
// against every known name.
func assemble(list, def string, all []string) (string, error) {
	if list == "" {
		return def, nil
	}
	switch list[0] {
	case '+':
		list = namesCat(def, list[1:])
	case '-':
		return filterList(def, list[1:], true), nil
	case '^':
		list = namesCat(list[1:], def)
	}
	every := strings.Join(all, ",")
	ret := ""
	for _, p := range strings.Split(list, ",") {
		if strings.HasPrefix(p, "!") {
			return "", errors.New("negated algorithm patterns aren't supported here")
		}
		ret = namesCat(ret, filterList(every, p, false))
	}
	if ret == "" {
		return "", errors.New("empty algorithm list")
	}
	return ret, nil
}
