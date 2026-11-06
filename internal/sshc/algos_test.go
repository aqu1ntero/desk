package sshc

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestNegotiationErrors(t *testing.T) {
	rsaOnly, err := ssh.NewSignerWithAlgorithms(genRSA(t).(ssh.AlgorithmSigner), []string{ssh.KeyAlgoRSA})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		what, fix string
		srv       srvOpts
	}{
		{"Ciphers", "aes128-cbc", srvOpts{config: func(c *ssh.ServerConfig) { c.Ciphers = []string{"aes128-cbc"} }}},
		{"KexAlgorithms", "diffie-hellman-group1-sha1", srvOpts{config: func(c *ssh.ServerConfig) { c.KeyExchanges = []string{"diffie-hellman-group1-sha1"} }}},
		{"MACs", "hmac-sha1-96", srvOpts{config: func(c *ssh.ServerConfig) {
			c.Ciphers, c.MACs = []string{"aes128-ctr"}, []string{"hmac-sha1-96"}
		}}},
		{"HostKeyAlgorithms", "ssh-rsa", srvOpts{hostKeys: []ssh.Signer{rsaOnly}}},
	} {
		w := newWorld(t)
		s := pwServer(t, w, tc.srv)
		_, err := dialT(t, w.opts(base(""), s))
		var e *NegotiationError
		if !errors.As(err, &e) || e.What != tc.what || !slices.Equal(e.Fixable(), []string{tc.fix}) || len(e.Ours) == 0 {
			t.Fatalf("%s: err %v", tc.what, err)
		}
		if slices.ContainsFunc(e.Theirs, func(a string) bool { return strings.HasPrefix(a, "kex-strict-") || strings.HasPrefix(a, "ext-info-") }) {
			t.Fatalf("%s: theirs %q", tc.what, e.Theirs)
		}
		if !strings.Contains(e.Error(), "no matching "+tc.what+" found. Their offer: "+strings.Join(e.Theirs, ",")) {
			t.Fatalf("message %q", e.Error())
		}
		// The fix: the legacy algorithm added.
		w.ans = []string{"pw"}
		if _, err := dialT(t, w.opts(base("  "+tc.what+" +"+tc.fix+"\n"), s)); err != nil {
			t.Fatalf("%s +%s: %v", tc.what, tc.fix, err)
		}
	}
}

func TestNegotiationPassesOthers(t *testing.T) {
	e := errors.New("x")
	if negotiation(e) != e {
		t.Fatal("changed")
	}
	if n := negotiation(&ssh.AlgorithmNegotiationError{What: "public key signature algorithm"}).(*NegotiationError); n.What != "PubkeyAcceptedAlgorithms" {
		t.Fatalf("what %q", n.What)
	}
}

func TestNoImplementedAlgorithm(t *testing.T) {
	w := newWorld(t)
	for _, line := range []string{"KexAlgorithms sntrup761x25519-sha512", "Ciphers aes192-cbc", "MACs umac-64@openssh.com", "HostKeyAlgorithms sk-ssh-ed25519@openssh.com"} {
		cfg := resolve(t, w, "Host *\n  "+line+"\n")
		if _, err := clientConfig(cfg, &hostCheck{}); err == nil || !strings.Contains(err.Error(), "desk implements none of") {
			t.Fatalf("%s: err %v", line, err)
		}
	}
}

func TestClientConfig(t *testing.T) {
	w := newWorld(t)
	cfg := resolve(t, w, "Host *\n  User me\n  RekeyLimit 1G 1h\n  VersionAddendum desk\n  Ciphers aes256-gcm@openssh.com,aes192-cbc,aes128-ctr\n")
	c, err := clientConfig(cfg, &hostCheck{})
	if err != nil {
		t.Fatal(err)
	}
	if c.RekeyThreshold != 1<<30 || c.ClientVersion != "SSH-2.0-Go desk" || c.User != "me" || !slices.Equal(c.Ciphers, []string{"aes256-gcm@openssh.com", "aes128-ctr"}) {
		t.Fatalf("%+v", c)
	}
	c, _ = clientConfig(resolve(t, w, "Host *\n  VersionAddendum none\n"), &hostCheck{})
	if c.ClientVersion != "" || c.RekeyThreshold != 0 {
		t.Fatalf("%+v", c)
	}
}
