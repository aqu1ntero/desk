//go:build linux

package infra

import (
	"encoding/json"
	"fmt"
	"image"
	"math"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"gioui.org/app"
	"gioui.org/io/system"
	"gioui.org/op"
	"gioui.org/op/paint"
	"gioui.org/unit"
)

// The terminal screen: a second window showing what the camera shows (the
// active tab's focused pane), so a share picker can offer it. On Hyprland it
// gets its own invisible monitor, DESK-SHARE, which pickers list as a screen;
// elsewhere the window itself is what to share.
const shareTitle, shareOutput = "desk terminal share", "DESK-SHARE"

var (
	shareMu   sync.Mutex
	shareStop chan struct{} // non-nil while the share window is open
	shareOnce sync.Once     // the Hyprland rules, once per desk
	shareFrom string        // the monitor focused when sharing started
)

func shareOn() bool {
	shareMu.Lock()
	defer shareMu.Unlock()
	return shareStop != nil
}

func hyprland() bool { return os.Getenv("HYPRLAND_INSTANCE_SIGNATURE") != "" }

func hypr(args ...string) error { return exec.Command("hyprctl", args...).Run() }

// shareToggle opens or closes the share window (and its monitor) and says what happened.
func shareToggle() string {
	shareMu.Lock()
	defer shareMu.Unlock()
	if shareStop != nil {
		close(shareStop)
		shareStop = nil
		return "terminal screen off"
	}
	shareStop = make(chan struct{})
	if !hyprland() {
		go shareWin(shareStop, 2)
		return `terminal screen on: share the "` + shareTitle + `" window`
	}
	shareOnce.Do(func() { // its own workspace, "silent": the window never takes focus
		hypr("eval", `hl.workspace_rule({ workspace = "name:desk-share", monitor = "`+shareOutput+`", default = true })`)
		hypr("eval", `hl.window_rule({ name = "desk-share", workspace = "name:desk-share silent", float = true, fullscreen_state = "2 2", match = { title = "^(`+shareTitle+`)$" } })`)
		sig := make(chan os.Signal, 1) // killed: still take the monitor away
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		go func() { <-sig; shareQuit(); os.Exit(1) }()
	})
	shareFrom = focusedMonitor()
	if err := hypr("output", "create", "headless", shareOutput); err != nil {
		go shareWin(shareStop, 2)
		return `terminal screen on: share the "` + shareTitle + `" window (no Hyprland output: ` + err.Error() + `)`
	}
	go func(stop chan struct{}) {
		// After monitor managers (hyprland.lua's) had their say: far from the
		// other monitors so the pointer never wanders in, 30 Hz is plenty.
		// Gio opens a window at the biggest whole output scale and keeps that
		// first GL buffer when the scale then changes (seen on NVIDIA), so the
		// monitor takes that scale: still 1920×1080 pixels.
		scale := 1
		for _, m := range hyprMonitors() {
			if s := int(math.Ceil(m.Scale)); m.Name != shareOutput && s > scale && 1080%s == 0 {
				scale = s
			}
		}
		time.Sleep(1500 * time.Millisecond)
		hypr("eval", fmt.Sprintf(`hl.monitor{ output = "%s", mode = "1920x1080@30", position = "-20000x-20000", scale = %d }`, shareOutput, scale))
		for range 30 { // open the window once that applied
			if m, ok := hyprMonitor(shareOutput); ok && m.Scale == float64(scale) && m.X == -20000 {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		shareWin(stop, scale)
	}(shareStop)
	return `terminal screen on: share the "` + shareOutput + `" screen`
}

type hyprMon struct {
	Name    string
	Focused bool
	X       int
	Scale   float64
}

func hyprMonitors() []hyprMon {
	var ms []hyprMon
	if out, err := exec.Command("hyprctl", "monitors", "-j").Output(); err == nil {
		json.Unmarshal(out, &ms)
	}
	return ms
}

func hyprMonitor(name string) (hyprMon, bool) {
	for _, m := range hyprMonitors() {
		if m.Name == name {
			return m, true
		}
	}
	return hyprMon{}, false
}

// focusedMonitor is Hyprland's focused monitor, "" when it's the share one.
func focusedMonitor() string {
	for _, m := range hyprMonitors() {
		if m.Focused && m.Name != shareOutput {
			return m.Name
		}
	}
	return ""
}

// shareRemove drops the Hyprland monitor. Hyprland then focuses some other
// monitor (here the parked laptop panel), so the one in use gets focus back.
func shareRemove() {
	if !hyprland() {
		return
	}
	back := focusedMonitor()
	if back == "" {
		back = shareFrom
	}
	hypr("output", "remove", shareOutput)
	if back != "" {
		hypr("dispatch", `hl.dsp.focus({ monitor = "`+back+`" })`)
	}
}

// shareQuit: no monitor outlives desk.
func shareQuit() {
	if shareOn() {
		shareRemove()
	}
}

// shareWin is shareWindow, a variable so tests never open a window.
var shareWin = shareWindow

// shareWindow opens the window at the share monitor's own size (1920×1080
// pixels at scale), floating: a Gio window opened next to another in the same
// process never resizes its GL buffer (Wayland, seen on NVIDIA), so it must
// not change size after it opens.
func shareWindow(stop chan struct{}, scale int) {
	defer shareRemove()
	w := new(app.Window)
	w.Option(app.Title(shareTitle), app.Size(unit.Dp(1920/scale), unit.Dp(1080/scale)))
	done := make(chan struct{})
	defer close(done)
	go func() {
		tick := time.NewTicker(time.Second / 20)
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-stop:
				w.Perform(system.ActionClose)
				return
			case <-tick.C:
				w.Invalidate()
			}
		}
	}()
	var (
		ops op.Ops
		r   camRender
		img *image.RGBA
		pic paint.ImageOp
	)
	for {
		switch e := w.Event().(type) {
		case app.DestroyEvent:
			shareMu.Lock()
			if shareStop == stop { // closed from the window manager
				shareStop = nil
			}
			shareMu.Unlock()
			return
		case app.FrameEvent:
			gtx := app.NewContext(&ops, e)
			if sz := gtx.Constraints.Max; img == nil || img.Rect.Size() != sz {
				img = image.NewRGBA(image.Rectangle{Max: sz})
			}
			if r.draw(img, camTerm.Load()) {
				pic = paint.NewImageOp(img)
			}
			pic.Add(gtx.Ops)
			paint.PaintOp{}.Add(gtx.Ops)
			e.Frame(gtx.Ops)
		}
	}
}
