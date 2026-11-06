package infra

import (
	"slices"
	"strings"
	"testing"
)

// seedSSHQ makes sshSupports answer kinds as given for one test.
func seedSSHQ(t *testing.T, kinds map[string][]string) {
	sshQMu.Lock()
	defer sshQMu.Unlock()
	for kind, names := range kinds {
		old, had := sshQ[kind]
		s := map[string]bool{}
		for _, n := range names {
			s[n] = true
		}
		sshQ[kind] = s
		t.Cleanup(func() {
			sshQMu.Lock()
			defer sshQMu.Unlock()
			if had {
				sshQ[kind] = old
			} else {
				delete(sshQ, kind)
			}
		})
	}
}

// TestAsbruAlgorithms: an algorithm the local ssh lacks is dropped from its
// list and noted, a list left empty goes, patterns and other keywords stay,
// and a kind ssh can't list stays as it is.
func TestAsbruAlgorithms(t *testing.T) {
	seedSSHQ(t, map[string][]string{
		"key-sig": {"ssh-ed25519", "ssh-rsa"},
		"kex":     {"diffie-hellman-group1-sha1", "curve25519-sha256"},
		"mac":     {"hmac-sha2-256"},
		"cipher":  nil,
	})
	y := `environments:
  a: {name: a, ip: 192.0.2.1, method: SSH, options: '-o "HostKeyAlgorithms=+ssh-dss,ssh-rsa" -o "PubkeyAcceptedKeyTypes=+ssh-dss" -o "KexAlgorithms=diffie-hellman-group1-sha1" -o "Ciphers=^3des-cbc" -o "MACs=-hmac-md5,hmac-sha1*" -o "ServerAliveInterval=60"'}
  b: {name: b, ip: 192.0.2.2, method: SSH, options: '-o "hostkeyalgorithms=ssh-rsa,,ssh-dss"'}
`
	text, _, _, err := AsbruConfig([]byte(y), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	want := `Host a
    HostName 192.0.2.1
    HostKeyAlgorithms +ssh-rsa
    KexAlgorithms diffie-hellman-group1-sha1
    Ciphers ^3des-cbc
    MACs -hmac-sha1*
    ServerAliveInterval 60
    # HostKeyAlgorithms +ssh-dss dropped: this ssh doesn't support it (ssh -Q key-sig)
    # PubkeyAcceptedKeyTypes +ssh-dss dropped: this ssh doesn't support it (ssh -Q key-sig)
    # MACs -hmac-md5 dropped: this ssh doesn't support it (ssh -Q mac)
Host b
    HostName 192.0.2.2
    hostkeyalgorithms ssh-rsa
    # hostkeyalgorithms ssh-dss dropped: this ssh doesn't support it (ssh -Q key-sig)
`
	if !strings.HasSuffix(text, want) {
		t.Errorf("got:\n%s\nwant it to end with:\n%s", text, want)
	}
}

// TestAsbruAlgorithmsUnchanged: a list with nothing to drop is written as it was.
func TestAsbruAlgorithmsUnchanged(t *testing.T) {
	seedSSHQ(t, map[string][]string{"key-sig": {"ssh-rsa"}})
	h := &asbruHost{lines: [][2]string{{"HostKeyAlgorithms", "+ssh-rsa, ssh-*"}, {"User", "me"}}}
	h.supported()
	if !slices.Equal(h.lines, [][2]string{{"HostKeyAlgorithms", "+ssh-rsa, ssh-*"}, {"User", "me"}}) || h.notes != nil {
		t.Errorf("lines %q, notes %q", h.lines, h.notes)
	}
}
