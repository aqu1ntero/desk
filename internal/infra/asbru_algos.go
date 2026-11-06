package infra

import (
	"fmt"
	"os/exec"
	"strings"
)

// sshAlgoKind is the `ssh -Q` query that lists what each algorithm-list
// keyword takes (lower-cased keywords: ssh's are case-insensitive).
var sshAlgoKind = map[string]string{
	"hostkeyalgorithms": "key-sig", "pubkeyacceptedalgorithms": "key-sig", "pubkeyacceptedkeytypes": "key-sig",
	"hostbasedacceptedalgorithms": "key-sig", "hostbasedkeytypes": "key-sig", "casignaturealgorithms": "key-sig",
	"kexalgorithms": "kex", "ciphers": "cipher", "macs": "mac",
}

// sshQuery lists what the local ssh supports of kind (`ssh -Q kind`), nil
// when it can't say (no ssh, or one too old for the query). Tests replace it.
var sshQuery = func(kind string) []string {
	out, err := exec.Command("ssh", "-Q", kind).Output()
	if err != nil {
		return nil
	}
	return strings.Fields(string(out))
}

// supported drops from h's algorithm lists the names the local ssh lacks:
// ssh refuses a whole config over one it doesn't know (OpenSSH 10 has no
// ssh-dss), so one old host imported as it was would break every server of
// the organization. Each drop is noted in the block; a list left empty goes.
// Patterns (*, ?, !) are kept. known caches the queries over one import; a
// kind ssh can't list is kept as it is.
func (h *asbruHost) supported(known map[string]map[string]bool) {
	lines := h.lines[:0]
	for _, l := range h.lines {
		kind := sshAlgoKind[strings.ToLower(l[0])]
		if kind == "" {
			lines = append(lines, l)
			continue
		}
		have, ok := known[kind]
		if !ok {
			if names := sshQuery(kind); len(names) > 0 {
				have = map[string]bool{}
				for _, n := range names {
					have[n] = true
				}
			}
			known[kind] = have
		}
		op, v := "", l[1]
		if v != "" && strings.ContainsRune("+-^", rune(v[0])) {
			op, v = v[:1], v[1:]
		}
		var keep, drop []string
		for _, a := range strings.Split(v, ",") {
			if a = strings.TrimSpace(a); a == "" {
				continue
			}
			if have == nil || have[a] || strings.ContainsAny(a, "*?!") {
				keep = append(keep, a)
			} else {
				drop = append(drop, a)
			}
		}
		if len(drop) == 0 {
			lines = append(lines, l)
			continue
		}
		h.notes = append(h.notes, fmt.Sprintf("%s %s%s dropped: this ssh doesn't support it (ssh -Q %s)", l[0], op, strings.Join(drop, ","), kind))
		if len(keep) > 0 {
			lines = append(lines, [2]string{l[0], op + strings.Join(keep, ",")})
		}
	}
	h.lines = lines
}
