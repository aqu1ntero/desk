//go:build !linux

package infra

import "sync/atomic"

// The terminal camera is a PipeWire node: Linux GUI only (gui_camera.go).

const cameraBuilt = false

var camTerm atomic.Pointer[Term]

func cameraOn() bool       { return false }
func cameraToggle() string { return "" }
func shareOn() bool        { return false }
func shareToggle() string  { return "" }
func shareQuit()           {}
