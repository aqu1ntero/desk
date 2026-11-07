//go:build !windows

package infra

// hostsCmdArgs runs a hosts-cmd as the program it is (its #! line says how).
func hostsCmdArgs(path string) []string { return []string{path} }
