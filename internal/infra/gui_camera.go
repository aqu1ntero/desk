//go:build linux

package infra

/*
#cgo pkg-config: libpipewire-0.3
#include <pipewire/pipewire.h>
#include <spa/param/video/format-utils.h>
#include <spa/param/buffers.h>
#include <stdlib.h>
#include <string.h>

// cam is a PipeWire Video/Source node (a camera other apps can pick) that we
// drive: cam_push copies a frame in and asks the stream for a process cycle.
typedef struct {
	struct pw_thread_loop *loop;
	struct pw_stream *stream;
	int w, h;
	uint8_t *frame; // I420, w*h*3/2
} cam;

static size_t cam_size(cam *c) { return (size_t)c->w * c->h * 3 / 2; }

static void cam_process(void *data) {
	cam *c = data;
	struct pw_buffer *b = pw_stream_dequeue_buffer(c->stream);
	if (!b) return;
	struct spa_data *d = &b->buffer->datas[0];
	if (d->data && d->maxsize >= cam_size(c)) {
		memcpy(d->data, c->frame, cam_size(c));
		d->chunk->offset = 0;
		d->chunk->size = cam_size(c);
		d->chunk->stride = c->w;
	}
	pw_stream_queue_buffer(c->stream, b);
}

static void cam_param_changed(void *data, uint32_t id, const struct spa_pod *param) {
	cam *c = data;
	if (param == NULL || id != SPA_PARAM_Format) return;
	uint8_t buf[256];
	struct spa_pod_builder b = SPA_POD_BUILDER_INIT(buf, sizeof buf);
	const struct spa_pod *p = spa_pod_builder_add_object(&b, SPA_TYPE_OBJECT_ParamBuffers, SPA_PARAM_Buffers,
		SPA_PARAM_BUFFERS_buffers, SPA_POD_CHOICE_RANGE_Int(4, 2, 8),
		SPA_PARAM_BUFFERS_blocks, SPA_POD_Int(1),
		SPA_PARAM_BUFFERS_size, SPA_POD_Int(cam_size(c)),
		SPA_PARAM_BUFFERS_stride, SPA_POD_Int(c->w),
		SPA_PARAM_BUFFERS_dataType, SPA_POD_CHOICE_FLAGS_Int((1 << SPA_DATA_MemPtr) | (1 << SPA_DATA_MemFd)));
	pw_stream_update_params(c->stream, &p, 1);
}

static const struct pw_stream_events cam_events = {
	PW_VERSION_STREAM_EVENTS,
	.param_changed = cam_param_changed,
	.process = cam_process,
};

static void cam_free(cam *c) {
	if (c->loop) pw_thread_loop_stop(c->loop);
	if (c->stream) pw_stream_destroy(c->stream);
	if (c->loop) pw_thread_loop_destroy(c->loop);
	free(c->frame);
	free(c);
}

static cam *cam_new(int w, int h, int fps) {
	pw_init(NULL, NULL);
	cam *c = calloc(1, sizeof *c);
	c->w = w, c->h = h;
	c->frame = calloc(1, cam_size(c));
	memset(c->frame + (size_t)w * h, 128, (size_t)w * h / 2); // black, not green
	c->loop = pw_thread_loop_new("desk-camera", NULL);
	if (!c->loop) { cam_free(c); return NULL; }
	c->stream = pw_stream_new_simple(pw_thread_loop_get_loop(c->loop), "desk terminal",
		pw_properties_new(PW_KEY_MEDIA_TYPE, "Video", PW_KEY_MEDIA_CATEGORY, "Capture",
			PW_KEY_MEDIA_ROLE, "Camera", PW_KEY_MEDIA_CLASS, "Video/Source",
			PW_KEY_NODE_NAME, "desk", PW_KEY_NODE_DESCRIPTION, "desk terminal", NULL),
		&cam_events, c);
	if (!c->stream) { cam_free(c); return NULL; }
	uint8_t buf[512];
	struct spa_pod_builder b = SPA_POD_BUILDER_INIT(buf, sizeof buf);
	const struct spa_pod *p = spa_pod_builder_add_object(&b, SPA_TYPE_OBJECT_Format, SPA_PARAM_EnumFormat,
		SPA_FORMAT_mediaType, SPA_POD_Id(SPA_MEDIA_TYPE_video),
		SPA_FORMAT_mediaSubtype, SPA_POD_Id(SPA_MEDIA_SUBTYPE_raw),
		SPA_FORMAT_VIDEO_format, SPA_POD_Id(SPA_VIDEO_FORMAT_I420),
		SPA_FORMAT_VIDEO_size, SPA_POD_Rectangle(&SPA_RECTANGLE(w, h)),
		SPA_FORMAT_VIDEO_framerate, SPA_POD_Fraction(&SPA_FRACTION(fps, 1)));
	if (pw_stream_connect(c->stream, PW_DIRECTION_OUTPUT, PW_ID_ANY,
			PW_STREAM_FLAG_DRIVER | PW_STREAM_FLAG_MAP_BUFFERS, &p, 1) < 0 ||
		pw_thread_loop_start(c->loop) < 0) {
		cam_free(c);
		return NULL;
	}
	return c;
}

// cam_streaming: some app is reading the camera right now.
static int cam_streaming(cam *c) {
	pw_thread_loop_lock(c->loop);
	int on = pw_stream_get_state(c->stream, NULL) == PW_STREAM_STATE_STREAMING;
	pw_thread_loop_unlock(c->loop);
	return on;
}

static void cam_push(cam *c, const uint8_t *f) {
	pw_thread_loop_lock(c->loop);
	memcpy(c->frame, f, cam_size(c));
	if (pw_stream_get_state(c->stream, NULL) == PW_STREAM_STATE_STREAMING)
		pw_stream_trigger_process(c->stream);
	pw_thread_loop_unlock(c->loop);
}
*/
import "C"

