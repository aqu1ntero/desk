package infra

import (
	"net"
	"sync"
)

// Owner is who one of desk's Claudes is, for desk's sockets: what it may
// act as, whatever its calls claim.
type Owner struct {
	Card string // the card tools' caller: a card's id, "ws:<id>", "draw:<path>"; "" for others
	Peer string // its name for desk peer: its card's key; "" when it has none
	Org  string // its organization
	Tab  *Tab   // an infra tab's own Claude
}

type owned struct {
	Owner
	start string // the process's start time, so a reused pid isn't taken for it
}

var owners sync.Map // pid → owned

// Own records that desk started pid as o's Claude; its children (the MCP
// server it runs, its Bash commands, desk peer) are o's too. 0 is ignored.
func Own(pid int, o Owner) {
	if pid > 0 {
		owners.Store(pid, owned{o, procStart(pid)})
	}
}

// whose is the Owner of the process at conn's other end, found through its
// parents. ok false: none of desk's Claudes started it (the user's own
// shell or Claude Code session), which keeps the rights it always had.
// sure false: this OS doesn't say who connected, so callers go on what a
// call claims.
// ponytail: same-user processes; one that leaves its Claude's tree (a
// double fork, reparented to init) counts as the user's. A pid namespace
// per bwrap (--unshare-pid) would hold it in.
func whose(conn net.Conn) (o Owner, ok, sure bool) {
	pid := peerPID(conn)
	if pid <= 0 {
		return Owner{}, false, false
	}
	for range 64 { // a chain this long is a loop
		if pid <= 1 {
			break
		}
		if v, found := owners.Load(pid); found {
			w := v.(owned)
			if w.start == procStart(pid) {
				return w.Owner, true, true
			}
			owners.Delete(pid) // ended; the pid went to another process
		}
		pid = parentPID(pid)
	}
	return Owner{}, false, true
}
