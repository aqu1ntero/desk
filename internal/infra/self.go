package infra

import "os"

// selfCmd is $DESK_SELF as desk started, read before main clears it so the
// terminals and programs desk starts don't inherit it.
var selfCmd = os.Getenv("DESK_SELF")

// Self is the command that runs desk again (desk mcp, desk peer): the
// launcher the AppImage's AppRun names in $DESK_SELF (on an old glibc the
// binary only runs through the bundled loader, and the executable is then
// that loader), else the executable.
func Self() (string, error) {
	if selfCmd != "" {
		return selfCmd, nil
	}
	return os.Executable()
}
