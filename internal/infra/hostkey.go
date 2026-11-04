package infra

import (
	"bufio"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
)

// A server whose host key changed (reinstalled, keys rotated, or someone in
// between) makes ssh refuse with a banner: the key it was sent, by
// fingerprint, and the known_hosts lines that hold the old one. The
// connection box shows both keys side by side; accepting drops those lines
// and connects once with StrictHostKeyChecking=accept-new, so ssh saves the
// new key, as ssh-keygen -R and a reconnect would by hand.

// hostKeyChange is what ssh's banner said, with the known key's fingerprint.
type hostKeyChange struct {
	Host         string   // the address ssh checked ("Host key for … has changed")
	Type, Remote string   // the key the server sent: ED25519, SHA256:…
	Old          []oldKey // the known_hosts lines with the key ssh expected
}

// oldKey is one offending known_hosts line and its key.
type oldKey struct {
	File        string
	Line        int
	Type, Print string // "" when the line couldn't be read
}

var (
	keyChanged = regexp.MustCompile(`REMOTE HOST IDENTIFICATION HAS\s*CHANGED`)
	keySent    = regexp.MustCompile(`fingerprint for the (\S+) key sent by\s+the remote host is\s+(SHA256:[A-Za-z0-9+/=]+)`)
	keyOld     = regexp.MustCompile(`Offending (?:\S+ )?key(?: for IP)? in (\S+):(\d+)`)
	keyFor     = regexp.MustCompile(`Host key for (\S+) has changed`)
)

// parseHostKeyChange reads ssh's output (unwrapped), nil when it isn't the
// changed-key refusal.
func parseHostKeyChange(out string) *hostKeyChange {
	if !keyChanged.MatchString(out) {
		return nil
	}
	c := &hostKeyChange{}
	if m := keySent.FindStringSubmatch(out); m != nil {
		c.Type, c.Remote = m[1], strings.TrimSuffix(m[2], ".")
	}
	if m := keyFor.FindStringSubmatch(out); m != nil {
		c.Host = m[1]
	}
	seen := map[string]bool{}
	for _, m := range keyOld.FindAllStringSubmatch(out, -1) {
		n, _ := strconv.Atoi(m[2])
		if k := m[1] + ":" + m[2]; !seen[k] && n > 0 {
			seen[k] = true
			o := oldKey{File: m[1], Line: n}
			o.Type, o.Print, _ = knownKey(m[1], n)
			c.Old = append(c.Old, o)
		}
	}
	return c
}

// knownKey is the type, fingerprint and host patterns of known_hosts line
// n of file.
func knownKey(file string, n int) (string, string, []string) {
	f, err := os.Open(file)
	if err != nil {
		return "", "", nil
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for i := 1; sc.Scan(); i++ {
		if i < n {
			continue
		}
		_, hosts, key, _, _, err := ssh.ParseKnownHosts(sc.Bytes())
		if err != nil {
			return "", "", nil
		}
		return keyKind(key.Type()), ssh.FingerprintSHA256(key), hosts
	}
	return "", "", nil
}

// knownFor says whether a known_hosts line's host patterns name host: as it
// is, as [host]:port, or hashed (|1|salt|hash, HMAC-SHA1 of host by salt).
func knownFor(hosts []string, host string) bool {
	for _, h := range hosts {
		if h == host || strings.HasPrefix(h, "["+host+"]:") {
			return true
		}
		if p := strings.Split(h, "|"); len(p) == 4 && p[1] == "1" {
			salt, err1 := base64.StdEncoding.DecodeString(p[2])
			want, err2 := base64.StdEncoding.DecodeString(p[3])
			mac := hmac.New(sha1.New, salt)
			mac.Write([]byte(host))
			if err1 == nil && err2 == nil && hmac.Equal(mac.Sum(nil), want) {
				return true
			}
		}
	}
	return false
}

// keyKind is a key type as ssh's banner names it: ED25519, ECDSA, RSA.
func keyKind(t string) string {
	switch {
	case strings.HasPrefix(t, "ecdsa-"), strings.HasPrefix(t, "sk-ecdsa-"):
		return "ECDSA"
	case strings.Contains(t, "ed25519"):
		return "ED25519"
	case t == "ssh-rsa":
		return "RSA"
	}
	return strings.ToUpper(strings.TrimPrefix(t, "ssh-"))
}

// tildeHome is path with the home folder as ~.
func tildeHome(path string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(path, home+"/") {
		return "~" + path[len(home):]
	}
	return path
}

// forget drops the offending lines from their known_hosts files, keeping
// every other line and the file's mode. A line must still hold the key it
// was read with (a file edited since, or a stale banner, would point it at
// another host's): else nothing is dropped.
func (c *hostKeyChange) forget() error {
	byFile := map[string]map[int]string{}
	for _, o := range c.Old {
		if byFile[o.File] == nil {
			byFile[o.File] = map[int]string{}
		}
		byFile[o.File][o.Line] = o.Print
	}
	for file, lines := range byFile {
		for n, print := range lines {
			if _, now, hosts := knownKey(file, n); print == "" || now != print || c.Host != "" && !knownFor(hosts, c.Host) {
				return fmt.Errorf("%s:%d no longer holds the key ssh named; nothing dropped", tildeHome(file), n)
			}
		}
	}
	for file, lines := range byFile {
		st, err := os.Stat(file)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		var keep []string
		for i, l := range strings.SplitAfter(string(b), "\n") {
			if _, drop := lines[i+1]; !drop {
				keep = append(keep, l)
			}
		}
		tmp := file + ".desk-tmp"
		if err := os.WriteFile(tmp, []byte(strings.Join(keep, "")), st.Mode().Perm()); err != nil {
			return err
		}
		if err := os.Rename(tmp, file); err != nil {
			os.Remove(tmp)
			return err
		}
	}
	return nil
}

// acceptNew is the hosts whose next connection takes the key the server
// sends (StrictHostKeyChecking=accept-new): once, right after the user
// accepted it in the connection box. sshArgs takes it.
var acceptNew = struct {
	sync.Mutex
	hosts map[string]bool
}{hosts: map[string]bool{}}

// takeAcceptNew says whether host's next ssh accepts a new key, and clears it.
func takeAcceptNew(host string) bool {
	acceptNew.Lock()
	defer acceptNew.Unlock()
	ok := acceptNew.hosts[host]
	delete(acceptNew.hosts, host)
	return ok
}

// acceptHostKey forgets the old key of the failed pane's host and has its
// next connection save the new one. ponytail: accept-new takes the key the
// server sends then, not one pinned to the fingerprint shown; pinning would
// mean fetching the key ourselves through the host's ProxyJump.
func acceptHostKey(host string, c *hostKeyChange) error {
	if len(c.Old) == 0 {
		return fmt.Errorf("ssh didn't say which known_hosts line holds the old key")
	}
	if err := c.forget(); err != nil {
		return err
	}
	acceptNew.Lock()
	acceptNew.hosts[host] = true
	acceptNew.Unlock()
	return nil
}
