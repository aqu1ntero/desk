package infra

import (
	"cmp"
	"os"
	"path/filepath"
)

// Windows' /etc, for changesLocal: where its hosts file lives.
func init() {
	changesEtc = filepath.Join(cmp.Or(os.Getenv("SystemRoot"), `C:\Windows`), "System32", "drivers", "etc")
}
