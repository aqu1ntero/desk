package sshc

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lfdominguez/desk/internal/sshconfig"
)

// echo is a local echo server's port (on network/addr).
func echo(t *testing.T, network, addr string) net.Listener {
	t.Helper()
	ln, err := net.Listen(network, addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { io.Copy(c, c); c.Close() }()
		}
	}()
	return ln
}

func portOf(a net.Addr) int { return a.(*net.TCPAddr).Port }

// ping says whether a round trip through network/addr echoes.
func ping(t *testing.T, network, addr string) bool {
	t.Helper()
	c, err := net.DialTimeout(network, addr, 2*time.Second)
	if err != nil {
		return false
	}
	defer c.Close()
	return roundTrip(c)
}

func roundTrip(c net.Conn) bool {
	c.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.WriteString(c, "ping\n"); err != nil {
		return false
	}
	l, err := bufio.NewReader(c).ReadString('\n')
	return err == nil && l == "ping\n"
}

func to(ln net.Listener) sshconfig.Forward {
	return sshconfig.Forward{ConnectHost: "127.0.0.1", ConnectPort: portOf(ln.Addr())}
}

func TestLocalForward(t *testing.T) {
	c, _, _ := pwClient(t, "", srvOpts{})
	f := to(echo(t, "tcp", "127.0.0.1:0"))
	f.ListenHost, f.ListenHostSet = "127.0.0.1", true
	addr, stop, err := c.Forward('L', f)
	if err != nil {
		t.Fatal(err)
	}
	if !ping(t, "tcp", addr.String()) {
		t.Fatal("no echo")
	}
	stop()
	stop()
	if ping(t, "tcp", addr.String()) {
		t.Fatal("still forwarding")
	}
}

func TestLocalForwardBind(t *testing.T) {
	for _, tc := range []struct {
		extra string
		f     sshconfig.Forward
		any   bool
	}{
		{"", sshconfig.Forward{}, false},
		{"  GatewayPorts yes\n", sshconfig.Forward{}, true},
		{"", sshconfig.Forward{ListenHostSet: true}, true},
		{"", sshconfig.Forward{ListenHost: "*", ListenHostSet: true}, true},
		{"", sshconfig.Forward{ListenHost: "localhost", ListenHostSet: true}, false},
	} {
		c, _, _ := pwClient(t, tc.extra, srvOpts{})
		ln, err := c.listenLocal(tc.f)
		if err != nil {
			t.Fatal(err)
		}
		ip := ln.Addr().(*net.TCPAddr).IP
		ln.Close()
		if ip.IsUnspecified() != tc.any {
			t.Fatalf("%q %+v: listens on %v", tc.extra, tc.f, ip)
		}
	}
}

func TestLocalForwardUnix(t *testing.T) {
	c, _, _ := pwClient(t, "  StreamLocalBindMask 0077\n", srvOpts{})
	sock := filepath.Join(t.TempDir(), "l")
	f := to(echo(t, "tcp", "127.0.0.1:0"))
	f.ListenPath, f.ListenPort = sock, sshconfig.StreamLocal
	if _, _, err := c.Forward('L', f); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(sock)
	if st.Mode().Perm() != 0o700 || !ping(t, "unix", sock) {
		t.Fatalf("mode %v", st.Mode())
	}
	// Taken: refused, unless StreamLocalBindUnlink.
	if _, _, err := c.Forward('L', f); err == nil {
		t.Fatal("listened twice")
	}
	c2, _, _ := pwClient(t, "  StreamLocalBindUnlink yes\n", srvOpts{})
	if _, _, err := c2.Forward('L', f); err != nil || !ping(t, "unix", sock) {
		t.Fatalf("unlink: %v", err)
	}
}

