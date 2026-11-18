package sshc

import (
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/lfdominguez/desk/internal/sshc/sshtest"
	"github.com/lfdominguez/desk/internal/sshconfig"
	"github.com/lfdominguez/desk/internal/testwait"
)

// TestExecConn: a command's stdio is a connection both ways, half-close
// included; a failed command's stderr is the read error.
func TestExecConn(t *testing.T) {
	c, _, _ := pwClient(t, "", sshtest.Opts{})
	conn, err := c.ExecConn("cat")
	if err != nil {
		t.Fatal(err)
	}
	io.WriteString(conn, "hello")
	conn.(interface{ CloseWrite() error }).CloseWrite()
	if b, _ := io.ReadAll(conn); string(b) != "hello" {
		t.Errorf("echoed %q", b)
	}
	conn.Close()
	conn.Close()
	var now time.Time
	if conn.LocalAddr().Network() != "exec" || conn.RemoteAddr().String() != "exec" || conn.SetDeadline(now) != nil ||
		conn.SetReadDeadline(now) != nil || conn.SetWriteDeadline(now) != nil {
		t.Error("addresses and deadlines")
	}
	bad, _ := c.ExecConn("stderr channel 0: open failed")
	if _, err := bad.Read(make([]byte, 1)); err == nil || !strings.Contains(err.Error(), "open failed") {
		t.Errorf("read err %v", err)
	}
	quiet, _ := c.ExecConn("exit 0")
	if _, err := quiet.Read(make([]byte, 1)); err != io.EOF {
		t.Errorf("quiet end %v", err)
	}
	c.Close()
	if _, err := c.ExecConn("cat"); err == nil {
		t.Error("a closed client")
	}
	if _, _, err := c.Exec("cat"); err == nil {
		t.Error("Exec on a closed client")
	}
}

// TestExec: a command started for as long as it runs, its end with what
// it said, and stop.
func TestExec(t *testing.T) {
	c, _, _ := pwClient(t, "", sshtest.Opts{})
	stop, ended, err := c.Exec("sleep")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-ended:
		t.Fatalf("ended early: %v", err)
	default:
	}
	stop()
	<-ended
	for cmd, want := range map[string]string{"stderr port taken": "port taken", "exit 0": "ended"} {
		_, ended, _ := c.Exec(cmd)
		select {
		case err := <-ended:
			if err == nil || err.Error() != want {
				t.Errorf("%s: %v", cmd, err)
			}
		case <-time.After(testwait.Long):
			t.Fatalf("%s never ended", cmd)
		}
	}
}

// TestForwardDial: L's far end goes through the dialer given.
func TestForwardDial(t *testing.T) {
	c, _, _ := pwClient(t, "", sshtest.Opts{})
	var asked string
	addr, stop, err := c.ForwardDial('L', sshconfig.Forward{ListenPort: 0, ConnectHost: "db.internal", ConnectPort: 5432}, func(network, a string) (net.Conn, error) {
		asked = a
		return c.ExecConn("cat")
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	conn, err := net.Dial("tcp", addr.String())
	if err != nil {
		t.Fatal(err)
	}
	io.WriteString(conn, "ping")
	conn.(*net.TCPConn).CloseWrite()
	if b, _ := io.ReadAll(conn); string(b) != "ping" || asked != "db.internal:5432" {
		t.Errorf("got %q via %q", b, asked)
	}
}
