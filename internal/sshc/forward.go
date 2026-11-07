package sshc

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/lfdominguez/desk/internal/sshconfig"
)

// Forward starts a forward, as -L ('L'), -R ('R') or -D ('D') would: L
// and D listen here (a Unix socket for a ListenPath), R on the server
// (RemoteForward to "socks" is a SOCKS proxy here, PermitRemoteOpen
// limiting it). addr is where it listens (the server's allocated port for
// R with port 0). stop ends it; closing the client does too.
func (c *Client) Forward(kind byte, f sshconfig.Forward) (addr net.Addr, stop func(), err error) {
	var ln net.Listener
	var handle func(net.Conn)
	switch kind {
	case 'L', 'D':
		if ln, err = c.listenLocal(f); err != nil {
			return nil, nil, err
		}
		handle = func(conn net.Conn) { socks(conn, c.Dial, func(string, int) bool { return true }, c.opt.Logf) }
		if kind == 'L' {
			handle = func(conn net.Conn) { c.relay(conn, c.Dial, f) }
		}
	case 'R':
		if f.ListenPath != "" {
			ln, err = c.ListenUnix(f.ListenPath)
		} else {
			ln, err = c.Listen("tcp", net.JoinHostPort(rfwdHost(f), strconv.Itoa(f.ListenPort)))
		}
		if err != nil {
			return nil, nil, fmt.Errorf("remote port forwarding failed for listen %s: %w", listenString(f), err)
		}
		handle = func(conn net.Conn) { c.relay(conn, net.Dial, f) }
		if f.ConnectHost == "socks" && f.ConnectPort == 0 && f.ConnectPath == "" {
			handle = func(conn net.Conn) { socks(conn, net.Dial, c.remoteOpen, c.opt.Logf) }
		}
	default:
		return nil, nil, fmt.Errorf("unknown forward kind %q", kind)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go handle(conn)
		}
	}()
	var once sync.Once
	stop = func() { once.Do(func() { ln.Close() }) }
	c.onClose(stop)
	return ln.Addr(), stop, nil
}

func listenString(f sshconfig.Forward) string {
	if f.ListenPath != "" {
		return f.ListenPath
	}
	return net.JoinHostPort(f.ListenHost, strconv.Itoa(f.ListenPort))
}

// rfwdHost is channels.c's channel_rfwd_bind_host: none given is
// localhost, "" or * every address.
func rfwdHost(f sshconfig.Forward) string {
	switch {
	case !f.ListenHostSet:
		return "localhost"
	case f.ListenHost == "*":
		return ""
	}
	return f.ListenHost
}

// listenLocal is channel_setup_fwd_listener's bind: loopback unless
// GatewayPorts (or "" or * given); a Unix socket with StreamLocalBindUnlink
// and StreamLocalBindMask.
func (c *Client) listenLocal(f sshconfig.Forward) (net.Listener, error) {
	if f.ListenPath != "" {
		if c.cfg.Bool("streamlocalbindunlink") {
			os.Remove(f.ListenPath)
		}
		ln, err := net.Listen("unix", f.ListenPath)
		if err != nil {
			return nil, err
		}
		mask, _ := strconv.ParseUint(c.cfg.Get("streamlocalbindmask"), 8, 32)
		os.Chmod(f.ListenPath, os.FileMode(0o777&^mask))
		return ln, nil
	}
	host := "localhost"
	switch {
	case !f.ListenHostSet && c.cfg.Bool("gatewayports"), f.ListenHostSet && (f.ListenHost == "" || f.ListenHost == "*"):
		host = ""
	case f.ListenHostSet:
		host = f.ListenHost
	}
	return net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(f.ListenPort)))
}

// relay connects conn to the forward's far end through dial.
func (c *Client) relay(conn net.Conn, dial func(network, addr string) (net.Conn, error), f sshconfig.Forward) {
	network, addr := "tcp", net.JoinHostPort(f.ConnectHost, strconv.Itoa(f.ConnectPort))
	if f.ConnectPath != "" {
		network, addr = "unix", f.ConnectPath
	}
	far, err := dial(network, addr)
	if err != nil {
		c.opt.Logf("connect to %s failed: %v", addr, err)
		conn.Close()
		return
	}
	pipe(conn, far)
}

// remoteOpen is PermitRemoteOpen for a remote SOCKS forward: "any", "none",
// or host:port patterns (* for either part).
func (c *Client) remoteOpen(host string, port int) bool {
	for _, p := range strings.Fields(c.cfg.Get("permitremoteopen")) {
		switch p {
		case "any":
			return true
		case "none":
			return false
		}
		h, ps, err := net.SplitHostPort(p)
		if err == nil && (h == "*" || strings.EqualFold(h, host)) && (ps == "*" || ps == strconv.Itoa(port)) {
			return true
		}
	}
	return false
}

