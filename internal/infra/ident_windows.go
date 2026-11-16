package infra

import (
	"net"
	"strconv"
	"unsafe"

	"golang.org/x/sys/windows"
)

// sioAFUnixGetPeerPID is SIO_AF_UNIX_GETPEERPID (Windows 10 1803's AF_UNIX).
const sioAFUnixGetPeerPID = 0x58000100

// peerPID is the pid at conn's other end; 0 when unknown.
func peerPID(conn net.Conn) int {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return 0
	}
	sc, err := uc.SyscallConn()
	if err != nil {
		return 0
	}
	var pid uint32
	sc.Control(func(fd uintptr) {
		var n uint32
		if windows.WSAIoctl(windows.Handle(fd), sioAFUnixGetPeerPID, nil, 0, (*byte)(unsafe.Pointer(&pid)), 4, &n, nil, 0) != nil {
			pid = 0
		}
	})
	return int(pid)
}

// parentPID is pid's parent, from a process snapshot; 0 when that pid
// now belongs to a process newer than pid (Windows reuses an ended
// parent's pid, and never reparents).
func parentPID(pid int) int {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0
	}
	defer windows.CloseHandle(snap)
	e := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		if int(e.ProcessID) == pid {
			parent := int(e.ParentProcessID)
			if created(parent) > created(pid) {
				return 0
			}
			return parent
		}
	}
	return 0
}

// created is pid's creation time, in 100 ns ticks; 0 when unknown.
func created(pid int) int64 {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return 0
	}
	defer windows.CloseHandle(h)
	var c, e, k, u windows.Filetime
	if windows.GetProcessTimes(h, &c, &e, &k, &u) != nil {
		return 0
	}
	return c.Nanoseconds() / 100
}

func procStart(pid int) string { return strconv.FormatInt(created(pid), 10) }
