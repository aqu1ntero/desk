// Package clip reads an image from the system clipboard, which Gio's
// clipboard can't (it reads only text): the Wayland selection on Linux
// (clip.go), NSPasteboard on macOS (clip_darwin.go), none elsewhere.
package clip
