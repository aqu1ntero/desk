package clip

import (
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// fakeCompositor serves one client on a socket in dir: a seat, the wlroots
// data-control manager, and a selection offering types; a receive of an
// image type gets data written into its fd.
// A mode makes it misbehave: "error-registry" and "error-device" answer
// that request with a protocol error, "no-manager" offers no data-control,
// "hangup" stops reading once the selection is sent, "silent" keeps the
// receive's fd open without writing, "noisy" mixes in events of other
// objects and opcodes that look like the ones Image reads.
func fakeCompositor(t *testing.T, dir string, types []string, data string, mode ...string) {
	m := ""
	if len(mode) > 0 {
		m = mode[0]
	}
	protoErr := func(ev func(uint32, uint32, args)) { ev(1, 0, args{}.u(1).u(7).s("boom")) }
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(dir, "wl"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		c, err := l.AcceptUnix()
		if err != nil {
			return
		}
		defer c.Close()
		ev := func(id, op uint32, a args) {
			m := le.AppendUint32(nil, id)
			m = le.AppendUint32(m, uint32(8+len(a))<<16|op)
			c.Write(append(m, a...))
		}
		ifaces := map[uint32]string{1: "wl_display"}
		var reg uint32
		var devSent bool
		const offer = 0xff000001
		buf, oob := make([]byte, 4096), make([]byte, 64)
		for {
			n, on, _, _, err := c.ReadMsgUnix(buf, oob)
			if err != nil {
				return
			}
			var fds []int
			if msgs, _ := syscall.ParseSocketControlMessage(oob[:on]); len(msgs) > 0 {
				fds, _ = syscall.ParseUnixRights(&msgs[0])
			}
			for b := body(buf[:n]); len(b) >= 8; {
				id, word := b.u(), b.u()
				op, rest := word&0xffff, body(b[:word>>16-8])
				b = b[word>>16-8:]
				switch {
				case id == 1 && op == 1:
					reg = rest.u()
					if m == "error-registry" {
						protoErr(ev)
						continue
					}
					ev(reg, 0, args{}.u(1).s("wl_seat").u(7))
					if m != "no-manager" {
						ev(reg, 0, args{}.u(2).s("zwlr_data_control_manager_v1").u(2))
					}
					if m == "noisy" { // another object's event, shaped like a global
						ev(99, 0, args{}.u(9).s("zwlr_data_control_manager_v1").u(1))
					}
				case id == 1 && op == 0:
					if devSent && m == "hangup" {
						c.CloseRead() // the client's next request fails
					}
					ev(rest.u(), 0, args{}.u(0))
				case ifaces[id] == "manager" && op == 1 && m == "error-device":
					protoErr(ev)
				case id == reg && op == 0:
					name, _, _, nid := rest.u(), rest.s(), rest.u(), rest.u()
					ifaces[nid] = map[uint32]string{1: "seat", 2: "manager"}[name]
				case ifaces[id] == "manager" && op == 1:
					dev := rest.u()
					ev(dev, 0, args{}.u(offer))
					for _, ty := range types {
						ev(offer, 0, args{}.s(ty))
					}
					if m == "noisy" { // the offer's op 1 is no type
						ev(offer, 1, args{}.s("image/png"))
					}
					ev(dev, 1, args{}.u(offer))
					if m == "noisy" { // another object's op 1 is no selection
						ev(99, 1, args{}.u(0))
					}
					devSent = true
				case id == offer && op == 0 && len(fds) > 0:
					f := os.NewFile(uintptr(fds[0]), "pipe")
					if m == "silent" {
						t.Cleanup(func() { f.Close() })
						continue
					}
					f.WriteString(data + "|" + rest.s())
					f.Close()
				}
			}
		}
	}()
	t.Setenv("XDG_RUNTIME_DIR", dir)
	t.Setenv("WAYLAND_DISPLAY", "wl")
}

// TestImage: the selection's PNG comes through the pipe; a clipboard of
// text only is no image.
func TestImage(t *testing.T) {
	fakeCompositor(t, t.TempDir(), []string{"text/html", "image/jpeg", "image/png"}, "PNG")
	b, typ, err := Image()
	if err != nil || typ != "image/png" || string(b) != "PNG|image/png" {
		t.Fatalf("%q %q %v", b, typ, err)
	}
	fakeCompositor(t, t.TempDir(), []string{"text/plain"}, "")
	if b, typ, err := Image(); b != nil || typ != "" || err != nil {
		t.Fatalf("text clipboard: %q %q %v", b, typ, err)
	}
}

// TestImageIgnoresOtherEvents: only the registry's globals, the device's
// offer and selection and the offer's types count.
func TestImageIgnoresOtherEvents(t *testing.T) {
	fakeCompositor(t, t.TempDir(), []string{"image/jpeg"}, "JPG", "noisy")
	b, typ, err := Image()
	if err != nil || typ != "image/jpeg" || string(b) != "JPG|image/jpeg" {
		t.Fatalf("%q %q %v", b, typ, err)
	}
}

// TestNewID: ids count up from where the connection left off.
func TestNewID(t *testing.T) {
	w := &conn{next: 2}
	if a, b := w.newID(), w.newID(); a != 2 || b != 3 {
		t.Errorf("ids %d, %d, want 2, 3", a, b)
	}
}
