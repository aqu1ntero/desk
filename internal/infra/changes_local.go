package infra

import (
	"bufio"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// changesLogs and changesEtc are where changesLocal reads dpkg's logs and
// looks for changed files (variables for tests; Windows' etc is its own).
var changesLogs, changesEtc = "/var/log", "/etc"

// changesLocal is changesScript done in Go, for the local host under
// localGo (Windows: no sh): the same sections, each tool run directly when
// it's on PATH and its pipes (grep, awk, sort, head, tail) done here.
// ponytail: Windows' own records (installs, logons, reboots in the event
// log) aren't read; add them through wevtutil if a Windows host needs them.
func changesLocal(ctx context.Context, hours int) string {
	since := time.Now().Add(-time.Duration(hours) * time.Hour)
	ago := fmt.Sprintf("%d hours ago", hours)
	var b strings.Builder
	out := func(ls []string) {
		for _, l := range ls {
			b.WriteString(l + "\n")
		}
	}
	b.WriteString("== packages\n")
	out(lastN(sorted(dpkgSince(changesLogs, since)), 40))
	out(firstN(runLines(ctx, "rpm", "-qa", "--last"), 15))
	b.WriteString("== files changed under " + changesEtc + "\n")
	out(newerFiles(changesEtc, since, 60))
	b.WriteString("== services started, stopped, failed\n")
	out(lastN(sorted(serviceEvents(runLines(ctx, "journalctl", "--since", ago, "-q", "--no-pager", "-o", "short-iso", "_PID=1"))), 40))
	b.WriteString("== logins\n")
	out(firstN(runLines(ctx, "last", "-F", "-n", "15"), 15))
	b.WriteString("== reboots\n")
	out(firstN(runLines(ctx, "last", "-x", "-F", "reboot", "shutdown", "-n", "5"), 5))
	b.WriteString("== containers\n")
	out(firstN(runLines(ctx, "docker", "ps", "-a", "--format", "{{.Names}}  {{.Status}}  {{.Image}}"), 20))
	return b.String()
}

// runLines is a program's output lines; a missing or failing one gives what
// it printed, or nothing (the script's 2>/dev/null).
func runLines(ctx context.Context, name string, args ...string) []string {
	out, _ := exec.CommandContext(ctx, name, args...).Output()
	if s := strings.TrimRight(string(out), "\n"); s != "" {
		return strings.Split(s, "\n")
	}
	return nil
}

func firstN(ls []string, n int) []string { return ls[:min(n, len(ls))] }
func lastN(ls []string, n int) []string  { return ls[max(0, len(ls)-n):] }
func sorted(ls []string) []string        { slices.Sort(ls); return ls }

// dpkgActions are the dpkg.log lines changesScript keeps.
var dpkgActions = map[string]bool{"install": true, "upgrade": true, "remove": true, "purge": true}

// dpkgSince is the dpkg.log* lines (gzipped or not, as zcat -f) of an
// install, upgrade, remove or purge at since or later.
func dpkgSince(dir string, since time.Time) []string {
	from := since.Format("2006-01-02 15:04:05")
	logs, _ := filepath.Glob(filepath.Join(dir, "dpkg.log*"))
	var ls []string
	for _, p := range logs {
		f, err := os.Open(p)
		if err != nil {
			continue
		}
		var r io.Reader = f
		if strings.HasSuffix(p, ".gz") {
			if r, err = gzip.NewReader(f); err != nil {
				f.Close()
				continue
			}
		}
		sc := bufio.NewScanner(r)
		for sc.Scan() {
			if w := strings.Fields(sc.Text()); len(w) > 2 && dpkgActions[w[2]] && w[0]+" "+w[1] >= from {
				ls = append(ls, sc.Text())
			}
		}
		f.Close()
	}
	return ls
}

// newerFiles is up to n regular files under root modified after since
// (find -type f -newermt); unreadable folders are skipped.
func newerFiles(root string, since time.Time, n int) []string {
	var ls []string
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return nil
		}
		if fi, err := d.Info(); err == nil && fi.ModTime().After(since) {
			ls = append(ls, p)
		}
		if len(ls) == n {
			return fs.SkipAll
		}
		return nil
	})
	return ls
}

var (
	serviceEvent = regexp.MustCompile(`Started|Stopped|Reloaded|Failed|failed`)
	journalHead  = regexp.MustCompile(`^[^ ]+ [^ ]+ [^ ]+ `) // time, host, unit[pid]:
)

// serviceEvents is changesScript's awk over systemd's journal lines: each
// start, stop, reload or failure once, with its last time and how often.
func serviceEvents(journal []string) []string {
	var order []string
	n, last := map[string]int{}, map[string]string{}
	for _, l := range journal {
		if !serviceEvent.MatchString(l) {
			continue
		}
		m := journalHead.ReplaceAllString(l, "")
		if n[m] == 0 {
			order = append(order, m)
		}
		n[m]++
		last[m] = strings.Fields(l)[0]
	}
	var ls []string
	for _, m := range order {
		times := ""
		if n[m] > 1 {
			times = fmt.Sprintf("%dx ", n[m])
		}
		ls = append(ls, last[m]+"  "+times+m)
	}
	return ls
}
