package infra

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestAsbruAlgorithms: an algorithm the local ssh lacks is dropped from its
// list and noted, a list left empty goes, patterns and other keywords stay,
// a kind ssh can't list stays as it is, and each kind is asked once.
func TestAsbruAlgorithms(t *testing.T) {
	asked := map[string]int{}
	old := sshQuery
	sshQuery = func(kind string) []string {
		asked[kind]++
		return map[string][]string{
			"key-sig": {"ssh-ed25519", "ssh-rsa"},
			"kex":     {"diffie-hellman-group1-sha1", "curve25519-sha256"},
			"mac":     {"hmac-sha2-256"},
		}[kind]
	}
	t.Cleanup(func() { sshQuery = old })
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
	for kind, n := range asked {
		if n != 1 {
			t.Errorf("ssh -Q %s asked %d times", kind, n)
		}
	}
	if kinds := slices.Sorted(maps.Keys(asked)); !slices.Equal(kinds, []string{"cipher", "kex", "key-sig", "mac"}) {
		t.Errorf("asked %q", kinds)
	}
}

// TestAsbruAlgorithmsUnchanged: a list with nothing to drop is written as it was.
func TestAsbruAlgorithmsUnchanged(t *testing.T) {
	old := sshQuery
	sshQuery = func(string) []string { return []string{"ssh-rsa"} }
	t.Cleanup(func() { sshQuery = old })
	h := &asbruHost{lines: [][2]string{{"HostKeyAlgorithms", "+ssh-rsa, ssh-*"}, {"User", "me"}}}
	h.supported(map[string]map[string]bool{})
	if !slices.Equal(h.lines, [][2]string{{"HostKeyAlgorithms", "+ssh-rsa, ssh-*"}, {"User", "me"}}) || h.notes != nil {
		t.Errorf("lines %q, notes %q", h.lines, h.notes)
	}
}


// TestSSHQuery: ssh -Q's lines; nil without an ssh or when it refuses.
func TestSSHQuery(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "ssh"), []byte("#!/bin/sh\n[ \"$1 $2\" = \"-Q kex\" ] || exit 1\nprintf 'a-1\\nb-2\\n'\n"), 0o755)
	t.Setenv("PATH", dir)
	if got := sshQuery("kex"); !slices.Equal(got, []string{"a-1", "b-2"}) {
		t.Errorf("kex: %q", got)
	}
	if got := sshQuery("key-sig"); got != nil {
		t.Errorf("refused: %q", got)
	}
	t.Setenv("PATH", t.TempDir())
	if got := sshQuery("kex"); got != nil {
		t.Errorf("no ssh: %q", got)
	}
}
