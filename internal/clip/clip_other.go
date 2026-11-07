//go:build !linux && !darwin

package clip

// Image: no clipboard image here, so a paste is the editor's text.
func Image() ([]byte, string, error) { return nil, "", nil }
