package orgs

import (
	"errors"
	"io/fs"
	"sync"

	"github.com/BurntSushi/toml"
)

// ZoomMin and ZoomMax bound the zoom, in percent; ZoomStep is one key press.
const (
	ZoomMin  = 50
	ZoomMax  = 300
	ZoomStep = 10
)

// zoomCache is the zoom read from the settings file at path: layout asks for
// it every frame, which mustn't read the disk.
var zoomCache struct {
	sync.Mutex
	path string
	z    int
}

// Zoom is how big the terminals' and the session panel's text is, in
// percent: the settings' zoom, 100 when unset.
func Zoom() int {
	path := SettingsFile()
	zoomCache.Lock()
	defer zoomCache.Unlock()
	if zoomCache.path != path {
		zoomCache.path, zoomCache.z = path, clampZoom(loadSettings().Zoom)
	}
	return zoomCache.z
}

// clampZoom is z inside ZoomMin..ZoomMax; 0 (unset) is 100.
func clampZoom(z int) int {
	if z == 0 {
		return 100
	}
	return min(max(z, ZoomMin), ZoomMax)
}

// SetZoom keeps z (clamped) as the zoom, the other settings as they are.
// The new zoom counts at once, even when the file can't be written.
func SetZoom(z int) error {
	z = clampZoom(z)
	path := SettingsFile()
	zoomCache.Lock()
	zoomCache.path, zoomCache.z = path, z
	zoomCache.Unlock()
	var s settings
	// a file that doesn't parse isn't rewritten: a key press would lose its folders and languages
	if _, err := toml.DecodeFile(path, &s); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if s.Zoom = z; z == 100 {
		s.Zoom = 0 // the default: not written
	}
	return saveSettings(s)
}

// StepZoom moves the zoom by steps of ZoomStep; 0 steps puts it back to 100.
func StepZoom(steps int) error {
	if steps == 0 {
		return SetZoom(100)
	}
	return SetZoom(Zoom() + steps*ZoomStep)
}
