package infra

import (
	"net"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// peerPID is the pid at conn's other end (SO_PEERCRED); 0 when unknown.
func peerPID(conn net.Conn) int {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return 0
	}
	sc, err := uc.SyscallConn()
	if err != nil {
		return 0
	}
	pid := 0
	sc.Control(func(fd uintptr) {
		if cred, err := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED); err == nil {
			pid = int(cred.Pid)
		}
	})
	return pid
}

// statFields are /proc/<pid>/stat's fields after the command's name (which
// may hold spaces and parentheses): [0] the state, [1] the parent, [19]
// the start time.
func statFields(pid int) []string {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return nil
	}
	s := string(b)
	return strings.Fields(s[strings.LastIndexByte(s, ')')+1:])
}

func parentPID(pid int) int {
	f := statFields(pid)
	if len(f) < 2 {
		return 0
	}
	n, _ := strconv.Atoi(f[1])
	return n
}

func procStart(pid int) string {
	if f := statFields(pid); len(f) > 19 {
		return f[19]
	}
	return ""
}
