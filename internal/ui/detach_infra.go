package ui

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gioui.org/layout"
	dbus "github.com/godbus/dbus/v5"

	"github.com/lfdominguez/desk/internal/ui/kit"
)

// infraWake redraws the window that draws the infra workspace, from its
// goroutines: a detached session's beside its chat, else the main one.
func (u *ui) infraWake() {
	if w := u.infraWin.Load(); w != nil {
		w.Invalidate()
		return
	}
	u.w.Invalidate()
}

// infraTake gives the infra workspace to owner's window (a detached card's
// id, "" the main one). The window that had it redraws without it, and the
// new one asks for its own card's tab again (splitFor): the workspace has
// one active tab, so the last window's would show here.
func (u *ui) infraTake(owner string) {
	if u.infraBy == owner {
		return
	}
	old := u.infraBy
	u.infraBy, u.splitFor = owner, ""
	u.tkGen++
	if d := u.detached[owner]; d != nil {
		u.infraWin.Store(d.w)
	} else {
		u.infraWin.Store(nil)
	}
	if d := u.detached[old]; d != nil {
		d.w.Invalidate()
	} else if old == "" {
		u.w.Invalidate()
	}
}

// mainInfra says whether the main window shows the workspace: its servers
// view, or a session of its own with its server work beside it.
func (u *ui) mainInfra() bool {
	v := u.mainPane.view
	if strings.HasPrefix(v, "srv:") {
		return true
	}
	id, ok := strings.CutPrefix(v, "sess:")
	return ok && u.detached[id] == nil && u.splitOrg(id) != ""
}

// infraElsewhere is the main window's servers view while another window
// draws the workspace and this one isn't focused: where it is. Working in
// this window brings it here (serversView's infraTake).
func (u *ui) infraElsewhere(gtx layout.Context) layout.Dimensions {
	key := u.infraBy
	if c := u.b.Card(key); c != nil {
		key = c.Key()
	}
	kit.Fill(gtx, kit.CColumn)
	return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(u.k.Lbl("The servers are beside "+key+"'s session, in its own window", 14, kit.CText, kit.Semi(kit.Sans))),
			layout.Rigid(kit.VGap(8)),
			layout.Rigid(u.k.Lbl("Click here to bring them back", 13, kit.CText3, kit.Sans)))
	})
}

// hyprFocus focuses this process's window titled title through
// Hyprland's socket, as `hyprctl dispatch` does: false off Hyprland or
// when it can't. Hyprland 0.56's Lua config takes hl.dsp.focus; older ones
// focuswindow.
func hyprFocus(title string) bool {
	sig := os.Getenv("HYPRLAND_INSTANCE_SIGNATURE")
	if sig == "" {
		return false
	}
	sock := filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), "hypr", sig, ".socket.sock")
	out, err := hyprCall(sock, "j/clients")
	if err != nil {
		return false
	}
	var clients []struct {
		Address string `json:"address"`
		Pid     int    `json:"pid"`
		Title   string `json:"title"`
	}
	if json.Unmarshal(out, &clients) != nil {
		return false
	}
	for _, c := range clients {
		if c.Pid != os.Getpid() || c.Title != title {
			continue
		}
		for _, cmd := range []string{
			fmt.Sprintf("dispatch hl.dsp.focus({ window = 'address:%s' })", c.Address),
			"dispatch focuswindow address:" + c.Address,
		} {
			if r, err := hyprCall(sock, cmd); err == nil && strings.TrimSpace(string(r)) == "ok" {
				return true
			}
		}
		return false
	}
	return false
}

// hyprCall sends one request on Hyprland's socket and reads its answer.
func hyprCall(sock, req string) ([]byte, error) {
	c, err := net.DialTimeout("unix", sock, 2*time.Second)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.WriteString(c, req); err != nil {
		return nil, err
	}
	return io.ReadAll(c)
}

// kwinRaise is the KWin script kwinFocus loads: KWin 6's windowList and
// activeWindow, else KWin 5's clientList and activeClient. A script may
// activate any window, past focus-stealing prevention.
const kwinRaise = `const want = %s, pid = %d;
const all = workspace.windowList ? workspace.windowList() : workspace.clientList();
for (const w of all) {
	if (w.pid !== pid || w.caption !== want) continue;
	w.minimized = false;
	if (workspace.windowList) workspace.activeWindow = w; else workspace.activeClient = w;
	break;
}
`

// kwinFocus focuses this process's window titled title on KDE, as kdotool
// does: a KWin script over the session bus, loaded under one name (the last
// one unloaded first) and started. False off KWin; whether the script found
// the window it can't tell.
func kwinFocus(title string) bool {
	bus, err := dbus.ConnectSessionBus()
	if err != nil {
		return false
	}
	defer bus.Close()
	js, _ := json.Marshal(title)
	f := filepath.Join(os.TempDir(), fmt.Sprintf("desk-raise-%d.js", os.Getpid()))
	if os.WriteFile(f, fmt.Appendf(nil, kwinRaise, js, os.Getpid()), 0o600) != nil {
		return false
	}
	const name = "desk-raise"
	o := bus.Object("org.kde.KWin", "/Scripting")
	if o.Call("org.kde.kwin.Scripting.unloadScript", 0, name).Err != nil {
		return false // no KWin
	}
	var id int32
	if o.Call("org.kde.kwin.Scripting.loadScript", 0, f, name).Store(&id) != nil || id < 0 {
		return false
	}
	return o.Call("org.kde.kwin.Scripting.start", 0).Err == nil
}