import (
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/gomonobold"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// The terminal camera: the active tab's focused pane, drawn on the CPU into a
// PipeWire camera node ("desk terminal") that call apps list next to the webcam.
const camW, camH, camFPS = 1280, 720, 15

const cameraBuilt = true

var (
	camTerm atomic.Pointer[Term] // what the camera shows; set by every snapshot
	camMu   sync.Mutex
	camStop chan struct{} // non-nil while the camera is on
)

func cameraOn() bool {
	camMu.Lock()
	defer camMu.Unlock()
	return camStop != nil
}

// cameraToggle starts or stops the camera and says what happened.
func cameraToggle() string {
	camMu.Lock()
	defer camMu.Unlock()
	if camStop != nil {
		close(camStop)
		camStop = nil
		return "terminal camera off"
	}
	f, name, err := camV4L2()
	if err == nil {
		camStop = make(chan struct{})
		go camLoop(camStop, func() bool { return true }, func(b []byte) { f.Write(b) }, func() { f.Close() })
		return fmt.Sprintf("terminal camera on: pick %q (%s) as the camera in the call", name, f.Name())
	}
	live, push, done, ok := camPipeWire()
	if !ok {
		return "terminal camera: can't reach PipeWire"
	}
	camStop = make(chan struct{})
	go camLoop(camStop, live, push, done)
	msg := `terminal camera on (PipeWire only): pick "desk terminal" as the camera in the call`
	if !errors.Is(err, errNoLoopback) {
		msg += "; v4l2loopback: " + err.Error()
	}
	return msg
}

// camPipeWire makes the PipeWire camera node: whether some app reads it, a
// frame in, and freeing it. A variable so tests never reach PipeWire.
var camPipeWire = func() (live func() bool, push func([]byte), done func(), ok bool) {
	c := C.cam_new(camW, camH, camFPS)
	if c == nil {
		return nil, nil, nil, false
	}
	return func() bool { return C.cam_streaming(c) != 0 },
		func(b []byte) { C.cam_push(c, (*C.uint8_t)(unsafe.Pointer(&b[0]))) }, func() { C.cam_free(c) }, true
}

// camLoop draws frames while live and hands them to push, until stop.
func camLoop(stop chan struct{}, live func() bool, push func([]byte), done func()) {
	defer done()
	img := image.NewRGBA(image.Rect(0, 0, camW, camH))
	yuv := make([]byte, camW*camH*3/2)
	var r camRender
	tick := time.NewTicker(time.Second / camFPS)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
		}
		if !live() {
			continue // nobody is watching
		}
		if r.draw(img, camTerm.Load()) {
			toI420(img, yuv)
		}
		push(yuv)
	}
}

var errNoLoopback = errors.New("no v4l2loopback device")

