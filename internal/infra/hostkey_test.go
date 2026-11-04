package infra

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gioui.org/layout"
	"gioui.org/widget"
	"golang.org/x/crypto/ssh"
)

// hostKeyBanner is ssh's refusal of a changed key, as a pane shows it.
func hostKeyBanner(known string) string {
	return "@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@\r\n" +
		"@    WARNING: REMOTE HOST IDENTIFICATION HAS CHANGED!     @\r\n" +
		"@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@@\r\n" +
		"IT IS POSSIBLE THAT SOMEONE IS DOING SOMETHING NASTY!\r\n" +
		"The fingerprint for the ED25519 key sent by the remote host is\r\n" +
		"SHA256:WFkX7vC0AfQS0w7kISWCtC+G+9q9TIghUc5AlQOvOXw.\r\n" +
		"Please contact your system administrator.\r\n" +
		"Add correct host key in " + known + " to get rid of this message.\r\n" +
		"Offending ECDSA key in " + known + ":3\r\n" +
		"Host key for 192.0.2.12 has changed and you have requested strict checking.\r\n" +
		"Host key verification failed.\r\n"
}

// knownHosts writes a known_hosts whose line 3 is an ECDSA key for
// 192.0.2.12; it returns the file and that key's fingerprint.
func knownHosts(t *testing.T) (string, string) {
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	pub, _ := ssh.NewPublicKey(&k.PublicKey)
	line := "192.0.2.12 " + strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub)))
	p := filepath.Join(t.TempDir(), "known_hosts")
	os.WriteFile(p, []byte("192.0.2.1 ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIGd0dXRlc3RrZXlmb3JkZXNrdGVzdHNvbmx5MDAw\n# a comment\n"+line+"\n192.0.2.30 "+line[len("192.0.2.12 "):]+"\n"), 0o640)
	return p, ssh.FingerprintSHA256(pub)
}

// TestParseHostKeyChange: the key sent, and each offending known_hosts line
// with the key it holds; a refusal of another kind is none.
func TestParseHostKeyChange(t *testing.T) {
	known, print := knownHosts(t)
	c := parseHostKeyChange(strings.ReplaceAll(hostKeyBanner(known), "\r", "") + "Offending key for IP in " + known + ":3\nOffending key for IP in " + known + ":0\n")
	if c == nil || c.Type != "ED25519" || c.Remote != "SHA256:WFkX7vC0AfQS0w7kISWCtC+G+9q9TIghUc5AlQOvOXw" {
		t.Fatalf("%+v", c)
	}
	if len(c.Old) != 1 || c.Old[0] != (oldKey{File: known, Line: 3, Type: "ECDSA", Print: print}) {
		t.Fatalf("old %+v", c.Old)
	}
	if parseHostKeyChange("Host key verification failed.\n") != nil || parseHostKeyChange("ssh: connect to host 192.0.2.1 port 22: Connection refused") != nil {
		t.Error("not a changed key")
	}
	if c := parseHostKeyChange("REMOTE HOST IDENTIFICATION HAS\nCHANGED"); c == nil || c.Remote != "" || c.Old != nil {
		t.Errorf("a banner cut short: %+v", c)
	}
	for _, n := range []int{1, 2, 9} { // the ed25519 line is no real key, a comment isn't one, past the end
		if typ, p, h := knownKey(known, n); typ != "" || p != "" || h != nil {
			t.Errorf("line %d: %q %q %q", n, typ, p, h)
		}
	}
	if typ, _, _ := knownKey(filepath.Join(t.TempDir(), "none"), 1); typ != "" {
		t.Error("a missing file")
	}
	if c.Host != "192.0.2.12" {
		t.Errorf("host %q", c.Host)
	}
	// |1|salt|hash of 192.0.2.12, as ssh's HashKnownHosts writes it (HMAC-SHA1)
	hashed := "|1|c2FsdHNhbHRzYWx0c2FsdHNhbHQ=|" + hmacB64("saltsaltsaltsaltsalt", "192.0.2.12")
	for hosts, want := range map[string]bool{"192.0.2.12": true, "web,192.0.2.12": true, "[192.0.2.12]:2222": true, hashed: true,
		"192.0.2.120": false, "|1|c2FsdA==|bm9wZQ==": false, "|1|!!|!!": false, "|2|x|y": false} {
		if got := knownFor(strings.Split(hosts, ","), "192.0.2.12"); got != want {
			t.Errorf("%q: %v", hosts, got)
		}
	}
	for in, want := range map[string]string{"ssh-ed25519": "ED25519", "sk-ssh-ed25519@openssh.com": "ED25519", "ecdsa-sha2-nistp384": "ECDSA",
		"sk-ecdsa-sha2-nistp256@openssh.com": "ECDSA", "ssh-rsa": "RSA", "ssh-dss": "DSS"} {
		if got := keyKind(in); got != want {
			t.Errorf("%s: %s", in, got)
		}
	}
}

