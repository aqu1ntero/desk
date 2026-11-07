package orgs

import "golang.org/x/sys/windows"

// wine is whether the tests run under Wine (ntdll has wine_get_version).
func wine() bool {
	return windows.NewLazySystemDLL("ntdll.dll").NewProc("wine_get_version").Find() == nil
}
