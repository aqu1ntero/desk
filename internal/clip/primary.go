package clip

import "sync"

// The primary selection is what was last selected with the mouse, in any
// window: what a middle click pastes in X terminals. desk owns it while the
// text selected in it is the newest (SetPrimary), and reads it for a middle
// click (Primary). Where the system has none desk can reach (macOS, Windows,
// a compositor without data-control's primary selection), both fall back to
// the text last selected in desk.

// owner is desk's own primary selection: its text, how to stop serving it,
// and the goroutines serving (tests wait for them).
var owner struct {
	sync.Mutex
	text    string
	stop    chan struct{}
	serving sync.WaitGroup
}

// SetPrimary makes text the primary selection, served off the caller's
// goroutine until another window's selection replaces it.
func SetPrimary(text string) {
	owner.Lock()
	defer owner.Unlock()
	if owner.stop != nil {
		close(owner.stop)
	}
	stop := make(chan struct{})
	owner.text, owner.stop = text, stop
	owner.serving.Go(func() { ownPrimary(text, stop) }) //nolint:errcheck // unreachable: desk's own text is the fallback
}

// Primary is the primary selection's text: the system's, or desk's own last
// selection when the system's can't be read.
func Primary() string {
	b, err := readPrimary()
	if err != nil {
		owner.Lock()
		defer owner.Unlock()
		return owner.text
	}
	return string(b)
}
