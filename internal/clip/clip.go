// Package clip reads an image from the Wayland clipboard. Gio's clipboard
// reads only text, so this speaks the Wayland wire protocol itself, over
// the compositor's socket: the data-control extension (ext-data-control-v1,
// or wlroots' zwlr-data-control-v1) hands the selection to a client
// without a window or focus. No libraries, no wl-paste.
package clip

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
)

var le = binary.LittleEndian // the wire is the host's order; every Linux desktop is little-endian

// managers are the data-control globals, preferred first; their requests
// and events have the same opcodes.
var managers = []string{"ext_data_control_manager_v1", "zwlr_data_control_manager_v1"}

// maxImage bounds what a clipboard owner may hand over.
const maxImage = 64 << 20

// pipe is os.Pipe; tests make it fail.
var pipe = os.Pipe

// Image is the clipboard's image and its type (image/png preferred), or
// nil when it holds no image.
func Image() ([]byte, string, error) {
	w, err := dial()
	if err != nil {
		return nil, "", err
	}
	defer w.c.Close()
	w.c.SetDeadline(time.Now().Add(3 * time.Second))

	reg := w.newID()
	w.send(1, 1, args{}.u(reg), -1) // wl_display.get_registry
	type global struct{ name, version uint32 }
	globals := map[string]global{}
	if err := w.roundtrip(func(id, op uint32, b body) {
		if id == reg && op == 0 { // wl_registry.global
			name, iface, ver := b.u(), b.s(), b.u()
			globals[iface] = global{name, ver}
		}
	}); err != nil {
		return nil, "", err
	}
	seat, ok := globals["wl_seat"]
	i := slices.IndexFunc(managers, func(m string) bool { _, ok := globals[m]; return ok })
	if !ok || i < 0 {
		return nil, "", errors.New("the compositor has no clipboard data-control protocol")
	}
	bind := func(iface string, g global) uint32 {
		id := w.newID()
		w.send(reg, 0, args{}.u(g.name).s(iface).u(1).u(id), -1) // wl_registry.bind, version 1
		return id
	}
	seatID, mgr := bind("wl_seat", seat), bind(managers[i], globals[managers[i]])
	dev := w.newID()
	w.send(mgr, 1, args{}.u(dev).u(seatID), -1) // get_data_device

	offers := map[uint32][]string{}
	var sel uint32
	if err := w.roundtrip(func(id, op uint32, b body) {
		switch {
		case id == dev && op == 0: // data_offer: a new offer, its types follow
			offers[b.u()] = nil
		case id == dev && op == 1: // selection: the clipboard's offer, 0 empty
			sel = b.u()
		case op == 0 && hasKey(offers, id): // offer.offer: one of its types
			offers[id] = append(offers[id], b.s())
		}
	}); err != nil {
		return nil, "", err
	}
	typ := pick(offers[sel])
	if sel == 0 || typ == "" {
		return nil, "", nil
	}
	pr, pw, err := pipe()
	if err != nil {
		return nil, "", err
	}
	defer pr.Close()
	err = w.send(sel, 0, args{}.s(typ), int(pw.Fd())) // offer.receive: the owner writes into the pipe
	pw.Close()
	if err != nil {
		return nil, "", err
	}
	pr.SetReadDeadline(time.Now().Add(5 * time.Second))
	data, err := io.ReadAll(io.LimitReader(pr, maxImage))
	if err != nil {
		return nil, "", fmt.Errorf("reading the clipboard: %w", err)
	}
	return data, typ, nil
}

func hasKey(m map[uint32][]string, k uint32) bool { _, ok := m[k]; return ok }

// pick is the image type to ask for: PNG when offered, else any image.
func pick(types []string) string {
	if slices.Contains(types, "image/png") {
		return "image/png"
	}
	if i := slices.IndexFunc(types, func(t string) bool { return strings.HasPrefix(t, "image/") }); i >= 0 {
		return types[i]
	}
	return ""
}

type conn struct {
	c    *net.UnixConn
	next uint32
}

// dial connects to $WAYLAND_DISPLAY (a name in $XDG_RUNTIME_DIR, or a path).
func dial() (*conn, error) {
	name := os.Getenv("WAYLAND_DISPLAY")
	if name == "" {
		name = "wayland-0"
	}
	if !filepath.IsAbs(name) {
		name = filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), name)
	}
	c, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: name, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("no Wayland clipboard: %w", err)
	}
	return &conn{c: c, next: 2}, nil // 1 is wl_display
}

func (w *conn) newID() uint32 { w.next++; return w.next - 1 }

// send writes request op of object id, with fd (-1 none) alongside.
func (w *conn) send(id, op uint32, a args, fd int) error {
	msg := le.AppendUint32(nil, id)
	msg = le.AppendUint32(msg, uint32(8+len(a))<<16|op)
	msg = append(msg, a...)
	var oob []byte
	if fd >= 0 {
		oob = syscall.UnixRights(fd)
	}
	_, _, err := w.c.WriteMsgUnix(msg, oob, nil)
	return err
}

// roundtrip hands every event to on until the server has handled all that
// was sent (wl_display.sync's callback); a protocol error ends it.
func (w *conn) roundtrip(on func(id, op uint32, b body)) error {
	cb := w.newID()
	if err := w.send(1, 0, args{}.u(cb), -1); err != nil {
		return err
	}
	for {
		var h [8]byte
		if _, err := io.ReadFull(w.c, h[:]); err != nil {
			return err
		}
		id, op, size := le.Uint32(h[:]), le.Uint32(h[4:])&0xffff, le.Uint32(h[4:])>>16
		if size < 8 {
			return errors.New("wayland: bad message")
		}
		b := make(body, size-8)
		if _, err := io.ReadFull(w.c, b); err != nil {
			return err
		}
		switch {
		case id == 1 && op == 0: // wl_display.error
			_, code := b.u(), b.u()
			return fmt.Errorf("wayland error %d: %s", code, b.s())
		case id == cb && op == 0:
			return nil
		case id != 1:
			on(id, op, b)
		}
	}
}

// args builds a request's arguments.
type args []byte

func (a args) u(v uint32) args { return le.AppendUint32(a, v) }

func (a args) s(s string) args {
	a = a.u(uint32(len(s) + 1))
	a = append(append(a, s...), 0)
	for len(a)%4 != 0 {
		a = append(a, 0)
	}
	return a
}

// body reads an event's arguments in order; short ones read as zero.
type body []byte

func (b *body) u() uint32 {
	if len(*b) < 4 {
		return 0
	}
	v := le.Uint32(*b)
	*b = (*b)[4:]
	return v
}

func (b *body) s() string {
	n := int(b.u())
	if n == 0 || n > len(*b) {
		return ""
	}
	s := string((*b)[:n-1])
	*b = (*b)[min((n+3)&^3, len(*b)):]
	return s
}