// camRoot prefixes camV4L2's /sys and /dev paths, and camIoctl is its ioctl:
// variables so tests never open a real video device.
var (
	camRoot  = ""
	camIoctl = func(fd, req uintptr, arg unsafe.Pointer) syscall.Errno {
		_, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, req, uintptr(arg))
		return e
	}
)

// camV4L2 opens a v4l2loopback device (they're the virtual video4linux ones;
// one named "desk…" wins) as an I420 output: apps that only
// read /dev/video* (Slack, Zoom, Discord) list it as a webcam, and PipeWire
// apps see it too.
// ponytail: VIDIOC_S_FMT's number assumes a 64-bit kernel (208-byte v4l2_format).
func camV4L2() (*os.File, string, error) {
	devs, _ := filepath.Glob(camRoot + "/sys/devices/virtual/video4linux/video*")
	if len(devs) == 0 {
		return nil, "", errNoLoopback
	}
	dev, name := devs[0], ""
	for _, d := range devs {
		if b, err := os.ReadFile(d + "/name"); err == nil && strings.HasPrefix(string(b), "desk") {
			dev = d
		}
	}
	if b, err := os.ReadFile(dev + "/name"); err == nil {
		name = strings.TrimSpace(string(b))
	}
	f, err := os.OpenFile(camRoot+"/dev/"+filepath.Base(dev), os.O_WRONLY, 0)
	if err != nil {
		return nil, "", err
	}
	var fm [208]byte                         // struct v4l2_format
	binary.NativeEndian.PutUint32(fm[0:], 2) // V4L2_BUF_TYPE_VIDEO_OUTPUT
	for i, v := range []uint32{camW, camH, 0x32315559 /* YU12: I420 */, 1 /* FIELD_NONE */, camW, camW * camH * 3 / 2, 1 /* SMPTE170M: BT.601 */} {
		binary.NativeEndian.PutUint32(fm[8+4*i:], v) // struct v4l2_pix_format, after the union's padding
	}
	if e := camIoctl(f.Fd(), 0xc0d05605 /* VIDIOC_S_FMT */, unsafe.Pointer(&fm[0])); e != 0 {
		f.Close()
		return nil, "", fmt.Errorf("%s: set format: %w", f.Name(), e)
	}
	return f, name, nil
}

// camRender draws a terminal with the window's colors, sizing the font so
// the whole screen fits the frame; it redraws only when the screen changed.
type camRender struct {
	term       *Term
	runs       [][]Run
	cx, cy     int
	cursor     bool
	cols, rows int
	frame      image.Point  // the image size the font was fitted to
	faces      [2]font.Face // regular, bold
	adv, lh    int          // cell width and height, px
	asc        int
	glyphs     map[camGlyph]camMask
}

type camGlyph struct {
	r    rune
	bold bool
}

type camMask struct {
	m  *image.Alpha
	at image.Point // offset from the dot
}

// camFonts: JetBrains Mono when fontconfig has it (as loadFonts), else Go Mono.
var camFonts = sync.OnceValue(func() [2]*opentype.Font {
	var fs [2]*opentype.Font
	for i, style := range []string{"Regular", "Bold"} {
		b := [][]byte{gomono.TTF, gomonobold.TTF}[i]
		if out, err := exec.Command("fc-match", "-f", "%{file}", "JetBrainsMono Nerd Font:style="+style).Output(); err == nil && strings.Contains(string(out), "JetBrains") {
			if s, err := os.ReadFile(string(out)); err == nil {
				b = s
			}
		}
		fs[i], _ = opentype.Parse(b)
		if fs[i] == nil {
			fs[i], _ = opentype.Parse([][]byte{gomono.TTF, gomonobold.TTF}[i])
		}
	}
	return fs
})