func TestLocalForwardToUnixAndFailures(t *testing.T) {
	c, _, w := pwClient(t, "", srvOpts{})
	sock := filepath.Join(t.TempDir(), "far")
	echo(t, "unix", sock)
	addr, _, err := c.Forward('L', sshconfig.Forward{ConnectPath: sock, ConnectPort: sshconfig.StreamLocal})
	if err != nil || !ping(t, "tcp", addr.String()) {
		t.Fatalf("to unix: %v", err)
	}
	addr, _, _ = c.Forward('L', sshconfig.Forward{ConnectHost: "127.0.0.1", ConnectPort: 1})
	if ping(t, "tcp", addr.String()) {
		t.Fatal("echo from nowhere")
	}
	time.Sleep(50 * time.Millisecond)
	if !w.logged("connect to 127.0.0.1:1 failed") {
		t.Fatalf("logs %q", w.logs)
	}
	if _, _, err := c.Forward('X', sshconfig.Forward{}); err == nil {
		t.Fatal("unknown kind")
	}
	if _, _, err := c.Forward('L', sshconfig.Forward{ListenHost: "192.0.2.1", ListenHostSet: true, ListenPort: 1}); err == nil {
		t.Fatal("listened on a foreign address")
	}
}

func TestRemoteForward(t *testing.T) {
	c, _, _ := pwClient(t, "", srvOpts{})
	addr, stop, err := c.Forward('R', to(echo(t, "tcp", "127.0.0.1:0")))
	if err != nil {
		t.Fatal(err)
	}
	if !ping(t, "tcp", "127.0.0.1:"+strconv.Itoa(portOf(addr))) {
		t.Fatal("no echo")
	}
	stop()
}

func TestRemoteForwardUnix(t *testing.T) {
	c, _, _ := pwClient(t, "", srvOpts{})
	sock := filepath.Join(t.TempDir(), "r")
	f := to(echo(t, "tcp", "127.0.0.1:0"))
	f.ListenPath = sock
	if _, _, err := c.Forward('R', f); err != nil {
		t.Fatal(err)
	}
	if !ping(t, "unix", sock) {
		t.Fatal("no echo")
	}
}

func TestRemoteForwardRefused(t *testing.T) {
	c, _, _ := pwClient(t, "", srvOpts{})
	_, _, err := c.Forward('R', sshconfig.Forward{ListenHost: "192.0.2.1", ListenHostSet: true, ListenPort: 1})
	if err == nil || !strings.Contains(err.Error(), "remote port forwarding failed for listen 192.0.2.1:1") {
		t.Fatalf("err %v", err)
	}
	_, _, err = c.Forward('R', sshconfig.Forward{ListenPath: "/nonexistent/sock"})
	if err == nil || !strings.Contains(err.Error(), "/nonexistent/sock") {
		t.Fatalf("err %v", err)
	}
}

func TestRfwdHost(t *testing.T) {
	for want, f := range map[string]sshconfig.Forward{
		"localhost": {}, "": {ListenHost: "*", ListenHostSet: true}, "192.0.2.1": {ListenHost: "192.0.2.1", ListenHostSet: true},
	} {
		if got := rfwdHost(f); got != want {
			t.Errorf("%+v: %q", f, got)
		}
	}
}

// socks5 connects through a SOCKS5 proxy at proxy to host:port.
func socks5(t *testing.T, proxy, host string, port int) (net.Conn, byte) {
	t.Helper()
	c, err := net.Dial("tcp", proxy)
	if err != nil {
		t.Fatal(err)
	}
	c.SetDeadline(time.Now().Add(2 * time.Second))
	c.Write([]byte{5, 1, 0})
	var r [10]byte
	io.ReadFull(c, r[:2])
	req := []byte{5, 1, 0}
	if ip := net.ParseIP(host); ip != nil && ip.To4() != nil {
		req = append(append(req, 1), ip.To4()...)
	} else if ip != nil {
		req = append(append(req, 4), ip...)
	} else {
		req = append(append(req, 3, byte(len(host))), host...)
	}
	req = binary.BigEndian.AppendUint16(req, uint16(port))
	c.Write(req)
	if _, err := io.ReadFull(c, r[:]); err != nil {
		return c, 0xff
	}
	return c, r[1]
}

