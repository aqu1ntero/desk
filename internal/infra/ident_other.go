//go:build !linux

package infra

import "net"

// ponytail: only Linux says who connected (SO_PEERCRED); macOS
// (LOCAL_PEERPID) and Windows (SIO_AF_UNIX_GETPEERPID) can, when their
// sessions need holding to their organization too.
func peerPID(net.Conn) int { return 0 }
func parentPID(int) int    { return 0 }
func procStart(int) string { return "" }