func hmacB64(salt, host string) string {
	mac := hmac.New(sha1.New, []byte(salt))
	mac.Write([]byte(host))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// TestAcceptHostKey: accepting drops just the offending lines (the file's
// mode kept) and marks the host's next ssh, once; without a line to drop,
// or a file it can't read, it says so.
func TestAcceptHostKey(t *testing.T) {
	known, _ := knownHosts(t)
	b0, _ := os.ReadFile(known)
	lines := strings.SplitAfter(string(b0), "\n")
	if err := acceptHostKey("web-02", parseHostKeyChange(hostKeyBanner(known))); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(known)
	if string(b) != lines[0]+lines[1]+lines[3] {
		t.Fatalf("left:\n%s", b)
	}
	if st, _ := os.Stat(known); st.Mode().Perm() != 0o640 {
		t.Errorf("mode %v", st.Mode())
	}
	if !takeAcceptNew("web-02") || takeAcceptNew("web-02") || takeAcceptNew("web-01") {
		t.Error("accept-new once, for that host")
	}
	if err := acceptHostKey("x", &hostKeyChange{}); err == nil {
		t.Error("nothing to drop")
	}
	if err := acceptHostKey("x", &hostKeyChange{Old: []oldKey{{File: filepath.Join(t.TempDir(), "none"), Line: 1}}}); err == nil || takeAcceptNew("x") {
		t.Error("a file that isn't there")
	}
	if err := acceptHostKey("x", parseHostKeyChange(hostKeyBanner(known))); err == nil || !strings.Contains(err.Error(), ":3 no longer holds the key ssh named") || takeAcceptNew("x") {
		t.Errorf("a stale banner: line 3 is another host's now: %v", err) // and nothing dropped:
	}
	if b2, _ := os.ReadFile(known); string(b2) != string(b) {
		t.Errorf("dropped anyway:\n%s", b2)
	}
	k2, p2 := knownHosts(t)
	os.Mkdir(k2+".desk-tmp", 0o755) // where the new file goes is a folder
	if err := (&hostKeyChange{Old: []oldKey{{File: k2, Line: 3, Print: p2}}}).forget(); err == nil || strings.Contains(err.Error(), "no longer holds") {
		t.Errorf("a file it can't write: %v", err)
	}
	if err := (&hostKeyChange{Old: []oldKey{{File: k2, Line: 3}}}).forget(); err == nil {
		t.Error("a line read without a key")
	}
	if err := (&hostKeyChange{Host: "192.0.2.12", Old: []oldKey{{File: k2, Line: 3, Print: "SHA256:other"}}}).forget(); err == nil || !strings.Contains(err.Error(), "no longer holds") {
		t.Errorf("the host's line holds another key now: %v", err)
	}
	if home, _ := os.UserHomeDir(); tildeHome(filepath.Join(home, ".ssh", "known_hosts")) != "~/.ssh/known_hosts" || tildeHome("/etc/ssh/ssh_known_hosts") != "/etc/ssh/ssh_known_hosts" {
		t.Error("tildeHome")
	}
}

// TestAcceptNewSSH: the connection after an accept runs ssh with
// StrictHostKeyChecking=accept-new; the next one doesn't.
func TestAcceptNewSSH(t *testing.T) {
	out := filepath.Join(t.TempDir(), "args")
	fakeBinC(t, "ssh", `printf '%s\n' "$@" >> `+out+"\n")
	acceptNew.Lock()
	acceptNew.hosts["web-02"] = true
	acceptNew.Unlock()
	for range 2 {
		tm, err := NewTerm("web-02", t.TempDir(), 80, 24, func() {}, paneState{})
		if err != nil {
			t.Fatal(err)
		}
		for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
			if _, e := tm.State(); e {
				break
			}
		}
		tm.Close()
	}
	args := waitFileC(t, out)
	for end := time.Now().Add(5 * time.Second); strings.Count(args, "web-02\n") < 2 && time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		args = waitFileC(t, out)
	}
	if strings.Count(args, "StrictHostKeyChecking=accept-new") != 1 || strings.Count(args, "web-02\n") != 2 {
		t.Fatalf("ssh args:\n%s", args)
	}
}

