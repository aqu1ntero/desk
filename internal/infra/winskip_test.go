package infra

import (
	"os/exec"
	"runtime"
	"testing"

	"github.com/lfdominguez/desk/internal/sshc/sshtest"
)

// exeSuffix is executables' suffix: Windows' PATH finds claude.exe only.
var exeSuffix = map[bool]string{true: ".exe"}[runtime.GOOS == "windows"]

// needSh skips t without sh on PATH (Windows, unless Git's is there): its
// fixtures are POSIX scripts and tools (true, sleep, bash, a fake claude).
func needSh(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("needs sh and the POSIX tools:", err)
	}
}

// wine is whether the tests run under Wine.
func wine() bool { return sshtest.Wine() }

// noUnix skips t under Wine, which has no AF_UNIX sockets (Windows 10 1803
// and later do): the tools, cards and peer sockets.
func noUnix(t *testing.T) {
	t.Helper()
	if wine() {
		t.Skip("Wine has no AF_UNIX sockets")
	}
}

// skipOnWindows skips t on Windows, saying why: what it checks isn't there
// (bwrap, mode bits).
func skipOnWindows(t *testing.T, why string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip(why)
	}
}
