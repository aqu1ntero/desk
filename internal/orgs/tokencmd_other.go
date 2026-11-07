//go:build !windows

package orgs

// tokenShell runs a token_cmd.
var tokenShell = [2]string{"sh", "-c"}
