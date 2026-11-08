package infra

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeProcs points asbruOpen at an empty /proc of the test's, which
// procs fills: each a pid's cmdline, this user's, or another's after
// "other:", or with no status after "gone:".
func fakeProcs(t *testing.T, procs ...string) {
	dir := t.TempDir()
	p0 := asbruProcs
	asbruProcs = dir
	t.Cleanup(func() { asbruProcs = p0 })
	for i, c := range procs {
		pid := filepath.Join(dir, string(rune('1'+i)))
		uid := os.Getuid()
		if c2, ok := strings.CutPrefix(c, "other:"); ok {
			c, uid = c2, uid+1
		}
		c, gone := strings.CutPrefix(c, "gone:")
		write(t, filepath.Join(pid, "cmdline"), strings.ReplaceAll(c, " ", "\x00"))
		if !gone {
			// another user's has this user's uid second (effective): only the real one counts
			write(t, filepath.Join(pid, "status"), fmt.Sprintf("Name:\tx\nUid:\t%d\t%d\t%d\t%d\n", uid, os.Getuid(), uid, uid))
		}
	}
	os.WriteFile(filepath.Join(dir, "uptime"), []byte("1 1"), 0o644) // not a process
	os.MkdirAll(filepath.Join(dir, "self-less"), 0o755)              // a process gone: no cmdline
}

// TestAsbruOpen: Ásbrú runs as its program or as perl's script, after
// perl's options; desk's own import-asbru, a program further in the
// arguments, another user's Ásbrú, one gone or nothing are not it.
func TestAsbruOpen(t *testing.T) {
	for _, c := range []struct {
		procs []string
		open  bool
	}{
		{nil, false},
		{[]string{"/usr/bin/desk servers import-asbru", "bash -c vim /opt/asbru/asbru-cm"}, false},
		{[]string{"/opt/asbru/asbru-cm"}, true},
		{[]string{"bash", "/usr/bin/perl /opt/asbru/asbru-cm --no-splash"}, true},
		{[]string{"perl ./asbru-cm.pl"}, true},
		{[]string{"/usr/bin/perl -w -CS /opt/asbru/asbru-cm"}, true},
		{[]string{"perl -w ./other.pl /opt/asbru/asbru-cm", "perl -w"}, false},
		{[]string{"other:/opt/asbru/asbru-cm"}, false},
		{[]string{"gone:/opt/asbru/asbru-cm"}, false},
	} {
		fakeProcs(t, c.procs...)
		if got := asbruOpen(); got != c.open {
			t.Errorf("%q: %v", c.procs, got)
		}
	}
}

// TestAsbruCurrent: Ásbrú's own file while it runs is refused, by import and
// print alike; an export, or Ásbrú closed, is read.
func TestAsbruCurrent(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("ASBRU_CFG", cfg)
	own := filepath.Join(cfg, "asbru.yml")
	write(t, own, asbruSample)
	export := writeTemp(t, asbruSample)
	fakeProcs(t, "/opt/asbru/asbru-cm")
	if err := AsbruCurrent(own + "/"); !errors.Is(err, ErrAsbruOpen) {
		t.Errorf("own file: %v", err)
	}
	if err := AsbruCurrent(export); err != nil {
		t.Errorf("an export: %v", err)
	}
	org := filepath.Join(t.TempDir(), "Acme")
	if _, _, _, err := ImportAsbru(org, own, ""); !errors.Is(err, ErrAsbruOpen) {
		t.Errorf("import: %v", err)
	}
	if _, err := os.Stat(serversTarget(org)); err == nil {
		t.Error("an import refused wrote the file")
	}
	var out, errw bytes.Buffer
	if c := ServersMain([]string{"import-asbru"}, &out, &errw); c != 1 || out.Len() != 0 || !strings.Contains(errw.String(), "Ásbrú is open") {
		t.Errorf("print: %d %q %q", c, out.String(), errw.String())
	}
	if _, added, _, err := ImportAsbru(org, export, ""); err != nil || len(added) == 0 {
		t.Errorf("an export while it runs: %q %v", added, err)
	}
	fakeProcs(t)
	if err := AsbruCurrent(own); err != nil {
		t.Errorf("closed: %v", err)
	}
}
