package kit

import (
	"testing"

	"gioui.org/op"
	"github.com/oligo/gvcode"
	gvcolor "github.com/oligo/gvcode/color"
	"github.com/oligo/gvcode/gutter/providers"
	"github.com/oligo/gvcode/textstyle/syntax"
)

// TestCodeEditor: our colours over the scheme's scopes, no wrap, line
// numbers and the extra gutter.
func TestCodeEditor(t *testing.T) {
	var cs syntax.ColorScheme
	cs.AddStyle("Keyword", syntax.Bold, gvcolor.MakeColor(CAcc), gvcolor.Color{})
	diff := providers.NewVCSDiffProvider()
	e := CodeEditor(cs, diff)
	p := e.ColorPalette()
	if p.Foreground.NRGBA() != CText || p.Background.NRGBA() != CPane || p.LineNumberColor.NRGBA() != CText3 {
		t.Fatalf("palette %+v", p)
	}
	ids := map[string]bool{}
	for _, g := range e.GutterManager().Providers() {
		ids[g.ID()] = true
	}
	if len(ids) != 2 || !ids[providers.DiffProviderID] {
		t.Fatalf("gutters %v", ids)
	}
	e.SetText("a\tb\n")
	var ops op.Ops
	if d := e.Layout(ctx(&ops, 400, 200), New().Sh); d.Size.X != 400 {
		t.Errorf("laid out %v", d.Size)
	}
	if style, n := e.TabStyle(); style != gvcode.Tabs || n != 4 {
		t.Errorf("tabs %v %d", style, n)
	}
}
