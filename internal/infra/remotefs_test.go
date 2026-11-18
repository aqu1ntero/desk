package infra

import (
	"fmt"
	"io"
	"maps"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/lfdominguez/desk/internal/sshc/sshtest"
	"github.com/lfdominguez/desk/internal/sshconfig"
)

// TestWrap: ssh hops quote what they run (the far shell reads it again) and
// get their flag; other wrappers take the command as words; an empty
// command is the last hop's login shell.
func TestWrap(t *testing.T) {
	for _, c := range []struct {
		via       []string
		cmd, flag string
		want      string
	}{
		{nil, "sh -c 'x'", "-t", "sh -c 'x'"},
		{[]string{"sudo -n"}, "sh -c 'x'", "-t", "sudo -n sh -c 'x'"},
		{[]string{"ssh -i k a@b"}, "sh -c 'x'", "-o BatchMode=yes", `ssh -o BatchMode=yes -i k a@b 'sh -c '\''x'\'''`},
		{[]string{"ssh a@b", "ssh c@d"}, "", "-t", `ssh -t a@b 'ssh -t c@d'`},
		{[]string{"ssh a@b", "docker exec -it app sh"}, "", "-t", `ssh -t a@b 'docker exec -it app sh'`},
	} {
		if got := wrap(c.via, c.cmd, c.flag); got != c.want {
			t.Errorf("wrap(%q, %q) = %s, want %s", c.via, c.cmd, got, c.want)
		}
	}
}

// TestRoute: a host's "#@ on" chain, outermost hop first; a loop is an
// error; a group's keys don't reroute its hosts.
func TestRoute(t *testing.T) {
	org := filepath.Join(t.TempDir(), "Acme")
	dir := filepath.Join(org, infraDir)
	os.MkdirAll(dir, 0o755)
	os.WriteFile(serversTarget(org), []byte("#@ group g\n#@ on x\n#@ via ssh nope\nHost web\n    HostName 192.0.2.1\n"+
		"Host ha\n    #@ on web\n    #@ via ssh -i /root/k admin@192.0.2.9\nHost cam\n    #@ on ha\n    #@ via ssh pi@cam\n"+
		"Host l1\n    #@ on l2\n    #@ via ssh a\nHost l2\n    #@ on l1\n    #@ via ssh b\n"), 0o644)
	if h, via, err := route(dir, "cam"); h != "web" || strings.Join(via, "|") != "ssh -i /root/k admin@192.0.2.9|ssh pi@cam" || err != nil {
		t.Errorf("cam: %s %q %v", h, via, err)
	}
	if h, via, _ := route(dir, "web"); h != "web" || via != nil {
		t.Errorf("web: %s %q", h, via)
	}
	if _, _, err := route(dir, "l1"); err == nil || !strings.Contains(err.Error(), "loop") {
		t.Errorf("loop: %v", err)
	}
	if h, via, _ := route(t.TempDir(), "cam"); h != "cam" || via != nil {
		t.Errorf("not an infra dir: %s %q", h, via)
	}
}

