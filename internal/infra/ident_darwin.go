package infra

import (
	"fmt"
	"net"

	"golang.org/x/sys/unix"
)

// peerPID is the pid at conn's other end (LOCAL_PEERPID); 0 when unknown.
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
		if p, err := unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID); err == nil {
			pid = p
		}
	})
	return pid
}

func parentPID(pid int) int {
	k, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return 0
	}
	return int(k.Eproc.Ppid)
}

func procStart(pid int) string {
	k, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return ""
	}
	t := k.Proc.P_starttime
	return fmt.Sprint(t.Sec, ".", t.Usec)
}