func TestRemoteForwardToHostNamedSocks(t *testing.T) {
	// Only a forward to "socks" with no port is a SOCKS proxy.
	c, _, _ := pwClient(t, "", srvOpts{})
	addr, _, err := c.Forward('R', sshconfig.Forward{ConnectHost: "socks.invalid", ConnectPort: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, code := socks5(t, "127.0.0.1:"+strconv.Itoa(portOf(addr)), "127.0.0.1", 1); code != 0xff {
		t.Fatalf("code %d", code)
	}
	addr, _, _ = c.Forward('R', sshconfig.Forward{ConnectHost: "socks", ConnectPort: 1})
	if _, code := socks5(t, "127.0.0.1:"+strconv.Itoa(portOf(addr)), "127.0.0.1", portOf(echo(t, "tcp", "127.0.0.1:0").Addr())); code == 0 {
		t.Fatal("socks:1 served SOCKS")
	}
}

func TestDynamicForward(t *testing.T) {
	c, _, _ := pwClient(t, "", srvOpts{})
	target := portOf(echo(t, "tcp", "127.0.0.1:0").Addr())
	addr, _, err := c.Forward('D', sshconfig.Forward{})
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"127.0.0.1", "localhost"} {
		conn, code := socks5(t, addr.String(), host, target)
		if code != 0 || !roundTrip(conn) {
			t.Fatalf("%s: code %d", host, code)
		}
		conn.Close()
	}
	if _, code := socks5(t, addr.String(), "127.0.0.1", 1); code != 5 {
		t.Fatalf("refused: code %d", code)
	}
	// SOCKS4, then 4A with a name.
	for _, req := range [][]byte{
		append(binary.BigEndian.AppendUint16([]byte{4, 1}, uint16(target)), 127, 0, 0, 1, 'm', 'e', 0),
		append(append(binary.BigEndian.AppendUint16([]byte{4, 1}, uint16(target)), 0, 0, 0, 1, 0), "localhost\x00"...),
	} {
		conn, _ := net.Dial("tcp", addr.String())
		conn.SetDeadline(time.Now().Add(2 * time.Second))
		conn.Write(req)
		var r [8]byte
		io.ReadFull(conn, r[:])
		if r[1] != 0x5a || !roundTrip(conn) {
			t.Fatalf("socks4 %v: reply %v", req, r)
		}
		conn.Close()
	}
}

// socksReply is what the proxy answers req (then the end of input) with,
// till it hangs up; its dials all fail.
func socksReply(t *testing.T, req []byte) []byte {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		if c, err := ln.Accept(); err == nil {
			socks(c, func(string, string) (net.Conn, error) { return nil, fmt.Errorf("no") }, func(string, int) bool { return true }, t.Logf)
		}
	}()
	a, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.SetDeadline(time.Now().Add(2 * time.Second))
	a.Write(req)
	a.(*net.TCPConn).CloseWrite()
	out, _ := io.ReadAll(a)
	return out
}

func TestSocksRefusals(t *testing.T) {
	for _, tc := range []struct {
		req, want []byte
	}{
		{[]byte{9}, nil},
		{[]byte{}, nil},
		{[]byte{4, 2, 0, 1, 1, 1, 1, 1, 0}, nil},      // SOCKS4 BIND
		{[]byte{4, 1, 0, 1, 1, 1, 1, 1}, nil},         // no user end
		{[]byte{4, 1, 0, 1, 0, 0, 0, 1, 0, 'x'}, nil}, // 4A name without end
		{[]byte{4, 1, 0, 1, 1, 1, 1, 1, 0}, []byte{0, 0x5b, 0, 0, 0, 0, 0, 0}}, // dial fails
		{[]byte{5, 1, 2}, []byte{5, 0xff}},                                     // no "no authentication"
		{[]byte{5, 1}, []byte{5, 0xff}},
		{[]byte{5, 1, 0, 5, 2, 0, 1}, []byte{5, 0, 5, 7, 0, 1, 0, 0, 0, 0, 0, 0}},                   // BIND
		{[]byte{5, 1, 0, 4, 1, 0, 1, 1, 1, 1, 1, 0, 1}, []byte{5, 0}},                               // bad version
		{[]byte{5, 1, 0, 5, 1, 0, 9}, []byte{5, 0}},                                                 // bad address type
		{[]byte{5, 1, 0, 5, 1, 0, 3}, []byte{5, 0}},                                                 // no name length
		{[]byte{5, 1, 0, 5, 1, 0, 1, 1}, []byte{5, 0}},                                              // short address
		{[]byte{5, 1, 0, 5, 1, 0, 1, 1, 1, 1, 1, 0}, []byte{5, 0}},                                  // short port
		{[]byte{5, 1, 0, 5, 1, 0, 1, 1, 1, 1, 1, 0, 1}, []byte{5, 0, 5, 5, 0, 1, 0, 0, 0, 0, 0, 0}}, // dial fails
	} {
		if got := socksReply(t, tc.req); string(got) != string(tc.want) {
			t.Errorf("%v: %v, want %v", tc.req, got, tc.want)
		}
	}
}

