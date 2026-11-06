// desk is a personal work manager: a Kanban board of cards, in a Gio window.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"

	"gioui.org/app"

	"github.com/lfdominguez/desk/internal/infra"
	"github.com/lfdominguez/desk/internal/kanban"
	"github.com/lfdominguez/desk/internal/ui"
)

// runUI, appMain and exit are the window and the process; tests replace them.
var (
	runUI   = ui.Run
	appMain = app.Main
	exit    = os.Exit
)

// version is set at build time (-ldflags "-X main.version=2026.11.03");
// a plain go build says its commit instead (versionOf).
var version = ""

func main() { exit(run(os.Args)) }

// versionOf is v, else the commit the binary was built from ("dev-1a2b3c4",
// "-dirty" with local changes), else "dev".
func versionOf(v string, bi *debug.BuildInfo) string {
	if v != "" {
		return v
	}
	rev, dirty := "", ""
	for _, s := range bi.Settings {
		switch {
		case s.Key == "vcs.revision" && len(s.Value) >= 7:
			rev = "-" + s.Value[:7]
		case s.Key == "vcs.modified" && s.Value == "true":
			dirty = "-dirty"
		}
	}
	return "dev" + rev + dirty
}

// fixShell makes SHELL a shell that is there. desk hands it to all it runs,
// and a launcher's /bin/bash (NixOS has none) broke them: ssh runs a host's
// ProxyCommand or ProxyJump with it, Claude Code its Bash tool. The shell of
// that name on PATH takes its place, else bash; none found leaves it as is.
func fixShell() {
	s := os.Getenv("SHELL")
	if s == "" {
		return
	}
	if fi, err := os.Stat(s); err == nil && !fi.IsDir() {
		return
	}
	for _, name := range []string{filepath.Base(s), "bash"} {
		if p, err := exec.LookPath(name); err == nil {
			os.Setenv("SHELL", p)
			return
		}
	}
}

// run is desk with args (os.Args); it returns the exit code, or never
// returns once the window is up (app.Main).
func run(args []string) int {
	bi, _ := debug.ReadBuildInfo()
	if bi == nil {
		bi = &debug.BuildInfo{}
	}
	ui.Version = versionOf(version, bi)
	fixShell()
	if len(args) == 2 && (args[1] == "--version" || args[1] == "version") {
		fmt.Println("desk", ui.Version)
		return 0
	}
	// desk runs itself for the infra workspace: as a tab Claude's MCP server,
	// and as the peer CLI other Claude sessions use.
	if len(args) >= 2 {
		switch args[1] {
		case "mcp":
			infra.MCPMain(args[2:])
			return 0
		case "peer":
			return infra.PeerMain(args[2:])
		case "servers": // import-asbru: Ásbrú's SSH hosts as a servers file
			return infra.ServersMain(args[2:], os.Stdout, os.Stderr)
		case "demo": // made-up organizations and scripted sessions, for recordings
			dir, err := ui.Demo()
			if err != nil {
				fmt.Fprintln(os.Stderr, "desk demo:", err)
				return 1
			}
			fmt.Fprintln(os.Stderr, "desk demo: in", dir)
		case "demo-infra": // the demo's scripted infra Claude, run by its workspace
			if len(args) > 2 {
				ui.DemoInfra(args[2], os.Stdin, os.Stdout)
			}
			return 0
		}
	}
	path := kanban.Path()
	b, err := kanban.Load(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "desk:", err)
		return 1
	}
	// A crash leaves its trace next to the board: desk runs detached, with no
	// terminal to print it on.
	if f, err := os.OpenFile(filepath.Join(filepath.Dir(path), "crash.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
		debug.SetCrashOutput(f, debug.CrashOptions{})
		f.Close()
	}
	go func() {
		code := 0
		if err := runUI(b, path); err != nil {
			fmt.Fprintln(os.Stderr, "desk:", err)
			code = 1
		}
		exit(code)
	}()
	appMain()
	return 0 // app.Main never returns
}
