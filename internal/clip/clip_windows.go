package clip

import (
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32       = windows.NewLazySystemDLL("user32.dll")
	kernel32     = windows.NewLazySystemDLL("kernel32.dll")
	openClip     = user32.NewProc("OpenClipboard")
	closeClip    = user32.NewProc("CloseClipboard")
	hasFormat    = user32.NewProc("IsClipboardFormatAvailable")
	getData      = user32.NewProc("GetClipboardData")
	registerFmt  = user32.NewProc("RegisterClipboardFormatW")
	globalLock   = kernel32.NewProc("GlobalLock")
	globalUnlock = kernel32.NewProc("GlobalUnlock")
	globalSize   = kernel32.NewProc("GlobalSize")
)

// cfDIB is CF_DIB; Windows synthesizes it from CF_DIBV5 and CF_BITMAP, so
// it is the one bitmap format to read.
const cfDIB = 8

// Image is the clipboard's image as image/png: the "PNG" format browsers and
// Office put there as is, else CF_DIB converted (dibPNG). nil when it holds
// neither.
func Image() ([]byte, string, error) {
	pngFmt := pngFormat()
	// another program may hold the clipboard for a moment
	for i := 0; ; i++ {
		if r, _, err := openClip.Call(0); r != 0 {
			break
		} else if i == 10 {
			return nil, "", err
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer closeClip.Call()
	if r, _, _ := hasFormat.Call(pngFmt); pngFmt != 0 && r != 0 {
		if b := global(pngFmt); b != nil {
			return b, "image/png", nil
		}
	}
	if r, _, _ := hasFormat.Call(cfDIB); r == 0 {
		return nil, "", nil
	}
	b := global(cfDIB)
	if b == nil {
		return nil, "", nil
	}
	p, err := dibPNG(b)
	if err != nil {
		return nil, "", err
	}
	return p, "image/png", nil
}

// pngFormat is the registered "PNG" clipboard format's id (0: none).
func pngFormat() uintptr {
	name, _ := windows.UTF16PtrFromString("PNG")
	f, _, _ := registerFmt.Call(uintptr(unsafe.Pointer(name)))
	return f
}

// global copies the clipboard's format f out of its global memory, or nil.
func global(f uintptr) []byte {
	h, _, _ := getData.Call(f)
	if h == 0 {
		return nil
	}
	n, _, _ := globalSize.Call(h)
	p, _, _ := globalLock.Call(h)
	if p == 0 || n == 0 || n > 64<<20 {
		if p != 0 {
			globalUnlock.Call(h)
		}
		return nil
	}
	defer globalUnlock.Call(h)
	ptr := *(*unsafe.Pointer)(unsafe.Pointer(&p)) // GlobalLock's address, outside Go's heap
	return append([]byte(nil), unsafe.Slice((*byte)(ptr), n)...)
}
