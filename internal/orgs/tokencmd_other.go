//go:build !windows

package orgs

// Shell runs a command line (a token_cmd, infra's hosts-cmd).
var Shell = [2]string{"sh", "-c"}
