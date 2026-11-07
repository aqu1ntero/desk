package infra

import (
	"fmt"
	"strings"

	"github.com/lfdominguez/desk/internal/sshconfig"
)

// supported drops from h's algorithm lists the names desk's SSH client
// doesn't know (sshconfig.Algorithm): like ssh, it refuses a whole config over
// one (OpenSSH 10 has no ssh-dss), so one old host imported as it was would
// break every server of the organization. Each drop is noted in the block; a
// list left empty goes. A -list is kept: its names aren't checked.
func (h *asbruHost) supported() {
	lines := h.lines[:0]
	for _, l := range h.lines {
		op, v := "", l[1]
		if v != "" && strings.ContainsRune("+-^", rune(v[0])) {
			op, v = v[:1], v[1:]
		}
		if _, list := sshconfig.Algorithm(l[0], ""); !list || op == "-" {
			lines = append(lines, l)
			continue
		}
		var keep, drop []string
		for _, a := range strings.Split(v, ",") {
			if a = strings.TrimSpace(a); a == "" {
				continue
			}
			if ok, _ := sshconfig.Algorithm(l[0], a); ok {
				keep = append(keep, a)
			} else {
				drop = append(drop, a)
			}
		}
		if len(drop) == 0 {
			lines = append(lines, l)
			continue
		}
		h.notes = append(h.notes, fmt.Sprintf("%s %s%s dropped: desk's SSH client doesn't support it", l[0], op, strings.Join(drop, ",")))
		if len(keep) > 0 {
			lines = append(lines, [2]string{l[0], op + strings.Join(keep, ",")})
		}
	}
	h.lines = lines
}
