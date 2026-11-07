package main

import (
	"os"

	"golang.org/x/sys/windows"
)

// attachConsole: desk.exe is a GUI program (-H windowsgui), so typed in
// cmd or PowerShell its output goes nowhere. Without a stdout of its own it
// attaches to the parent's console and writes there; a piped stdout (desk
// mcp, desk peer run by Claude) is kept as it is.
func attachConsole() {
	if h, err := windows.GetStdHandle(windows.STD_OUTPUT_HANDLE); err == nil && h != 0 && h != windows.InvalidHandle {
		return
	}
	const attachParent = ^uintptr(0) // ATTACH_PARENT_PROCESS
	if r, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("AttachConsole").Call(attachParent); r == 0 {
		return // started from Explorer: no console to write to
	}
	if f, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
		os.Stdout, os.Stderr = f, f
	}
}
