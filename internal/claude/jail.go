package claude

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// Confine says what a session working in dir mustn't see (hide: folders
// it finds empty) and, under those, what it still may (keep); desk sets
// it for its own sessions. nil: nothing is hidden.
var Confine func(dir string) (hide, keep []string)

// Jail runs cmd (claude, or bwrap with an account's profile around it)
// for a session working in dir, in a sandbox, with Confine's folders and
// the other accounts' profiles walled off, and its own folder and
// --add-dir folders kept. On Linux bwrap's: the walls are empty folders,
// and a pid namespace of its own keeps what it starts below it (desk knows
// its callers by their parents). On macOS sandbox-exec's: the walls can't
// be read or written. Without either, or elsewhere, cmd as it is.
// ponytail: Windows has no walls: nothing built in confines one program's
// files short of an AppContainer, which would have to be granted all of
// Claude Code's own.
func Jail(cmd *exec.Cmd, home, dir string) *exec.Cmd {
	if Confine == nil || os.Getenv("CLAUDE_PROFILE") != "" || NoBwrap && !seatbelt {
		return cmd
	}
	hide, keep := Confine(dir)
	hide = append(hide, filepath.Join(home, "Secure", "Claudes")) // the accounts' sessions; a profile's own is bound before
	keep = append(keep, dir)
	for _, a := range cmd.Args {
		if d, ok := strings.CutPrefix(a, "--add-dir="); ok {
			keep = append(keep, d)
		}
	}
	for _, kv := range cmd.Env { // macOS: a profile is its config folder, inside the profiles'
		if d, ok := strings.CutPrefix(kv, "CLAUDE_CONFIG_DIR="); ok {
			keep = append(keep, d)
		}
	}
	slices.Sort(keep) // a folder around another first; each once
	keep = slices.Compact(keep)
	if seatbelt {
		return sandboxed(cmd, hide, keep)
	}
	args := []string{"--unshare-pid"}
	var hidden []string
	for _, h := range hide {
		if isDir(h) {
			args, hidden = append(args, "--tmpfs", h), append(hidden, h)
		}
	}
	for _, k := range keep {
		if isDir(k) && slices.ContainsFunc(hidden, func(h string) bool { return within(k, h) }) {
			args = append(args, "--bind", k, k)
		}
	}
	if filepath.Base(cmd.Path) == "bwrap" { // a profile: before its command
		at := slices.Index(cmd.Args, "--")
		cmd.Args = slices.Concat(cmd.Args[:at], args, cmd.Args[at:])
		return cmd
	}
	bwrap := bwrapBin()
	if bwrap == "" {
		return cmd
	}
	c := exec.Command(bwrap, slices.Concat([]string{"--dev-bind", "/", "/"}, args, []string{"--", cmd.Path}, cmd.Args[1:])...)
	c.Env, c.Dir = cmd.Env, cmd.Dir
	return c
}

// seatbelt: macOS walls sessions in with sandbox-exec (Seatbelt), at
// sandboxExec; variables so tests reach it on any OS.
var (
	seatbelt    = runtime.GOOS == "darwin"
	sandboxExec = "/usr/bin/sandbox-exec"
)

// sandboxed is cmd under sandbox-exec: everything allowed but reading or
// writing in hide's folders, apart from keep's (Seatbelt's last matching
// rule wins). Its children inherit the sandbox.
func sandboxed(cmd *exec.Cmd, hide, keep []string) *exec.Cmd {
	paths := func(ps []string) string {
		var b strings.Builder
		for _, p := range ps {
			if isDir(p) {
				if r, err := filepath.EvalSymlinks(p); err == nil { // Seatbelt matches real paths: /var is /private/var
					p = r
				}
				b.WriteString(` (subpath "` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(p) + `")`)
			}
		}
		return b.String()
	}
	profile := "(version 1)\n(allow default)\n"
	if h := paths(hide); h != "" {
		profile += "(deny file-read* file-write*" + h + ")\n"
		if k := paths(keep); k != "" {
			profile += "(allow file-read* file-write*" + k + ")\n"
		}
	}
	c := exec.Command(sandboxExec, slices.Concat([]string{"-p", profile, cmd.Path}, cmd.Args[1:])...)
	c.Env, c.Dir = cmd.Env, cmd.Dir
	return c
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// within: p is dir or inside it.
func within(p, dir string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