// fit picks the biggest font size whose cells fit cols×rows in a w×h frame.
func (r *camRender) fit(cols, rows int, frame image.Point) {
	r.cols, r.rows, r.frame, r.glyphs = cols, rows, frame, map[camGlyph]camMask{}
	fs := camFonts()
	for size := float64(frame.Y / max(rows, 1)); size >= 4; size-- {
		for i := range r.faces {
			r.faces[i], _ = opentype.NewFace(fs[i], &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
		}
		m := r.faces[0].Metrics()
		a, _ := r.faces[0].GlyphAdvance('M')
		r.adv, r.lh, r.asc = a.Ceil(), (m.Ascent+m.Descent).Ceil()+int(size/8), m.Ascent.Ceil()
		if r.adv*cols <= frame.X && r.lh*rows <= frame.Y {
			return
		}
	}
}

func (r *camRender) glyph(g camGlyph) camMask {
	if m, ok := r.glyphs[g]; ok {
		return m
	}
	f := r.faces[0]
	if g.bold {
		f = r.faces[1]
	}
	var m camMask
	if dr, mask, mp, _, ok := f.Glyph(fixed.Point26_6{}, g.r); ok && !dr.Empty() {
		a := image.NewAlpha(image.Rect(0, 0, dr.Dx(), dr.Dy()))
		draw.Draw(a, a.Bounds(), mask, mp, draw.Src) // the face reuses its mask buffer
		m = camMask{a, dr.Min}
	}
	r.glyphs[g] = m
	return m
}

// draw paints t into img and reports whether anything changed.
func (r *camRender) draw(img *image.RGBA, t *Term) bool {
	var runs [][]Run
	var cx, cy int
	var cursor bool
	if t != nil {
		runs, _, cx, cy, cursor = t.Grid(0)
	}
	frame := img.Rect.Size()
	if t == r.term && cx == r.cx && cy == r.cy && cursor == r.cursor && frame == r.frame && reflect.DeepEqual(runs, r.runs) {
		return false
	}
	r.term, r.runs, r.cx, r.cy, r.cursor = t, runs, cx, cy, cursor
	draw.Draw(img, img.Bounds(), image.NewUniform(cPane), image.Point{}, draw.Src)
	if t == nil {
		r.frame, r.cols, r.rows = frame, 0, 0 // unchanged until a terminal shows; fit again for it
		return true
	}
	if cols, rows := t.Size(); cols != r.cols || rows != r.rows || frame != r.frame {
		r.fit(cols, rows, frame)
	}
	ox, oy := (frame.X-r.adv*r.cols)/2, (frame.Y-r.lh*r.rows)/2
	for y, row := range runs {
		py := oy + y*r.lh
		for _, run := range row {
			fg, bg := cellColor(run.Style.Fg, cText), cellColor(run.Style.Bg, color.NRGBA{})
			if run.Style.Attrs&uv.AttrReverse != 0 {
				fg, bg = cellColor(run.Style.Bg, cPane), cellColor(run.Style.Fg, cText)
			}
			if run.Style.Attrs&uv.AttrFaint != 0 {
				fg.A = 0x90
			}
			x := ox + run.X*r.adv
			if bg.A != 0 {
				draw.Draw(img, image.Rect(x, py, x+run.W*r.adv, py+r.lh), image.NewUniform(bg), image.Point{}, draw.Over)
			}
			src := image.NewUniform(fg)
			for _, ch := range run.Text {
				w := ansi.StringWidth(string(ch))
				if ch != ' ' && w > 0 {
					if m := r.glyph(camGlyph{ch, run.Style.Attrs&uv.AttrBold != 0}); m.m != nil {
						at := image.Pt(x, py+r.asc).Add(m.at)
						draw.DrawMask(img, m.m.Bounds().Add(at), src, image.Point{}, m.m, image.Point{}, draw.Over)
					}
				}
				x += w * r.adv
			}
		}
	}
	if cursor && cy < len(runs) {
		x, y := ox+cx*r.adv, oy+cy*r.lh
		draw.Draw(img, image.Rect(x, y, x+r.adv, y+r.lh), image.NewUniform(cAcc), image.Point{}, draw.Over)
	}
	return true
}

// toI420 converts to BT.601 limited-range I420; each 2×2 block takes the
// chroma of its top-left pixel.
func toI420(img *image.RGBA, out []byte) {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	u, v := out[w*h:], out[w*h+w*h/4:]
	for y := range h {
		p := img.Pix[y*img.Stride:]
		for x := range w {
			r, g, b := int(p[4*x]), int(p[4*x+1]), int(p[4*x+2])
			out[y*w+x] = byte((66*r+129*g+25*b+128)>>8 + 16)
			if x%2 == 0 && y%2 == 0 {
				i := y/2*(w/2) + x/2
				u[i] = byte((-38*r-74*g+112*b+128)>>8 + 128)
				v[i] = byte((112*r-94*g-18*b+128)>>8 + 128)
			}
		}
	}
}