func TestRemoteDynamicForward(t *testing.T) {
	target := portOf(echo(t, "tcp", "127.0.0.1:0").Addr())
	for _, tc := range []struct {
		permit string
		ok     bool
	}{
		{"", true},
		{"  PermitRemoteOpen any\n", true},
		{"  PermitRemoteOpen none\n", false},
		{"  PermitRemoteOpen 127.0.0.1:" + strconv.Itoa(target) + "\n", true},
		{"  PermitRemoteOpen 127.0.0.1:*\n", true},
		{"  PermitRemoteOpen *:" + strconv.Itoa(target) + "\n", true},
		{"  PermitRemoteOpen 192.0.2.1:*\n", false},
		{"  PermitRemoteOpen 127.0.0.1:1\n", false},
	} {
		c, _, _ := pwClient(t, tc.permit, srvOpts{})
		addr, _, err := c.Forward('R', sshconfig.Forward{ConnectHost: "socks"})
		if err != nil {
			t.Fatal(err)
		}
		conn, code := socks5(t, "127.0.0.1:"+strconv.Itoa(portOf(addr)), "127.0.0.1", target)
		if (code == 0) != tc.ok || tc.ok && !roundTrip(conn) {
			t.Fatalf("%q: code %d", tc.permit, code)
		}
		conn.Close()
	}
}

func freePort(t *testing.T) int {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return portOf(ln.Addr())
}

func TestConfigForwards(t *testing.T) {
	e := portOf(echo(t, "tcp", "127.0.0.1:0").Addr())
	l, d := freePort(t), freePort(t)
	c, _, w := pwClient(t, fmt.Sprintf("  LocalForward 127.0.0.1:%d 127.0.0.1:%d\n  DynamicForward 127.0.0.1:%d\n  RemoteForward 0 127.0.0.1:%d\n  ExitOnForwardFailure yes\n", l, e, d, e), srvOpts{})
	if w.logged("disabled") {
		t.Fatalf("logs %q", w.logs)
	}
	if !ping(t, "tcp", "127.0.0.1:"+strconv.Itoa(l)) {
		t.Fatal("LocalForward not started")
	}
	conn, code := socks5(t, "127.0.0.1:"+strconv.Itoa(d), "127.0.0.1", e)
	if code != 0 {
		t.Fatal("DynamicForward not started")
	}
	conn.Close()
	c.Close()
	if ping(t, "tcp", "127.0.0.1:"+strconv.Itoa(l)) {
		t.Fatal("forward outlived the client")
	}
	if _, _, err := c.Forward('L', sshconfig.Forward{ListenPort: freePort(t)}); err != nil {
		t.Fatal(err) // listens, but stops at once: the client is gone
	}
}

func TestForwardFailureOnConnect(t *testing.T) {
	taken := echo(t, "tcp", "127.0.0.1:0")
	spec := fmt.Sprintf("  LocalForward 127.0.0.1:%d 127.0.0.1:1\n", portOf(taken.Addr()))
	_, _, w := pwClient(t, spec, srvOpts{})
	if !w.logged("Warning: forwarding failed") {
		t.Fatalf("logs %q", w.logs)
	}
	w = newWorld(t)
	s := pwServer(t, w, srvOpts{})
	w.ans = []string{"pw"}
	if _, err := dialT(t, w.opts(base(spec+"  ExitOnForwardFailure yes\n"), s)); err == nil || !strings.Contains(err.Error(), "forwarding failed") {
		t.Fatalf("err %v", err)
	}
	w.ans = []string{"pw"}
	if _, err := dialT(t, w.opts(base(spec+"  ClearAllForwardings yes\n  ExitOnForwardFailure yes\n"), s)); err != nil {
		t.Fatal(err)
	}
}

func TestPipeHalfClose(t *testing.T) {
	a1, a2 := net.Pipe()
	ln := echo(t, "tcp", "127.0.0.1:0")
	far, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	go pipe(a2, far)
	a1.Write([]byte("x"))
	var b [1]byte
	io.ReadFull(a1, b[:])
	a1.Close()
	if b[0] != 'x' {
		t.Fatal("no echo")
	}
}
