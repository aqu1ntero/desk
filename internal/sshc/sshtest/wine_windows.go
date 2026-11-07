package sshtest

import "golang.org/x/sys/windows"

// Wine is whether the tests run under Wine (ntdll has wine_get_version),
// for skips of what Wine lacks and Windows has: AF_UNIX sockets.
func Wine() bool {
	return windows.NewLazySystemDLL("ntdll.dll").NewProc("wine_get_version").Find() == nil
}
