package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestContent: a message without images is its text; with them, the text
// then each image as "[Image n]" and its base64 block, typed by extension
// or, without one, by its bytes; a missing file is an error.
func TestContent(t *testing.T) {
	if c, err := Content("hi", nil); c != "hi" || err != nil {
		t.Fatalf("%v %v", c, err)
	}
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.png"), filepath.Join(dir, "b")
	os.WriteFile(a, []byte("\x89PNG\r\n\x1a\nxx"), 0o644)
	os.WriteFile(b, []byte("\xff\xd8\xff\xe0jpeg"), 0o644)
	c, err := Content("see [Image 1] [Image 2]", []string{a, b})
	if err != nil {
		t.Fatal(err)
	}
	j, _ := json.Marshal(c)
	s := string(j)
	for _, want := range []string{`{"text":"see [Image 1] [Image 2]","type":"text"}`, `{"text":"[Image 1]","type":"text"}`, `"media_type":"image/png"`, `{"text":"[Image 2]","type":"text"}`, `"media_type":"image/jpeg"`, `"type":"base64"`} {
		if !strings.Contains(s, want) {
			t.Errorf("no %s in %s", want, s)
		}
	}
	if _, err := Content("x", []string{filepath.Join(dir, "gone.png")}); err == nil {
		t.Error("a missing image sent")
	}
}
