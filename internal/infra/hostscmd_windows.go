package infra

import deskorgs "github.com/lfdominguez/desk/internal/orgs"

// hostsCmdArgs runs a hosts-cmd through cmd /C (orgs.Shell, as token_cmd):
// a .bat, .cmd or .exe; Windows has no #! line to run a script by.
func hostsCmdArgs(path string) []string { return []string{deskorgs.Shell[0], deskorgs.Shell[1], path} }
