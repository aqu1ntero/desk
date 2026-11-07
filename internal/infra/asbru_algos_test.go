package infra

import (
	"slices"
	"strings"
	"testing"
)

// TestAsbruAlgorithms: a name desk's SSH client doesn't know is dropped from
// its list and noted, a list left empty goes, a -list and other keywords stay.
func TestAsbruAlgorithms(t *testing.T) {
	y := `environments:
  a: {name: a, ip: 192.0.2.1, method: SSH, options: '-o "HostKeyAlgorithms=+ssh-dss,ssh-rsa" -o "PubkeyAcceptedKeyTypes=+ssh-dss" -o "KexAlgorithms=diffie-hellman-group1-sha1" -o "Ciphers=^3des-cbc,aes*" -o "MACs=-hmac-md5,hmac-nope" -o "CASignatureAlgorithms=ssh-ed25519-cert-v01@openssh.com,ssh-ed25519" -o "ServerAliveInterval=60"'}
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
    MACs -hmac-md5,hmac-nope
    CASignatureAlgorithms ssh-ed25519
    ServerAliveInterval 60
    # HostKeyAlgorithms +ssh-dss dropped: desk's SSH client doesn't support it
    # PubkeyAcceptedKeyTypes +ssh-dss dropped: desk's SSH client doesn't support it
    # Ciphers ^aes* dropped: desk's SSH client doesn't support it
    # CASignatureAlgorithms ssh-ed25519-cert-v01@openssh.com dropped: desk's SSH client doesn't support it
Host b
    HostName 192.0.2.2
    hostkeyalgorithms ssh-rsa
    # hostkeyalgorithms ssh-dss dropped: desk's SSH client doesn't support it
`
	if !strings.HasSuffix(text, want) {
		t.Errorf("got:\n%s\nwant it to end with:\n%s", text, want)
	}
}

// TestAsbruAlgorithmsUnchanged: a list with nothing to drop (key patterns
// pass) is written as it was.
func TestAsbruAlgorithmsUnchanged(t *testing.T) {
	h := &asbruHost{lines: [][2]string{{"HostKeyAlgorithms", "+ssh-rsa, ssh-*"}, {"User", "me"}}}
	h.supported()
	if !slices.Equal(h.lines, [][2]string{{"HostKeyAlgorithms", "+ssh-rsa, ssh-*"}, {"User", "me"}}) || h.notes != nil {
		t.Errorf("lines %q, notes %q", h.lines, h.notes)
	}
}
