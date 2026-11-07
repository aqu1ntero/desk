//go:build !windows

package main

// attachConsole is Windows' (console_windows.go): elsewhere a program
// started from a terminal already writes to it.
func attachConsole() {}
