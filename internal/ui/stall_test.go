package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestStall: a window away from its events past stallAfter is noted once in
// crash.log, with what it was doing and every goroutine's stack in
// stall.log, and noted again when it is back; a short one is not.
func TestStall(t *testing.T) {
	dir := t.TempDir()
	u := &ui{path: filepath.Join(dir, "board.json")}
	s := &stall{u: u, name: "main"}
	log := func() string { b, _ := os.ReadFile(filepath.Join(dir, "crash.log")); return string(b) }
	s.check(time.Now()) // at its events: nothing
	s.busy("handling app.FrameEvent")
	start := time.Unix(0, s.since.Load())
	s.check(start.Add(stallAfter / 2))
	if log() != "" {
		t.Fatal("a short stall is noted")
	}
	s.busy("drawing the frame (GPU, buffer swap)") // still the same stall
	s.check(start.Add(stallAfter + time.Second))
	s.check(start.Add(stallAfter + 2*time.Second))
	if l := log(); strings.Count(l, "away from its events") != 1 || !strings.Contains(l, "window main away from its events 4s") || !strings.Contains(l, "buffer swap") {
		t.Fatalf("noted once, with what it does: %q", l)
	}
	if st, _ := os.ReadFile(filepath.Join(dir, "stall.log")); !strings.Contains(string(st), "goroutine ") || !strings.Contains(string(st), "window main") {
		t.Errorf("stacks: %.200q", st)
	}
	s.idle()
	s.check(time.Now())
	s.check(time.Now())
	if l := log(); strings.Count(l, "back at its events") != 1 {
		t.Errorf("its end, once: %q", l)
	}
	w := u.watchStall("DESK-1 · a") // the ticker's goroutine ends with stop
	w.stop()
}
