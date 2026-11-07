//go:build !windows

package orgs

// wine is false off Windows.
func wine() bool { return false }
