package kit

import (
	"image"
	"testing"

	"gioui.org/io/input"
	"gioui.org/io/semantic"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/unit"
)

func TestFileIconLookup(t *testing.T) {
	for name, want := range map[string]setiIcon{
		"main.go":       setiExts["go"],
		"README.md":     setiNames["readme.md"], // the whole name beats the extension
		"Dockerfile":    setiNames["dockerfile"],
		"app.test.js":   setiExts["test.js"], // the longest extension first
		"lib.min.go":    setiExts["go"],      // then the shorter ones
		"Makefile":      setiNames["makefile"],
		"go.mod":        setiNames["go.mod"],
		"noext":         setiFile,
		"archive.zzzzq": setiFile,
	} {
		if got := fileIcon(name); got != want {
			t.Errorf("fileIcon(%q) = %x, want %x", name, got, want)
		}
	}
	if setiExts["go"] == setiFile || setiExts["test.js"] == setiExts["js"] || setiNames["readme.md"] == setiExts["md"] {
		t.Fatal("the cases above must tell their icons apart")
	}
}

// TestIconBoxCenters: smaller and wider content alike sit centered.
func TestIconBoxCenters(t *testing.T) {
	for size, want := range map[image.Point]image.Point{{4, 6}: {6, 5}, {24, 4}: {-4, 6}} {
		ops := new(op.Ops)
		gtx := layout.Context{Ops: ops, Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}}
		IconBox(16, func(gtx layout.Context) layout.Dimensions {
			defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
			semantic.LabelOp("glyph").Add(gtx.Ops)
			return layout.Dimensions{Size: size}
		})(gtx)
		var r input.Router
		r.Frame(ops)
		var got []image.Point
		for _, n := range r.AppendSemantics(nil) {
			if n.Desc.Label == "glyph" {
				got = append(got, n.Desc.Bounds.Min)
			}
		}
		if len(got) != 1 || got[0] != want {
			t.Errorf("%v content at %v, want %v", size, got, want)
		}
	}
}

func TestIconsKeepTheirBox(t *testing.T) {
	k := New()
	gtx := layout.Context{Ops: new(op.Ops), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Constraints: layout.Constraints{Max: image.Pt(200, 40)}}
	for _, w := range []layout.Widget{k.FileIcon("main.go", 16), FolderIcon(16, CAccText, false), FolderIcon(16, CAccText, true)} {
		if d := w(gtx); d.Size != image.Pt(16, 16) {
			t.Errorf("size %v, want 16×16", d.Size)
		}
	}
}