// TestPaletteOverConnBox: with the palette open over a pane's connection
// box, keys are the palette's (esc closes it, not the pane; enter opens its
// pick), as when no box is up.
func TestPaletteOverConnBox(t *testing.T) {
	failed, _ := gmTerm(t, "web-02", 80, 24)
	failed.exited = true
	tb := &Tab{org: "Acme", name: "web-02", pinned: map[int]bool{}, panes: []*Pane{{id: 1, term: failed, name: "web-02"}}}
	tb.term = failed
	m := model{tabs: []*Tab{tb}, pal: true}
	if tb.connState() != "failed" {
		t.Fatalf("state %q", tb.connState())
	}
	r, _ := m.key(keyOf("esc"))
	if mm := r.(model); mm.pal || len(tb.panes) != 1 || tb.panes[0].closed {
		t.Fatalf("esc: palette open %v, panes %d", mm.pal, len(tb.panes))
	}
	r, _ = r.(model).key(keyOf("esc")) // the palette gone: the box takes it
	if len(tb.panes) == 1 && !tb.panes[0].closed {
		t.Error("esc on the box closes the pane")
	}
}

// TestGMHostKeyBox: a pane whose host key changed shows both keys, says
// what accepting does, and has Accept new key (a), which drops the line and
// reconnects; the snapshot finds the change in the pane's output.
func TestGMHostKeyBox(t *testing.T) {
	known, print := knownHosts(t)
	failed, _ := gmTerm(t, "web-02", 200, 24)
	gmOut(failed, hostKeyBanner(known))
	failed.exited = true
	tb := &Tab{org: "Acme", name: "web-02", pinned: map[int]bool{}, panes: []*Pane{{id: 1, term: failed, name: "web-02"}}}
	gp := (&driver{}).snapshot(&model{tabs: []*Tab{tb}}).Tabs[0].Panes[0]
	if gp.Conn != "failed" || gp.KeyChange == nil || gp.KeyChange.Old[0].Print != print {
		t.Fatalf("snapshot %+v", gp)
	}
	title, _, guide := connText(gp, time.Now())
	if title != "web-02's host key changed" || !strings.Contains(guide, "drops the old line from known_hosts") {
		t.Errorf("%q %q", title, guide)
	}
	h := newGM(t)
	u := h.u
	st, _, _ := gmTermState(t)
	h.set(st)
	u.org = "Acme"
	st.Tabs[0].Panes[0].Conn, st.Tabs[0].Panes[0].KeyChange = "failed", gp.KeyChange
	u.clicks = map[string]*widget.Clickable{}
	w := func(gtx layout.Context) { u.terminals(gtx) }
	h.draw(w)
	if u.clicks["connA"] == nil || u.clicks["connR"] == nil {
		t.Fatal("Accept new key and Reconnect")
	}
	u.click("connA").Click()
	h.draw(w)
	if len(h.acts()) != 1 {
		t.Error("Accept presses a key")
	}
	st.Tabs[0].Panes[0].KeyChange = &hostKeyChange{Type: "ED25519", Remote: "SHA256:x"} // no line to drop: nothing to accept
	u.clicks = map[string]*widget.Clickable{}
	h.draw(w)
	if u.clicks["connA"] != nil {
		t.Error("Accept with no known_hosts line")
	}

	HostCmd = func(string) *exec.Cmd { return exec.Command("bash", "--norc", "-i") } // the reconnect, without ssh
	defer func() { HostCmd = nil }()
	tb.term = failed
	m := model{tabs: []*Tab{tb}}
	m.key(keyOf("a"))
	if b, _ := os.ReadFile(known); strings.Count(string(b), "\n") != 3 || tb.panes[0].term == failed {
		t.Fatalf("a: the line dropped and a new connection:\n%s", b)
	}
	tb.panes[0].term.Close()
	if !takeAcceptNew("web-02") {
		t.Error("its next ssh accepts the new key")
	}
	m.errText = ""
	tb.panes[0].term = failed
	tb.term = failed
	before, _ := os.ReadFile(known)
	r, _ := m.key(keyOf("a")) // the same banner again: line 3 is another host's now
	after, _ := os.ReadFile(known)
	if e := r.(model).errText; !strings.HasPrefix(e, "accepting the new host key: ") || string(after) != string(before) || tb.panes[0].term != failed {
		t.Errorf("a stale banner: err %q, file\n%s", e, after)
	}
}
