package infra

import (
	"image/color"
	"strconv"
	"strings"

	"github.com/alecthomas/chroma/v2/styles"

	"github.com/lfdominguez/desk/internal/ui/kit"
)

// GUI themes: the TUI's chat palettes (themes.go) also dress the window.
// "desk" is the hand-made look below; the others derive surfaces from the
// palette's rule color (a tone close to its background) and take text, accent
// and status colors from its roles. ctrl+k → "theme: …" switches, saved in
// layout.json like the TUI's.

var deskLook = look{
	panel: cPanel, rim: cRim, pane: cPane, card: cCard, panelCard: cPanelCard, hover: cHover, line: cLine,
	text: cText, text2: cText2, text3: cText3, acc: cAcc, accText: cAccText, accSoft: cAccSoft,
	green: cGreen, orange: cOrange, orangeSoft: cOrangeSoft, purple: cPurple, purpleSoft: cPurpleSoft, red: cRed, idle: cIdle,
	ansi: ansi16, chroma: "github-dark",
}

type look struct {
	panel, rim, pane, card, panelCard, hover, line           color.NRGBA
	text, text2, text3, acc, accText, accSoft                color.NRGBA
	green, orange, orangeSoft, purple, purpleSoft, red, idle color.NRGBA
	ansi                                                     [16]color.NRGBA
	chroma                                                   string
}

var guiTheme = "desk"

func hex(s string, def color.NRGBA) color.NRGBA {
	v, err := strconv.ParseUint(strings.TrimPrefix(s, "#"), 16, 32)
	if err != nil || len(strings.TrimPrefix(s, "#")) != 6 {
		return def
	}
	return rgb(uint32(v))
}

// mix is a + (b-a)·f, per channel.
func mix(a, b color.NRGBA, f float64) color.NRGBA {
	m := func(x, y uint8) uint8 { return uint8(float64(x) + (float64(y)-float64(x))*f) }
	return color.NRGBA{m(a.R, b.R), m(a.G, b.G), m(a.B, b.B), 0xff}
}

// lookFor builds the window's colors from palette p.
func lookFor(p Palette) look {
	if p.Rule == "" { // "mono": no colors of its own
		return deskLook
	}
	base := hex(p.Rule, cRim)
	black, white := rgb(0), rgb(0xffffff)
	l := deskLook
	if p.Light {
		l.pane, l.card = mix(base, white, 0.8), mix(base, white, 0.55)
		l.panel, l.panelCard = mix(base, white, 0.45), mix(base, white, 0.15)
		l.hover, l.line, l.rim = mix(base, black, 0.06), mix(base, black, 0.12), mix(base, black, 0.15)
		l.idle = mix(base, black, 0.35)
	} else {
		l.pane, l.card = mix(base, black, 0.6), mix(base, black, 0.4)
		l.panel, l.panelCard = mix(base, black, 0.45), mix(base, black, 0.2)
		l.hover, l.line, l.rim = mix(base, white, 0.04), mix(base, black, 0.1), base
		l.idle = mix(base, white, 0.25)
	}
	l.text = hex(p.Strong, l.text)
	l.text2 = hex(p.CodeText, l.text2)
	l.text3 = hex(p.Quote, l.text3)
	l.acc = hex(p.Accent, l.acc)
	l.accText = hex(p.Link, l.accText)
	l.accSoft = mix(l.acc, l.pane, 0.72)
	l.green, l.red = hex(p.DiffAdded, l.green), hex(p.DiffRemoved, l.red)
	l.orange, l.purple = hex(p.Heading, l.orange), hex(p.Emphasis, l.purple)
	l.orangeSoft, l.purpleSoft = mix(l.orange, l.pane, 0.75), mix(l.purple, l.pane, 0.75)
	if p.Light { // dark ANSI greys and whites so light-on-light text stays readable
		l.ansi[0], l.ansi[7], l.ansi[8], l.ansi[15] = l.text2, l.text3, l.text3, l.text
	}
	if p.Chroma != "" {
		l.chroma = p.Chroma
	}
	return l
}

// applyTheme switches the window's design tokens to theme name.
func applyTheme(name string) {
	p, ok := themes[name]
	if !ok {
		return
	}
	guiTheme = name
	l := deskLook
	if name != "desk" {
		l = lookFor(p)
	}
	cPanel, cRim, cPane, cCard, cPanelCard, cHover, cLine = l.panel, l.rim, l.pane, l.card, l.panelCard, l.hover, l.line
	cText, cText2, cText3, cAcc, cAccText, cAccSoft = l.text, l.text2, l.text3, l.acc, l.accText, l.accSoft
	cGreen, cOrange, cOrangeSoft, cPurple, cPurpleSoft, cRed, cIdle = l.green, l.orange, l.orangeSoft, l.purple, l.purpleSoft, l.red, l.idle
	cKeyRim, cBtnRim, cDanger, cSel, cRedSoft, cRedText = kit.CKeyRim, kit.CBtnRim, kit.CDanger, kit.CSel, kit.CRedSoft, kit.CRedText
	if name != "desk" { // the dialog tokens, from the theme's own
		cKeyRim, cBtnRim = mix(cPanelCard, cText, 0.08), mix(cPanelCard, cText, 0.06)
		cDanger, cSel = mix(cRed, rgb(0), 0.2), mix(cAcc, cPane, 0.75)
		cRedSoft, cRedText = mix(cRed, cPane, 0.85), mix(cRed, cText, 0.25)
	}
	ansi16 = l.ansi
	chromaStyle = styles.Get(l.chroma)
	tabDot = map[string]color.NRGBA{"approval": cOrange, "working": cPurple, "your turn": cAcc}
}