// pipe copies both ways until both sides end, passing half-closes on.
func pipe(a, b net.Conn) {
	var wg sync.WaitGroup
	cp := func(dst, src net.Conn) {
		defer wg.Done()
		io.Copy(dst, src)
		if cw, ok := dst.(interface{ CloseWrite() error }); ok {
			cw.CloseWrite()
		} else {
			dst.Close()
		}
	}
	wg.Add(2)
	go cp(a, b)
	go cp(b, a)
	wg.Wait()
	a.Close()
	b.Close()
}

// bufConn is a conn whose first bytes a bufio.Reader already took.
type bufConn struct {
	net.Conn
	r *bufio.Reader
}

func (b *bufConn) Read(p []byte) (int, error) { return b.r.Read(p) }

func (b *bufConn) CloseWrite() error {
	if cw, ok := b.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return b.Conn.Close()
}

// socks is channels.c's SOCKS4/4A and SOCKS5 (no authentication, CONNECT
// only) server on conn, dialing through dial what allowed lets.
func socks(conn net.Conn, dial func(network, addr string) (net.Conn, error), allowed func(string, int) bool, logf func(string, ...any)) {
	br := bufio.NewReader(conn)
	fail := func(msg string) {
		logf("dynamic forward: %s", msg)
		conn.Close()
	}
	ver, err := br.ReadByte()
	if err != nil {
		conn.Close()
		return
	}
	var host string
	var port int
	var reply func(ok bool)
	switch ver {
	case 4:
		var h [7]byte
		if _, err := io.ReadFull(br, h[:]); err != nil || h[0] != 1 {
			fail("bad SOCKS4 request")
			return
		}
		port = int(binary.BigEndian.Uint16(h[1:3]))
		host = net.IP(h[3:7]).String()
		if _, err := br.ReadString(0); err != nil {
			fail("bad SOCKS4 user")
			return
		}
		if h[3] == 0 && h[4] == 0 && h[5] == 0 && h[6] != 0 { // 4A: the name follows
			if host, err = br.ReadString(0); err != nil {
				fail("bad SOCKS4A host")
				return
			}
			host = strings.TrimSuffix(host, "\x00")
		}
		reply = func(ok bool) {
			code := byte(0x5b)
			if ok {
				code = 0x5a
			}
			conn.Write([]byte{0, code, 0, 0, 0, 0, 0, 0})
		}
	case 5:
		n, err := br.ReadByte()
		methods := make([]byte, n)
		if err == nil {
			_, err = io.ReadFull(br, methods)
		}
		if err != nil || !strings.ContainsRune(string(methods), 0) {
			conn.Write([]byte{5, 0xff})
			fail("no acceptable SOCKS5 method")
			return
		}
		conn.Write([]byte{5, 0})
		var h [4]byte
		if _, err := io.ReadFull(br, h[:]); err != nil || h[0] != 5 {
			fail("bad SOCKS5 request")
			return
		}
		reply = func(ok bool) {
			code := byte(5) // connection refused
			if ok {
				code = 0
			}
			conn.Write([]byte{5, code, 0, 1, 0, 0, 0, 0, 0, 0})
		}
		if h[1] != 1 {
			conn.Write([]byte{5, 7, 0, 1, 0, 0, 0, 0, 0, 0})
			fail("SOCKS5 command not supported")
			return
		}
		var addr []byte
		switch h[3] {
		case 1:
			addr = make([]byte, 4)
		case 4:
			addr = make([]byte, 16)
		case 3:
			l, err := br.ReadByte()
			if err != nil {
				fail("bad SOCKS5 name")
				return
			}
			addr = make([]byte, l)
		default:
			fail("bad SOCKS5 address type")
			return
		}
		var p [2]byte
		if _, err := io.ReadFull(br, addr); err != nil {
			fail("bad SOCKS5 address")
			return
		}
		if _, err := io.ReadFull(br, p[:]); err != nil {
			fail("bad SOCKS5 port")
			return
		}
		host, port = string(addr), int(binary.BigEndian.Uint16(p[:]))
		if h[3] != 3 {
			host = net.IP(addr).String()
		}
	default:
		fail(fmt.Sprintf("unknown SOCKS version %d", ver))
		return
	}
	if !allowed(host, port) {
		reply(false)
		fail(fmt.Sprintf("%s:%d not permitted by PermitRemoteOpen", host, port))
		return
	}
	far, err := dial("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		reply(false)
		fail(fmt.Sprintf("connect to %s:%d failed: %v", host, port, err))
		return
	}
	reply(true)
	pipe(&bufConn{conn, br}, far)
}
