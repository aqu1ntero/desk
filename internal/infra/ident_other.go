//go:build !linux && !darwin && !windows

package infra

import "net"

// Other systems don't say who connected: callers' claims stand.
func peerPID(net.Conn) int { return 0 }
func parentPID(int) int    { return 0 }
func procStart(int) string { return "" }
