package sshc

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// known is one known_hosts line, as hostfile.c reads it. x/crypto's
// knownhosts package isn't used to check: it refuses a whole file for one
// bad line and doesn't say which lines hold a changed key, nor can it tell
// a host's lines from its address's.
type known struct {
	file   string // "" for a KnownHostsCommand's output
	line   int
	marker string // "", "cert-authority" or "revoked"
	hosts  []string
	key    ssh.PublicKey
}

// parseKnown is data's usable lines; unreadable ones are skipped, as ssh does.
func parseKnown(data []byte, file string) []known {
	var out []known
	for i, l := range bytes.Split(data, []byte("\n")) {
		if marker, hosts, key, _, _, err := ssh.ParseKnownHosts(l); err == nil {
			out = append(out, known{file, i + 1, marker, hosts, key})
		}
	}
	return out
}

// readKnown is the lines of files that can be read.
func readKnown(files []string, logf func(string, ...any)) []known {
	var out []known
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			if !os.IsNotExist(err) {
				logf("Failed to open %s: %v", f, err)
			}
			continue
		}
		out = append(out, parseKnown(b, f)...)
	}
	return out
}

// matchHosts is hostfile.c's host match: name (lowercased) against a
// line's patterns, hashed (|1|salt|hmac) or with * and ?, a matching
// !pattern ruling the line out.
func matchHosts(pats []string, name string) bool {
	name = strings.ToLower(name)
	found := false
	for _, p := range pats {
		if strings.HasPrefix(p, "|1|") {
			found = found || hashMatch(p, name)
			continue
		}
		neg := strings.HasPrefix(p, "!")
		if wild(strings.ToLower(strings.TrimPrefix(p, "!")), name) {
			if neg {
				return false
			}
			found = true
		}
	}
	return found
}

// wild is match.c's match_pattern: * any run, ? any one byte.
func wild(p, s string) bool {
	for ; p != ""; p = p[1:] {
		switch p[0] {
		case '*':
			for i := len(s); i >= 0; i-- {
				if wild(p[1:], s[i:]) {
					return true
				}
			}
			return false
		case '?':
			if s == "" {
				return false
			}
		default:
			if s == "" || s[0] != p[0] {
				return false
			}
		}
		s = s[1:]
	}
	return s == ""
}

// hashMatch is a |1|salt|hash pattern naming name: HMAC-SHA1 of it by salt.
func hashMatch(p, name string) bool {
	f := strings.Split(p, "|")
	if len(f) != 4 {
		return false
	}
	salt, err1 := base64.StdEncoding.DecodeString(f[2])
	want, err2 := base64.StdEncoding.DecodeString(f[3])
	mac := hmac.New(sha1.New, salt)
	mac.Write([]byte(name))
	return err1 == nil && err2 == nil && hmac.Equal(mac.Sum(nil), want)
}

func keyEqual(a, b ssh.PublicKey) bool { return bytes.Equal(a.Marshal(), b.Marshal()) }

// addKnown is hostfile.c's add_host_to_hostfile: names and key appended to
// file (made, and its folder, when missing); hashed, one line per name.
func addKnown(file string, names []string, key ssh.PublicKey, hash bool) error {
	old, err := os.ReadFile(file)
	if err != nil && !os.IsNotExist(err) {
		return err // never rewrite what couldn't be read
	}
	b := bytes.NewBuffer(old)
	if len(old) > 0 && old[len(old)-1] != '\n' {
		b.WriteString("\n")
	}
	k := strings.TrimSuffix(string(ssh.MarshalAuthorizedKey(key)), "\n")
	if hash {
		for _, n := range names {
			b.WriteString(knownhosts.HashHostname(n) + " " + k + "\n")
		}
	} else {
		b.WriteString(strings.Join(names, ",") + " " + k + "\n")
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return err
	}
	return os.WriteFile(file, b.Bytes(), 0o600)
}

// KnownLine is a known_hosts line holding a key the server didn't send.
type KnownLine struct {
	File              string // "" when a KnownHostsCommand printed it
	Line              int
	Name              string // the host or address it is known under
	Type, Fingerprint string // ED25519, SHA256:…

	key ssh.PublicKey
}

// forget drops lines from their files, keeping every other line and each
// file's mode. Every line must still hold the key it was read with, for
// that name (a file edited since would point it at another host's): else
// nothing is dropped.
func forget(lines []KnownLine) error {
	byFile := map[string]map[int]bool{}
	for _, l := range lines {
		if l.File == "" {
			return fmt.Errorf("the old %s key of %s comes from KnownHostsCommand; change what it prints", l.Type, l.Name)
		}
		b, err := os.ReadFile(l.File)
		if err != nil {
			return err
		}
		ls := parseKnown(b, l.File)
		i := 0
		for i < len(ls) && ls[i].line != l.Line {
			i++
		}
		if i == len(ls) || !keyEqual(ls[i].key, l.key) || !matchHosts(ls[i].hosts, l.Name) {
			return fmt.Errorf("%s:%d no longer holds that key; nothing dropped", l.File, l.Line)
		}
		if byFile[l.File] == nil {
			byFile[l.File] = map[int]bool{}
		}
		byFile[l.File][l.Line] = true
	}
	for file, drop := range byFile {
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
			if !drop[i+1] {
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
