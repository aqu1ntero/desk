package tty

import "golang.org/x/sys/windows"

// wine says the tests run under Wine, whose ConPTY has no
// ResizePseudoConsole (E_NOTIMPL as of Wine 11).
func wine() bool {
	return windows.NewLazySystemDLL("ntdll.dll").NewProc("wine_get_version").Find() == nil
}
