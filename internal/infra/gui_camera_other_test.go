//go:build !linux

package infra

import (
	"strings"
	"testing"
)

// cameraPalC: without PipeWire the palette offers no camera or share at all,
// rather than a toggle that does nothing.
func cameraPalC(t *testing.T, m *model) {
	m.pal, m.palQ = true, ""
	for _, it := range m.palItems() {
		if strings.Contains(it.label, "terminal camera") || strings.Contains(it.label, "terminal as a") {
			t.Errorf("offers %q", it.label)
		}
	}
}