// fakeBin writes executable scripts into a new folder: the server's PATH
// gets it first.
func fakeBin(t *testing.T, scripts map[string]string) string {
	t.Helper()
	d := t.TempDir()
	for name, body := range scripts {
		if err := os.WriteFile(filepath.Join(d, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

// fakeSSH is ssh as far as quoting goes: options and the destination
// dropped, the rest joined and read by a shell again; none is a shell.
// -W host:port prints "W host:port" and echoes its stdin back; -R is
// noted in $HOPS (FAIL_R refuses it), and the remote forward's long sleep
// a short one.
const fakeSSH = `w=
while [ $# -gt 0 ]; do case $1 in -i|-o) shift 2;; -W) w=$2; shift 2;;
  -R) [ -n "$FAIL_R" ] && { echo "remote port forwarding failed for listen port ${2%%:*}" >&2; exit 255; }; echo "R $2" >> "$HOPS"; shift 2;;
  -*) shift;; *) shift; break;; esac; done
echo "hop" >> "$HOPS"; [ -n "$w" ] && { echo "W $w"; exec cat; }
[ "$*" = "exec sleep 2147483647" ] && exec sleep 5
[ $# -eq 0 ] && exec sh -i; exec sh -c "$*"
`

// fileW is a tab with a pane on web-01 of an SSH server running this
// machine's sh (more scripts first on its PATH), its approvals answered by ok.
func fileW(t *testing.T, extra string, ok *bool, more ...map[string]string) (*sshW, *Tab, *[]*approval) {
	t.Helper()
	needSh(t)
	stateC(t)
	scripts := map[string]string{"ssh": fakeSSH, "sudo": `[ "$1" = -n ] && shift; [ -n "$SUDO_FAIL" ] && { echo "sudo: a password is required" >&2; exit 1; }; exec "$@"` + "\n"}
	for _, m := range more {
		maps.Copy(scripts, m)
	}
	bin := fakeBin(t, scripts)
	hopsFile = filepath.Join(t.TempDir(), "hops")
	w := newSSHW(t, sshtest.Opts{}, "    SetEnv PATH="+bin+":"+os.Getenv("PATH")+" HOPS="+hopsFile+extra+"\n")
	var seen []*approval
	askHook = nil
	setDispatchA(t, func(msg tea.Msg) {
		switch m := msg.(type) {
		case approvalMsg:
			seen = append(seen, m.a)
			if askHook != nil {
				askHook()
			}
			m.a.done <- approvalResult{ok: *ok}
		case func(*model):
			m(&model{})
		}
	})
	term, _ := fakeTermA(t, "web-01")
	tab := &Tab{org: "Acme", panes: []*Pane{{id: 1, term: term, name: "web-01", dir: w.dir}},
		hosts: []node{{label: "web-01", host: "web-01", dir: w.dir, org: "Acme"}}}
	return w, tab, &seen
}

func call(tab *Tab, name string, a fileArgs) toolResult { return tab.fileTool(name, a) }

// askHook runs while an approval waits (fileW's), as the user looks.
var askHook func()

// hopsFile is the last fileW's log of fake ssh hops.
var hopsFile string

// TestFileToolsRaces: the tools come through callTool; a file changed
// while the user looked isn't written; what can't be kept or written
// says so; a long line is cut.
func TestFileToolsRaces(t *testing.T) {
	ok := true
	w, tab, _ := fileW(t, "", &ok)
	p := filepath.Join(w.remote, "f")
	os.WriteFile(p, []byte(strings.Repeat("x", 2500)+"\n"), 0o644)
	r := tab.callTool(toolCall{Name: "file_read", Args: []byte(`{"path":"` + p + `"}`)})
	if r.IsError || !strings.HasSuffix(r.Text, strings.Repeat("x", 2000)+"…\n") {
		t.Fatalf("read: %+v", r)
	}
	askHook = func() { os.WriteFile(p, []byte("meanwhile\n"), 0o644) }
	if r := call(tab, "file_write", fileArgs{Path: p, Content: "y\n"}); !r.IsError || !strings.Contains(r.Text, "while the user looked") {
		t.Errorf("changed while asked: %+v", r)
	}
	askHook = nil
	call(tab, "file_read", fileArgs{Path: p})
	t.Setenv("XDG_STATE_HOME", p) // a file: no backups folder
	if r := call(tab, "file_write", fileArgs{Path: p, Content: "y\n"}); !r.IsError || !strings.Contains(r.Text, "keeping the old content failed") {
		t.Errorf("no backup: %+v", r)
	}
	stateC(t)
	if os.Getuid() != 0 {
		ro := filepath.Join(w.remote, "ro")
		os.MkdirAll(ro, 0o755)
		os.WriteFile(filepath.Join(ro, "f"), []byte("a\n"), 0o644)
		os.Chmod(ro, 0o555)
		t.Cleanup(func() { os.Chmod(ro, 0o755) })
		call(tab, "file_read", fileArgs{Path: filepath.Join(ro, "f")})
		if r := call(tab, "file_write", fileArgs{Path: filepath.Join(ro, "f"), Content: "b\n"}); !r.IsError {
			t.Errorf("written in a read-only folder: %+v", r)
		}
		locked := filepath.Join(w.remote, "locked")
		os.WriteFile(locked, []byte("x"), 0o000)
		if r := call(tab, "file_edit", fileArgs{Path: locked, OldString: "x", NewString: "y"}); !r.IsError || !strings.Contains(r.Text, "sudo: true") {
			t.Errorf("unreadable: %+v", r)
		}
	}
}

// TestFileTools: read numbered, write only what was read and is unchanged,
// edit by a unique old_string, each write approved as a diff, written
// keeping the mode, the old content kept as a backup.
func TestFileTools(t *testing.T) {
	ok := true
	start := time.Now().Add(-time.Minute)
	w, tab, seen := fileW(t, "", &ok)
	p := filepath.Join(w.remote, "etc", "app.conf")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte("port = 80\nhost = a\n"), 0o640)

	if r := call(tab, "file_edit", fileArgs{Path: p, OldString: "80", NewString: "81"}); !r.IsError || !strings.Contains(r.Text, "file_read") {
		t.Errorf("edit before reading: %+v", r)
	}
	r := call(tab, "file_read", fileArgs{Path: p})
	if r.IsError || r.Text != "web-01:"+p+" · 2 lines\n     1\tport = 80\n     2\thost = a\n" {
		t.Fatalf("read: %q", r.Text)
	}
	if r := call(tab, "file_read", fileArgs{Path: p, Offset: 2, Limit: 1}); !strings.HasSuffix(r.Text, "     2\thost = a\n") {
		t.Errorf("offset: %q", r.Text)
	}
	if r := call(tab, "file_read", fileArgs{Path: p, Limit: 1}); !strings.Contains(r.Text, "… 1 more lines: read on with offset 2") {
		t.Errorf("limit: %q", r.Text)
	}
	r = call(tab, "file_edit", fileArgs{Path: p, OldString: "80", NewString: "8080", Reason: "new port"})
	if r.IsError || !strings.HasPrefix(r.Text, "edit web-01:"+p+" (+1 −1); the old content is kept at ") {
		t.Fatalf("edit: %+v", r)
	}
	if b, _ := os.ReadFile(p); string(b) != "port = 8080\nhost = a\n" {
		t.Errorf("file: %q", b)
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o640 {
		t.Errorf("mode %v", st.Mode())
	}
	a := (*seen)[0]
	if a.file != "web-01:"+p || a.title != "Claude wants to edit a file" || a.reason != "new port" || !strings.Contains(a.cmd, "-port = 80\n+port = 8080\n") {
		t.Errorf("approval: %+v", a)
	}
	backup := strings.SplitN(r.Text, "kept at ", 2)[1]
	if b, _ := os.ReadFile(backup); string(b) != "port = 80\nhost = a\n" {
		t.Errorf("backup: %q", b)
	}
	// its own write counts as read
	if r := call(tab, "file_edit", fileArgs{Path: p, OldString: "a", NewString: "b"}); r.IsError {
		t.Errorf("edit again: %+v", r)
	}
	os.WriteFile(p, []byte("someone else\n"), 0o640)
	if r := call(tab, "file_write", fileArgs{Path: p, Content: "x"}); !r.IsError || !strings.Contains(r.Text, "changed since you read it") {
		t.Errorf("changed: %+v", r)
	}
	call(tab, "file_read", fileArgs{Path: p})
	for _, c := range []struct {
		a    fileArgs
		want string
	}{
		{fileArgs{Path: p, OldString: "zzz"}, "isn't in the file"},
		{fileArgs{Path: p, OldString: "e"}, "4 times"},
		{fileArgs{Path: p, OldString: ""}, "empty"},
		{fileArgs{Path: p, OldString: "else", NewString: "else"}, "changes nothing"},
		{fileArgs{Path: filepath.Join(w.remote, "none"), OldString: "a"}, "create it with file_write"},
	} {
		if r := call(tab, "file_edit", c.a); !r.IsError || !strings.Contains(r.Text, c.want) {
			t.Errorf("%+v: %+v", c.a, r)
		}
	}
	if r := call(tab, "file_edit", fileArgs{Path: p, OldString: "e", NewString: "E", ReplaceAll: true}); r.IsError {
		t.Errorf("replace_all: %+v", r)
	}
	// a new file: mode, folders, no backup
	n := filepath.Join(w.remote, "new", "dir", "run.sh")
	if r := call(tab, "file_write", fileArgs{Path: n, Content: "echo hi\n", Mode: "750"}); r.IsError || strings.Contains(r.Text, "kept at") {
		t.Errorf("new: %+v", r)
	}
	if st, err := os.Stat(n); err != nil || st.Mode().Perm() != 0o750 || (*seen)[len(*seen)-1].title != "Claude wants to create a file" {
		t.Errorf("new file: %v %v", st, err)
	}
	// denied: nothing written, and logged
	ok = false
	if r := call(tab, "file_write", fileArgs{Path: n, Content: "rm -rf /\n"}); !r.IsError || !strings.Contains(r.Text, "denied") {
		t.Errorf("denied: %+v", r)
	}
	if b, _ := os.ReadFile(n); string(b) != "echo hi\n" {
		t.Errorf("written anyway: %q", b)
	}
	if es := auditSince("Acme", start); len(es) == 0 || es[len(es)-1].Verdict != "denied" || es[len(es)-1].Event != "file" {
		t.Errorf("audit: %+v", es)
	}
}

// TestFileToolsRefuse: what the tools won't do, and why.
func TestFileToolsRefuse(t *testing.T) {
	ok := true
	w, tab, _ := fileW(t, "", &ok)
	defer func(n int) { fileMax = n }(fileMax)
	fileMax = b64Max + 100
	mid := filepath.Join(w.remote, "mid")
	os.WriteFile(mid, make([]byte, b64Max+1), 0o644)
	bin := filepath.Join(w.remote, "blob")
	os.WriteFile(bin, []byte("a\x00b"), 0o644)
	locked := filepath.Join(w.remote, "locked")
	os.WriteFile(locked, []byte("x"), 0o000)
	big := filepath.Join(w.remote, "big")
	os.WriteFile(big, make([]byte, fileMax+1), 0o644)
	for _, c := range []struct {
		name string
		a    fileArgs
		want string
	}{
		{"file_read", fileArgs{Path: "etc/x"}, "absolute"},
		{"file_read", fileArgs{Path: "/x", Host: "nowhere"}, "no server"},
		{"file_read", fileArgs{Path: "/x", Pane: 9}, "no pane 9"},
		{"file_read", fileArgs{Path: bin}, "encoding base64"},
		{"file_edit", fileArgs{Path: bin, OldString: "a", NewString: "b"}, "replace it whole"},
		{"file_write", fileArgs{Path: bin, Content: "!!", Encoding: "base64"}, "isn't base64"},
		{"file_write", fileArgs{Path: bin, Content: "x", Encoding: "hex"}, "base64 or none"},
		{"file_read", fileArgs{Path: w.remote}, "not a regular file"},
		{"file_read", fileArgs{Path: filepath.Join(w.remote, "gone")}, "doesn't exist"},
		{"file_read", fileArgs{Path: big}, "too big"},
		{"file_write", fileArgs{Path: bin, Content: "x"}, "binary file"},
		{"file_read", fileArgs{Path: big, Encoding: "base64"}, "too big"},
		{"file_read", fileArgs{Path: mid, Encoding: "base64"}, "more than file_read gives whole"},
		{"file_write", fileArgs{Path: "/x", Content: "x", Mode: "rw"}, "octal"},
		{"file_write", fileArgs{Path: filepath.Join(w.remote, "n"), Content: string(make([]byte, fileMax+1))}, "too big"},
		{"file_list", fileArgs{Path: "/x"}, "unknown tool"},
	} {
		if r := tab.fileTool(c.name, c.a); !r.IsError || !strings.Contains(r.Text, c.want) {
			t.Errorf("%s %+v: %+v", c.name, c.a.Path, r)
		}
	}
	if os.Getuid() != 0 { // root reads it anyway
		if r := call(tab, "file_read", fileArgs{Path: locked}); !r.IsError || !strings.Contains(r.Text, "sudo: true") {
			t.Errorf("permission: %+v", r)
		}
	}
	if r := call(tab, "file_read", fileArgs{Path: locked, Sudo: true, Host: "web-01"}); os.Getuid() != 0 && !r.IsError {
		t.Errorf("the fake sudo isn't root: %+v", r)
	}
}

// TestFileToolsBinary: a binary file goes both ways as base64, its change
// shown by size and hash; a big text file comes a window at a time.
func TestFileToolsBinary(t *testing.T) {
	ok := true
	w, tab, seen := fileW(t, "", &ok)
	p := filepath.Join(w.remote, "key.der")
	os.WriteFile(p, []byte{0, 1, 2}, 0o600)
	if r := call(tab, "file_read", fileArgs{Path: p, Encoding: "base64"}); r.IsError || !strings.HasSuffix(r.Text, "· 3 bytes, base64\nAAEC\n") {
		t.Fatalf("read: %+v", r)
	}
	if r := call(tab, "file_write", fileArgs{Path: p, Content: "AAED", Encoding: "base64"}); r.IsError {
		t.Fatalf("write: %+v", r)
	}
	if b, _ := os.ReadFile(p); string(b) != "\x00\x01\x03" {
		t.Errorf("file %q", b)
	}
	if a := (*seen)[0]; !strings.HasPrefix(a.cmd, "binary: 3 bytes → 3 bytes\n-sha256 ") {
		t.Errorf("approval %q", a.cmd)
	}
	big := filepath.Join(w.remote, "big.log")
	os.WriteFile(big, []byte(strings.Repeat(strings.Repeat("y", 1000)+"\n", 600)), 0o644)
	r := call(tab, "file_read", fileArgs{Path: big})
	if r.IsError || len(r.Text) > readMax+2100 || !strings.Contains(r.Text, "more lines: read on with offset ") {
		t.Errorf("window: %d bytes, %q", len(r.Text), r.Text[len(r.Text)-60:])
	}
}

// TestFileToolsBSDStat: a server whose stat is BSD's (-f, no -c) still
// has a file's owner and mode kept.
func TestFileToolsBSDStat(t *testing.T) {
	needSh(t)
	real, err := exec.LookPath("stat")
	if err != nil {
		t.Skip("no stat")
	}
	ok := true
	w, tab, _ := fileW(t, "", &ok, map[string]string{"stat": `[ "$1" = -c ] && exit 1
f=$2; shift 2; [ "$1" = -- ] && shift
case $f in %u:%g) exec ` + real + ` -c %u:%g -- "$1";; %Lp) exec ` + real + ` -c %a -- "$1";; esac; exit 1
`})
	p := filepath.Join(w.remote, "f")
	os.WriteFile(p, []byte("a\n"), 0o604)
	call(tab, "file_read", fileArgs{Path: p})
	if r := call(tab, "file_write", fileArgs{Path: p, Content: "b\n"}); r.IsError {
		t.Fatalf("write: %+v", r)
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o604 {
		t.Errorf("mode %v", st.Mode())
	}
}

// TestForwardVia: forwards on a server two hops past web-01 go through
// them: L and SOCKS by ssh -W on the last hop, R by ssh -R on each one.
func TestForwardVia(t *testing.T) {
	ok := true
	w, _, _ := fileW(t, "", &ok)
	defer func(d time.Duration) { hopSettle = d }(hopSettle)
	hopSettle = 300 * time.Millisecond
	f, _ := os.ReadFile(w.file)
	os.WriteFile(w.file, append(f, []byte("Host ha\n    #@ on web-01\n    #@ via ssh -i /root/k admin@192.0.2.9\nHost cam\n    #@ on ha\n    #@ via ssh pi@cam\n")...), 0o644)
	port := freePort(t)
	l := portFwd{host: "cam", dir: w.dir, name: "cam", kind: "L", spec: fmt.Sprint(port) + ":localhost:80"}
	if err := l.run(false); err != nil {
		t.Fatal(err)
	}
	defer l.run(true)
	conn, err := net.Dial("tcp", fmt.Sprint("127.0.0.1:", port))
	if err != nil {
		t.Fatal(err)
	}
	io.WriteString(conn, "ping")
	conn.(*net.TCPConn).CloseWrite()
	if b, _ := io.ReadAll(conn); string(b) != "W localhost:80\nping" {
		t.Errorf("through the hops: %q", b)
	}
	rp := freePort(t)
	r := portFwd{host: "cam", dir: w.dir, name: "cam", kind: "R", spec: fmt.Sprint(rp) + ":localhost:3000"}
	if err := r.run(false); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(hopsFile); strings.Count(string(b), fmt.Sprintf("R %d:localhost:%d\n", rp, rp)) != 2 {
		t.Errorf("hops:\n%s", b)
	}
	if err := r.run(true); err != nil {
		t.Error(err)
	}
	if _, err := hopCmd([]string{"ssh a", "docker exec -i app"}, "-W x:1"); err == nil || !strings.Contains(err.Error(), "isn't one") {
		t.Errorf("non-ssh hop: %v", err)
	}
	if _, err := hopDial(nil, []string{"ssh a"})("unix", "/run/x.sock"); err == nil {
		t.Error("a unix socket through hops")
	}
	if _, err := hopDial(nil, []string{"docker exec -i app"})("tcp", "x:1"); err == nil {
		t.Error("a forward through a non-ssh hop")
	}
}

// TestForwardViaFails: a remote forward whose hop refuses the port says
// so, and a non-ssh hop can't carry one.
func TestForwardViaFails(t *testing.T) {
	ok := true
	w, _, _ := fileW(t, " FAIL_R=1", &ok)
	f, _ := os.ReadFile(w.file)
	os.WriteFile(w.file, append(f, []byte("Host ha\n    #@ on web-01\n    #@ via ssh admin@192.0.2.9\nHost box\n    #@ on web-01\n    #@ via docker exec -i app\n"+
		"Host l1\n    #@ on l2\n    #@ via ssh a\nHost l2\n    #@ on l1\n    #@ via ssh b\n")...), 0o644)
	r := portFwd{host: "ha", dir: w.dir, name: "ha", kind: "R", spec: fmt.Sprint(freePort(t)) + ":localhost:3000"}
	if err := r.run(false); err == nil || !strings.Contains(err.Error(), "remote port forwarding failed") {
		t.Errorf("refused: %v", err)
	}
	taken, err := net.Listen("tcp", "127.0.0.1:0") // the "server" is this machine: its port is busy
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	tp := taken.Addr().(*net.TCPAddr).Port
	c, release, err := sshClient(t.Context(), "web-01", w.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := hopRemote(c, []string{"ssh -o x a"}, sshconfig.Forward{ListenPort: tp, ConnectHost: "localhost", ConnectPort: 1}); err == nil {
		t.Error("a taken port on the first server")
	}
	dead, rel, _ := sshClient(t.Context(), "web-01", w.dir)
	rel()
	dead.Close()
	if _, err := hopRemote(dead, []string{"ssh a"}, sshconfig.Forward{ListenPort: 1}); err == nil {
		t.Error("a closed connection")
	}
	b := portFwd{host: "box", dir: w.dir, name: "box", kind: "R", spec: fmt.Sprint(freePort(t)) + ":localhost:3000"}
	if err := b.run(false); err == nil || !strings.Contains(err.Error(), "isn't one") {
		t.Errorf("non-ssh: %v", err)
	}
	if err := (portFwd{host: "l1", dir: w.dir, kind: "L", spec: "1:localhost:2"}).run(false); err == nil || !strings.Contains(err.Error(), "loop") {
		t.Errorf("loop: %v", err)
	}
}

// TestFileToolsSudo: sudo -n wraps the script; a sudo that wants a
// password is sent to the user.
func TestFileToolsSudo(t *testing.T) {
	ok := true
	w, tab, seen := fileW(t, "", &ok)
	p := filepath.Join(w.remote, "f")
	os.WriteFile(p, []byte("a\n"), 0o644)
	call(tab, "file_read", fileArgs{Path: p, Sudo: true})
	if r := call(tab, "file_write", fileArgs{Path: p, Content: "b\n", Sudo: true}); r.IsError {
		t.Fatalf("sudo write: %+v", r)
	}
	if (*seen)[0].why != "as root (sudo)" {
		t.Errorf("why %q", (*seen)[0].why)
	}
	cmds := w.srv.Commands()
	if !strings.HasPrefix(cmds[len(cmds)-1], "sudo -n sh -c ") {
		t.Errorf("ran %q", cmds[len(cmds)-1])
	}
}

func TestFileToolsSudoPassword(t *testing.T) {
	ok := true
	w, tab, _ := fileW(t, " SUDO_FAIL=1", &ok)
	if r := call(tab, "file_read", fileArgs{Path: filepath.Join(w.remote, "x"), Sudo: true}); !r.IsError || !strings.Contains(r.Text, "ask the user") {
		t.Errorf("password sudo: %+v", r)
	}
}

// TestFileToolsVia: a host reached from another one runs the scripts
// through its hops, ssh quoting and all.
func TestFileToolsVia(t *testing.T) {
	ok := true
	w, tab, _ := fileW(t, "", &ok)
	f, _ := os.ReadFile(w.file)
	os.WriteFile(w.file, append(f, []byte("Host ha\n    #@ on web-01\n    #@ via ssh -i /root/k admin@192.0.2.9\nHost cam\n    #@ on ha\n    #@ via ssh pi@cam\n")...), 0o644)
	tab.hosts = append(tab.hosts, node{label: "cam", host: "cam", dir: w.dir})
	p := filepath.Join(w.remote, "it's here.txt")
	os.WriteFile(p, []byte("one\n"), 0o644)
	if r := call(tab, "file_read", fileArgs{Path: p, Host: "cam"}); r.IsError || !strings.Contains(r.Text, "1\tone") {
		t.Fatalf("read through two hops: %+v", r)
	}
	if r := call(tab, "file_edit", fileArgs{Path: p, Host: "cam", OldString: "one", NewString: "two"}); r.IsError {
		t.Fatalf("edit through two hops: %+v", r)
	}
	if b, _ := os.ReadFile(p); string(b) != "two\n" {
		t.Errorf("file %q", b)
	}
	cmds := w.srv.Commands()
	if !strings.HasPrefix(cmds[0], "ssh -o BatchMode=yes -i /root/k admin@192.0.2.9 'ssh -o BatchMode=yes pi@cam '") {
		t.Errorf("ran %q", cmds[0])
	}
	if s, c, _ := sftpFor(t.Context(), "cam", w.dir); s != nil || c != nil {
		t.Error("sftp on the first server for a host behind it")
	}
}

// TestPaneVia: a pane on a host reached from another is a terminal on the
// first server running the hops (ssh -t); a route that loops fails in the
// pane, as sshClient does.
func TestPaneVia(t *testing.T) {
	ok := true
	w, _, _ := fileW(t, "", &ok)
	f, _ := os.ReadFile(w.file)
	os.WriteFile(w.file, append(f, []byte("Host ha\n    #@ on web-01\n    #@ via ssh -i /root/k admin@192.0.2.9\n"+
		"Host l1\n    #@ on l2\n    #@ via ssh a\nHost l2\n    #@ on l1\n    #@ via ssh b\n")...), 0o644)
	term, err := NewTerm("ha", w.dir, 80, 24, func() {}, paneState{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(term.Close)
	waitS(t, "the hop", func() bool {
		return slices.Contains(w.srv.Commands(), "ssh -t -i /root/k admin@192.0.2.9") && term.Connected()
	})
	loop, err := NewTerm("l1", w.dir, 80, 24, func() {}, paneState{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(loop.Close)
	waitS(t, "the loop's error", func() bool { return loop.Err() != nil })
	if !strings.Contains(loop.Err().Error(), "loop") {
		t.Errorf("err %v", loop.Err())
	}
	if _, _, err := sshClient(t.Context(), "l1", w.dir); err == nil {
		t.Error("sshClient took a looping route")
	}
	if err := runSh(t.Context(), "l1", w.dir, "true", false, nil, nil); err == nil {
		t.Error("runSh took a looping route")
	}
}

// TestAddHost: the block goes right after the "on" server's, once
// approved; then it routes, and refusals say why.
func TestAddHost(t *testing.T) {
	ok := true
	w, tab, seen := fileW(t, "", &ok)
	f, _ := os.ReadFile(w.file)
	os.WriteFile(w.file, append(f, []byte("\nHost db\n    HostName 192.0.2.3\n")...), 0o644)
	r := tab.addHost(addHostArgs{Name: "ha", On: "web-01", Command: "ssh -i /root/k admin@192.0.2.9", Desc: "home assistant", Reason: "work on it"})
	if r.IsError || !strings.Contains(r.Text, "added ha, reached from web-01") {
		t.Fatalf("add: %+v", r)
	}
	b, _ := os.ReadFile(w.file)
	if !strings.Contains(string(b), "IdentityFile "+w.keyPath+"\nHost ha\n    #@ on web-01\n    #@ via ssh -i /root/k admin@192.0.2.9\n    #@ desc home assistant\n\nHost db") {
		t.Errorf("file:\n%s", b)
	}
	if a := (*seen)[0]; a.title != "Claude wants to add a server" || !strings.Contains(a.cmd, "+Host ha\n") || a.file != w.file {
		t.Errorf("approval: %+v", a)
	}
	if h, via, _ := route(w.dir, "ha"); h != "web-01" || len(via) != 1 {
		t.Errorf("route: %s %q", h, via)
	}
	for _, c := range []struct {
		a    addHostArgs
		want string
	}{
		{addHostArgs{Name: "a b", On: "web-01", Command: "ssh x"}, "letters"},
		{addHostArgs{Name: "x", On: "web-01", Command: ""}, "one line"},
		{addHostArgs{Name: "x", On: "web-01", Command: "ssh x\nrm -rf /"}, "one line"},
		{addHostArgs{Name: "x", On: "nowhere", Command: "ssh x"}, "no server"},
		{addHostArgs{Name: "HA", On: "web-01", Command: "ssh x"}, "already a server"},
	} {
		if r := tab.addHost(c.a); !r.IsError || !strings.Contains(r.Text, c.want) {
			t.Errorf("%+v: %+v", c.a, r)
		}
	}
	ok = false
	before, _ := os.ReadFile(w.file)
	if r := tab.addHost(addHostArgs{Name: "cam", On: "web-01", Command: "ssh pi@cam"}); !r.IsError || !strings.Contains(r.Text, "denied") {
		t.Errorf("denied: %+v", r)
	}
	if after, _ := os.ReadFile(w.file); string(after) != string(before) {
		t.Error("written anyway")
	}
	ok = true
	askHook = func() { os.WriteFile(w.file, append(before, '\n'), 0o644) }
	if r := tab.callTool(toolCall{Name: "add_host", Args: []byte(`{"name":"cam","on":"web-01","command":"ssh pi@cam"}`)}); !r.IsError || !strings.Contains(r.Text, "changed while") {
		t.Errorf("changed while asked: %+v", r)
	}
	askHook = nil
	if os.Getuid() != 0 {
		os.Chmod(w.file, 0o444)
		if r := tab.addHost(addHostArgs{Name: "cam", On: "web-01", Command: "ssh pi@cam"}); !r.IsError {
			t.Errorf("read-only file: %+v", r)
		}
		os.Chmod(w.file, 0o644)
	}
	tab.hosts = append(tab.hosts, node{label: "ghost", host: "ghost", dir: w.dir})
	if r := tab.addHost(addHostArgs{Name: "cam", On: "ghost", Command: "ssh pi@cam"}); !r.IsError || !strings.Contains(r.Text, "no Host ghost") {
		t.Errorf("no block: %+v", r)
	}
	os.Remove(w.file)
	if r := tab.addHost(addHostArgs{Name: "cam", On: "web-01", Command: "ssh pi@cam"}); !r.IsError {
		t.Errorf("no servers file: %+v", r)
	}
}

// TestPeerFile: a card's Claude hands a file over with desk peer file (its
// bytes from a file or stdin); desk stages it in the tab and tells the
// infra Claude, whose file_write staged=N places it with the card named in
// the approval; what's wrong is refused at once.
func TestPeerFile(t *testing.T) {
	noUnix(t)
	ok := true
	w, tab, seen := fileW(t, "", &ok)
	tab.orgOnly = true
	m := &model{tree: []node{{depth: 0, label: "Acme", org: "Acme", dir: w.dir}}, tabs: []*Tab{tab}}
	setDispatchA(t, func(msg tea.Msg) {
		switch x := msg.(type) {
		case peerMsg:
			x.reply <- m.peer(x.req)
		case approvalMsg:
			*seen = append(*seen, x.a)
			x.a.done <- approvalResult{ok: true}
		}
	})
	if _, err := servePeers(); err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(t.TempDir(), "app.conf")
	os.WriteFile(local, []byte("port = 81\n"), 0o644)
	dest := filepath.Join(w.remote, "etc", "app.conf")
	code, out, errOut := captureA(t, func() int {
		return peerMain([]string{"file", "acme", "--from", "DESK-3", "--host", "web-01", "--path", dest, "--mode", "640", "--why", "the new port", local})
	})
	if code != 0 || !strings.Contains(out, "Acme's Claude") {
		t.Fatalf("file: %d %q %q", code, out, errOut)
	}
	if len(tab.staged) != 1 || len(tab.peerCtx) != 1 || !strings.Contains(tab.peerCtx[0], "File #1 for web-01:"+dest+" (10 bytes), written by DESK-3. the new port\nPlace it with file_write staged=1") {
		t.Fatalf("staged %d, told %q", len(tab.staged), tab.peerCtx)
	}
	if r := call(tab, "file_write", fileArgs{Staged: 1}); r.IsError {
		t.Fatalf("place: %+v", r)
	}
	if b, _ := os.ReadFile(dest); string(b) != "port = 81\n" {
		t.Errorf("placed %q", b)
	}
	if st, _ := os.Stat(dest); st.Mode().Perm() != 0o640 {
		t.Errorf("mode %v", st.Mode())
	}
	if a := (*seen)[len(*seen)-1]; a.reason != "the new port" || !strings.Contains(a.why, "written by DESK-3's Claude") {
		t.Errorf("approval: %+v", a)
	}
	// from stdin, binary
	r, wr, _ := os.Pipe()
	wr.Write([]byte{0, 1})
	wr.Close()
	old := os.Stdin
	os.Stdin = r
	code, _, _ = captureA(t, func() int {
		return peerMain([]string{"file", "acme", "--from", "DESK-3", "--host", "web-01", "--path", dest + ".bin"})
	})
	os.Stdin = old
	if code != 0 || len(tab.staged) != 2 || string(tab.staged[1].data) != "\x00\x01" {
		t.Errorf("stdin: %d %d", code, len(tab.staged))
	}
	if r := call(tab, "file_write", fileArgs{Staged: 9}); !r.IsError || !strings.Contains(r.Text, "no staged file #9") {
		t.Errorf("unknown: %+v", r)
	}
	if code, _, errOut := captureA(t, func() int {
		return peerMain([]string{"file", "acme", "--host", "web-01", "--path", "/x", filepath.Join(t.TempDir(), "none")})
	}); code != 1 || !strings.Contains(errOut, "desk peer file:") {
		t.Errorf("no local file: %d %q", code, errOut)
	}
	defer func(n int) { fileMax = n }(fileMax)
	fileMax = 4
	for _, c := range []struct {
		r    peerReq
		want string
	}{
		{peerReq{Path: "/x"}, "needs --host"},
		{peerReq{Host: "web-01", Path: "x"}, "needs --host"},
		{peerReq{Host: "web-01", Path: "/x", Mode: "rw"}, "octal"},
		{peerReq{Host: "web-01", Path: "/x", Data: []byte("12345")}, "too big"},
		{peerReq{Host: "nowhere", Path: "/x"}, "no server"},
	} {
		c.r.Op, c.r.Org, c.r.From = "file", "acme", "DESK-3"
		if r := m.peer(c.r); !strings.Contains(r.Err, c.want) {
			t.Errorf("%+v: %+v", c.r, r)
		}
	}
}
