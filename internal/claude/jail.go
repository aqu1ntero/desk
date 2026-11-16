package claude

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// Confine says what a session working in dir mustn't see (hide: folders
// it finds empty) and, under those, what it still may (keep); desk sets
// it for its own sessions. nil: nothing is hidden.
var Confine func(dir string) (hide, keep []string)

// Jail runs cmd (claude, or bwrap with an account's profile around it)
// for a session working in dir, in a sandbox: a pid namespace of its own,
// so what it starts stays below it (desk knows its callers by their
// parents), with Confine's folders and the other accounts' profiles
// hidden, and its own folder and --add-dir folders kept. Linux with bwrap
// only; elsewhere, or without bwrap, cmd as it is.
// ponytail: macOS (sandbox-exec) and Windows have no such walls yet.
func Jail(cmd *exec.Cmd, home, dir string) *exec.Cmd {
	if NoBwrap || Confine == nil || os.Getenv("CLAUDE_PROFILE") != "" {
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
	slices.Sort(keep) // a folder around another first; each once
	keep = slices.Compact(keep)
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

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// within: p is dir or inside it.
func within(p, dir string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
