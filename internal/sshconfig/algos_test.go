package sshconfig

import "testing"

// TestAlgorithm: each list keyword (any case, aliases) checks against its own
// names, key lists take patterns and CASignatureAlgorithms no certificates.
func TestAlgorithm(t *testing.T) {
	for _, c := range []struct {
		kw, name string
		ok, list bool
	}{
		{"Ciphers", "aes128-ctr", true, true},
		{"Ciphers", "hmac-sha1", false, true},
		{"Ciphers", "aes*", false, true},
		{"MACs", "hmac-sha1", true, true},
		{"MACs", "aes128-ctr", false, true},
		{"KexAlgorithms", "curve25519-sha256", true, true},
		{"KexAlgorithms", "hmac-sha1", false, true},
		{"hostkeyalgorithms", "ssh-rsa", true, true},
		{"HostKeyAlgorithms", "ssh-dss", false, true},
		{"HostKeyAlgorithms", "ssh-*", true, true},
		{"PubkeyAcceptedKeyTypes", "ssh-ed25519-cert-v01@openssh.com", true, true},
		{"HostbasedAcceptedAlgorithms", "ssh-dss", false, true},
		{"CASignatureAlgorithms", "ssh-ed25519", true, true},
		{"CASignatureAlgorithms", "ssh-ed25519-cert-v01@openssh.com", false, true},
		{"User", "ssh-rsa", false, false},
		{"Nonsense", "ssh-rsa", false, false},
	} {
		if ok, list := Algorithm(c.kw, c.name); ok != c.ok || list != c.list {
			t.Errorf("Algorithm(%q, %q) = %v, %v; want %v, %v", c.kw, c.name, ok, list, c.ok, c.list)
		}
	}
}
