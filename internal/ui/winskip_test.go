package ui

import (
	"os/exec"
	"runtime"
	"testing"

	"github.com/lfdominguez/desk/internal/sshc/sshtest"
)

// onWindows is runtime.GOOS == "windows".
var onWindows = runtime.GOOS == "windows"

// needBash skips t without bash on PATH (Windows): desk demo's hosts are
// bash with canned answers.
func needBash(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("desk demo's hosts need bash:", err)
	}
}

// noUnix skips t under Wine, which has no AF_UNIX sockets (Windows 10 1803
// and later do).
func noUnix(t *testing.T) {
	t.Helper()
	if sshtest.Wine() {
		t.Skip("Wine has no AF_UNIX sockets")
	}
}

// noTMPDIR skips t on Windows, whose temp folder is TMP's, not TMPDIR's,
// which t points elsewhere.
func noTMPDIR(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Windows' temp folder isn't TMPDIR's")
	}
}
