package ui

import (
	"cmp"
	"fmt"
	"image"
	"image/color"
	"math"
	"slices"
	"strings"
	"time"

	"gioui.org/f32"
	"gioui.org/font"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"

	"github.com/lfdominguez/desk/internal/company"
	"github.com/lfdominguez/desk/internal/kanban"
	"github.com/lfdominguez/desk/internal/orgs"
	"github.com/lfdominguez/desk/internal/ui/kit"
)

// viewCompany is the Company view: every organization's Claudes as a
// little solar system around the user (companygeo.go lays it out), in
// pastels on paper, the hand-offs in flight as paper planes.
const viewCompany = "company"

// coState is what the Company view keeps between frames.
type coState struct {
	logs  map[string]coLog // each organization's company log, read at most every coLogEvery
	ps    []coPlanet       // last frame's, for the pointer
	notes []coNote         // last frame's sticky notes, for the pointer
	at    f32.Point        // the sun, in px
	px    float32          // px per dp
	ptr   f32.Point        // the pointer, last it moved over the view
}

type coLog struct {
	read time.Time
	es   []company.Entry
}

// coNote is a sticky note for a card that needs the user, where it was drawn.
type coNote struct {
	card string
	r    image.Rectangle
}

// coLogEvery is how often the view reads a log again; coFlight how long a
// hand-off flies.
const (
	coLogEvery = 2 * time.Second
	coFlight   = 90 * time.Second
)

var (
	coPaper   = kit.RGB(0xfff8ee)
	coInk     = kit.RGB(0x2f2a3a)
	coInkSoft = kit.RGB(0x5d5470)
	coDots    = kit.RGB(0xd9c7ae)
	coSticky  = kit.RGB(0xffe58f)
	// coOrgs are the sectors' pastels and inks, an organization's by its order.
	coOrgs = [][2]color.NRGBA{
		{kit.RGB(0xffe3d4), kit.RGB(0xc2603a)}, {kit.RGB(0xece0ff), kit.RGB(0x7a52c7)}, {kit.RGB(0xd9f7e7), kit.RGB(0x2f9a68)},
		{kit.RGB(0xdcefff), kit.RGB(0x2c6aa0)}, {kit.RGB(0xfff3c4), kit.RGB(0x9a7a10)},
	}
	// coFams are the planets' pastels and inks: executives, engineering,
	// infra, security, finance.
	coFams = [][2]color.NRGBA{
		{kit.RGB(0xfff1b8), kit.RGB(0x8a5c00)}, {kit.RGB(0xd6ecff), kit.RGB(0x2c5f93)}, {kit.RGB(0xd2fff1), kit.RGB(0x1f7a62)},
		{kit.RGB(0xffd9e8), kit.RGB(0xa33a68)}, {kit.RGB(0xe3ffd6), kit.RGB(0x3f7a1f)},
	}
	coStickers = map[string][2]string{"you": {"needs you!", "ffc36b"}, "turn": {"your turn", "d6ecff"}, "stuck": {"stuck", "ffb8b8"}, "done": {"done", "bff0c8"}}
	coExecs    = []struct{ role, ini, label string }{{"desk-cto", "CT", "CTO"}, {"desk-ciso", "CI", "CISO"}, {"desk-cfo", "CF", "CFO"}}
)

// coCards are the cards in the sky: with a session, not done or archived.
func (u *ui) coCards() []kanban.Card {
	done := u.b.Column("Done")
	var out []kanban.Card
	for i, col := range u.b.Columns {
		for _, c := range col.Cards {
			if i != done && c.Session.ID != "" && c.Org != "" {
				out = append(out, c)
			}
		}
	}
	return out
}

// coState of card c in the sky.
func (u *ui) coCardState(c kanban.Card) string {
	switch {
	case u.asks[c.ID] != nil || strings.HasPrefix(c.Note, "Paused"):
		return "you"
	case c.Status == kanban.StatusWorking:
		return "work"
	case c.Status == kanban.StatusWaiting:
		return "turn"
	case c.Status == kanban.StatusBlocked || c.Status == kanban.StatusFailed:
		return "stuck"
	case c.Status == kanban.StatusReview:
		return "done"
	}
	return ""
}

// coModel is the sky now: its sectors, planets and hand-offs in flight.
func (u *ui) coModel(now time.Time) ([]coSector, []coPlanet, []coLink) {
	cards := u.coCards()
	var orgList []string
	for _, c := range cards {
		if !slices.Contains(orgList, c.Org) {
			orgList = append(orgList, c.Org)
		}
	}
	for _, st := range u.leads {
		if !slices.Contains(orgList, st.org) {
			orgList = append(orgList, st.org)
		}
	}
	slices.Sort(orgList)
	var ps []coPlanet
	count := map[string]int{}
	for _, org := range orgList {
		busy := map[string]bool{} // the roles running in its cards now
		for _, c := range cards {
			for _, t := range u.tasks[c.ID] {
				if c.Org == org && t.Status == "running" {
					busy[t.Agent] = true
				}
			}
		}
		for _, x := range coExecs {
			ps = append(ps, coPlanet{id: "exec:" + org + ":" + x.role, org: org, ring: 0, ini: x.ini, label: x.label, fam: 0, state: map[bool]string{true: "work"}[busy[x.role]]})
		}
		for i, d := range company.Departments {
			st := u.leads[leadKey(org, d.Name)]
			p := coPlanet{id: "lead:" + org + ":" + d.Name, org: org, ring: 1, ini: strings.ToUpper(d.Name[:2]), label: d.Lead, fam: []int{1, 3, 4}[i]}
			if st != nil && len(st.from) > 0 {
				p.state, p.note = "work", "on a brief"
			}
			ps = append(ps, p)
		}
		if u.infraOrg(org) != nil {
			ps = append(ps, coPlanet{id: "lead:" + org + ":infra", org: org, ring: 1, ini: "IN", label: "Infra lead", fam: 2})
		}
		for _, c := range cards {
			if c.Org == org {
				ps = append(ps, coPlanet{id: "card:" + c.ID, org: org, ring: 2, ini: fmt.Sprint(c.Num), label: c.Key(), note: coShort(c.Title, 20), fam: 1, state: u.coCardState(c)})
			}
		}
	}
	for _, p := range ps {
		count[p.org]++
	}
	return coSectors(orgList, count), ps, u.coLinks(orgList, ps, now)
}

// coLinks are the hand-offs of the last coFlight between planets of the sky.
func (u *ui) coLinks(orgList []string, ps []coPlanet, now time.Time) []coLink {
	named := map[string]int{} // a log's from/to names: an org's planet
	for i, p := range ps {
		named[p.org+"\x00"+p.label] = i
		if role, ok := strings.CutPrefix(p.id, "exec:"+p.org+":"); ok {
			named[p.org+"\x00"+role] = i
		}
	}
	var out []coLink
	for _, org := range orgList {
		for _, e := range u.coLog(org, now) {
			if now.Sub(e.At) > coFlight || e.Kind == "message" && e.From == e.To {
				continue
			}
			a, okA := named[org+"\x00"+e.From]
			b, okB := named[org+"\x00"+e.To]
			if okA && okB && a != b {
				out = append(out, coLink{a, b})
			}
		}
	}
	return out
}

// coLog is org's company log, read again once it is coLogEvery old.
func (u *ui) coLog(org string, now time.Time) []company.Entry {
	if u.co.logs == nil {
		u.co.logs = map[string]coLog{}
	}
	l, ok := u.co.logs[org]
	if !ok || now.Sub(l.read) >= coLogEvery {
		l = coLog{now, company.Read(orgs.Dir(u.root, org, ""))}
		if len(l.es) > 200 { // ponytail: the whole log is read; a tail read when logs grow large
			l.es = l.es[len(l.es)-200:]
		}
		u.co.logs[org] = l
	}
	return l.es
}

// companySentence is the header's: who's busy, who needs the user, and
// what the organizations in the sky spent today.
func (u *ui) companySentence() string {
	busy, needs, orgSeen, spent := 0, 0, map[string]bool{}, 0.0
	for _, c := range u.coCards() {
		switch u.coCardState(c) {
		case "work":
			busy++
		case "you":
			needs++
		}
		if !orgSeen[c.Org] {
			orgSeen[c.Org] = true
			spent += company.Spent(orgs.Dir(u.root, c.Org, ""), time.Now())
		}
	}
	return fmt.Sprintf("%d busy · %d need you · $%.2f spent today", busy, needs, spent)
}

func coShort(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// companyView draws the sky, and the cards that need the user as sticky
// notes on the right; a click on a card's planet or note opens its session.
func (u *ui) companyView(gtx layout.Context) layout.Dimensions {
	if u.co == nil {
		u.co = &coState{}
	}
	size := gtx.Constraints.Max
	paint.FillShape(gtx.Ops, coPaper, clip.Rect{Max: size}.Op())
	u.coPointer(gtx)
	secs, ps, links := u.coModel(gtx.Now)
	panel := gtx.Dp(300)
	if size.X < gtx.Dp(760) {
		panel = 0
	}
	px := gtx.Metric.PxPerDp
	sky := image.Rect(0, 0, size.X-panel, size.Y)
	r := max(min(float32(sky.Dx()), float32(sky.Dy()))/px/2-56, 120)
	u.co.px, u.co.at = px, f32.Pt(float32(sky.Dx())/2, float32(sky.Dy())/2)
	radii := coRadii(r)
	coPlace(ps, secs, radii)
	u.co.ps = ps

	st := op.Affine(f32.Affine2D{}.Scale(f32.Point{}, f32.Pt(px, px)).Offset(u.co.at)).Push(gtx.Ops)
	cg := gtx
	cg.Metric = unit.Metric{PxPerDp: 1, PxPerSp: 1}
	cg.Constraints = layout.Constraints{Max: image.Pt(1<<20, 1<<20)}
	for i, s := range secs {
		pal := coOrgs[i%len(coOrgs)]
		coWedge(cg.Ops, radii[0]*0.55, r, s.a0, s.a1, pal[0])
		x, y := coAt(r+24, s.a0+min(4, (s.a1-s.a0)/4)) // at its start: its planets spread from the middle
		u.cvText(cg, image.Pt(int(x)-100, int(y)-16), s.org, 26, pal[1], kit.Bold(kit.Hand), 200, text.Middle)
	}
	for _, rr := range radii {
		coDotted(cg.Ops, rr)
	}
	t := float32(gtx.Now.UnixMilli()%2400) / 2400
	for i, l := range links {
		coChordLine(cg.Ops, ps[l.from], ps[l.to], coFams[ps[l.from].fam][1])
		x, y := coChord(ps[l.from], ps[l.to], float32(math.Mod(float64(t)+float64(i)*0.31, 1)))
		coDisc(cg.Ops, x, y, 5, kit.CWhite)
		coDisc(cg.Ops, x, y, 3, coFams[ps[l.from].fam][1])
	}
	needs := 0
	for _, p := range ps {
		if p.state == "you" {
			needs++
		}
	}
	coDisc(cg.Ops, 0, 0, 62, kit.CWhite)
	coDisc(cg.Ops, 0, 0, 57, kit.RGB(0xffd27a))
	coDisc(cg.Ops, -8, -8, 44, kit.RGB(0xffe9a8))
	u.cvText(cg, image.Pt(-60, -30), "PRESIDENT", 9, kit.RGB(0x6b3d0c), kit.Bold(kit.Sans), 120, text.Middle)
	u.cvText(cg, image.Pt(-60, -16), "You", 20, kit.RGB(0x6b3d0c), kit.Bold(kit.Sans), 120, text.Middle)
	u.cvText(cg, image.Pt(-60, 10), fmt.Sprintf("%d need you", needs), 15, kit.RGB(0x8a4b12), kit.Hand, 120, text.Middle)
	busy := len(links) > 0
	for _, p := range ps {
		u.coPlanet(cg, p, gtx.Now)
		busy = busy || p.state == "work" || p.state == "you"
	}
	st.Pop()

	if panel > 0 {
		u.coPanel(gtx, image.Rect(size.X-panel, 0, size.X, size.Y), ps)
	}
	if len(ps) == 0 {
		u.cvText(gtx, image.Pt(0, sky.Dy()/2+gtx.Dp(90)), "No one is working yet: start a card's session and its organization appears.", unit.Sp(16), coInkSoft, kit.Hand, sky.Dx(), text.Middle)
	}
	area := clip.Rect{Max: size}.Push(gtx.Ops) // the planets and notes take clicks
	event.Op(gtx.Ops, u.co)
	area.Pop()
	if busy { // halos, wiggles and planes: ~12 frames a second, slower when not looked at
		gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(map[bool]time.Duration{true: 80 * time.Millisecond, false: 500 * time.Millisecond}[u.focused])})
	}
	return layout.Dimensions{Size: size}
}

// coPointer opens the session of the card whose planet or note was
// clicked, once the frame is drawn.
func (u *ui) coPointer(gtx layout.Context) {
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: u.co, Kinds: pointer.Press | pointer.Move})
		if !ok {
			return
		}
		e, ok := ev.(pointer.Event)
		if !ok || u.co.px == 0 {
			continue
		}
		if e.Kind == pointer.Move { // where it is in the view: tests find the planets by it
			u.co.ptr = e.Position
			continue
		}
		card := ""
		for _, n := range u.co.notes {
			if e.Position.Round().In(n.r) {
				card = n.card
			}
		}
		if i := coHit(u.co.ps, (e.Position.X-u.co.at.X)/u.co.px, (e.Position.Y-u.co.at.Y)/u.co.px); i >= 0 {
			card = strings.TrimPrefix(u.co.ps[i].id, "card:")
			if card == u.co.ps[i].id {
				card = ""
			}
		}
		if card != "" {
			u.post = func() { u.openSession(card) }
		}
	}
}

// coPlanet draws p: its halo while busy, its disc with a white rim, its
// name and note on paper, and its sticker.
func (u *ui) coPlanet(gtx layout.Context, p coPlanet, now time.Time) {
	fam := coFams[p.fam]
	r := coSize(p.ring)
	if p.state == "work" {
		ph := float32(now.UnixMilli()%2000) / 2000
		c := fam[1]
		c.A = uint8(180 * (1 - ph))
		coRing(gtx.Ops, p.x, p.y, r+4+ph*14, 2.5, c)
	}
	coDisc(gtx.Ops, p.x, p.y+4, r+4, kit.Mix(fam[1], coPaper, .75)) // its soft shadow
	coDisc(gtx.Ops, p.x, p.y, r+4, kit.CWhite)
	coDisc(gtx.Ops, p.x, p.y, r, fam[0])
	u.cvText(gtx, image.Pt(int(p.x-r), int(p.y)-8), p.ini, 13, fam[1], kit.Bold(kit.Sans), int(2*r), text.Middle)
	y := int(p.y + r + 8)
	if p.ring == 2 && p.y < 0 { // the outer ring's names point away from the sun, clear of the leads
		y = int(p.y-r) - 26
		if p.note != "" {
			y -= 18
		}
	}
	u.coPaperText(gtx, image.Pt(int(p.x), y), p.label, 12, coInk, kit.Bold(kit.Sans))
	if p.note != "" {
		u.coPaperText(gtx, image.Pt(int(p.x), y+18), p.note, 15, coInkSoft, kit.Hand)
	}
	if s, ok := coStickers[p.state]; ok {
		var h uint32
		fmt.Sscanf(s[1], "%x", &h)
		ang := float32(0)
		if p.state == "you" {
			ang = float32(math.Sin(float64(now.UnixMilli())/190)) * 0.12
		}
		at := f32.Pt(p.x+r*0.55, p.y-r-6)
		if p.ring == 2 && p.y < 0 { // its names are above: the sticker goes below
			at.Y = p.y + r + 6
		}
		rot := op.Affine(f32.Affine2D{}.Rotate(at, ang)).Push(gtx.Ops)
		m := op.Record(gtx.Ops)
		w := u.cvText(gtx, image.Pt(int(at.X)+6, int(at.Y)-7), s[0], 10, coInk, kit.Bold(kit.Sans), 120, text.Start)
		call := m.Stop()
		box := image.Rect(int(at.X), int(at.Y)-9, int(at.X)+w+12, int(at.Y)+9)
		kit.FillRRect(gtx.Ops, box.Inset(-2), 11, kit.CWhite)
		kit.FillRRect(gtx.Ops, box, 9, kit.RGB(h))
		call.Add(gtx.Ops)
		rot.Pop()
	}
}

// coPaperText writes s centered under at, on a patch of paper so the
// rings and chords don't run through it.
func (u *ui) coPaperText(gtx layout.Context, at image.Point, s string, sp unit.Sp, c color.NRGBA, f font.Font) {
	m := op.Record(gtx.Ops)
	w := u.cvText(gtx, image.Pt(at.X-120, at.Y), s, sp, c, f, 240, text.Middle)
	call := m.Stop()
	h := int(sp) + 6
	kit.FillRRect(gtx.Ops, image.Rect(at.X-w/2-6, at.Y-1, at.X+w/2+6, at.Y+h-1), h/2, kit.Mix(coPaper, kit.CWhite, .3))
	call.Add(gtx.Ops)
}

// coPanel lists the cards that need the user as sticky notes, then how to
// read the sky.
func (u *ui) coPanel(gtx layout.Context, r image.Rectangle, ps []coPlanet) {
	u.co.notes = u.co.notes[:0]
	y := r.Min.Y + gtx.Dp(24)
	x, w := r.Min.X+gtx.Dp(16), r.Dx()-gtx.Dp(36)
	u.cvText(gtx, image.Pt(x, y), "Notes for you", unit.Sp(26), kit.RGB(0x3b2f5c), kit.Bold(kit.Hand), w, text.Start)
	y += gtx.Dp(44)
	for i, p := range ps {
		id, ok := strings.CutPrefix(p.id, "card:")
		if !ok || p.state != "you" && p.state != "turn" {
			continue
		}
		c := u.b.Card(id)
		if c == nil {
			continue
		}
		box := image.Rect(x, y, x+w, y+gtx.Dp(78))
		tilt := float32([]float64{-0.025, 0.02}[i%2])
		rot := op.Affine(f32.Affine2D{}.Rotate(f32.Pt(float32(box.Min.X+box.Dx()/2), float32(box.Min.Y)), tilt)).Push(gtx.Ops)
		kit.FillRRect(gtx.Ops, box.Add(image.Pt(0, gtx.Dp(4))), gtx.Dp(4), kit.Mix(coSticky, coInk, .25))
		kit.FillRRect(gtx.Ops, box, gtx.Dp(4), map[bool]color.NRGBA{true: coSticky, false: kit.RGB(0xd6ecff)}[p.state == "you"])
		u.cvText(gtx, image.Pt(x+gtx.Dp(12), y+gtx.Dp(8)), c.Key()+" · "+coShort(c.Title, 26), unit.Sp(13), coInk, kit.Bold(kit.Sans), w-gtx.Dp(24), text.Start)
		why := cmp.Or(c.Note, "your turn")
		if a := u.asks[id]; a != nil {
			why = "asks: " + a.r.Line
		}
		u.cvText(gtx, image.Pt(x+gtx.Dp(12), y+gtx.Dp(30)), coShort(why, 40), unit.Sp(17), kit.RGB(0x3d3550), kit.Hand, w-gtx.Dp(24), text.Start)
		u.cvText(gtx, image.Pt(x+gtx.Dp(12), y+gtx.Dp(54)), "open its session →", unit.Sp(11), coInkSoft, kit.Bold(kit.Sans), w-gtx.Dp(24), text.Start)
		rot.Pop()
		u.co.notes = append(u.co.notes, coNote{id, box})
		y += gtx.Dp(96)
	}
	if len(u.co.notes) == 0 {
		u.cvText(gtx, image.Pt(x, y), "Nothing waits for you.", unit.Sp(18), coInkSoft, kit.Hand, w, text.Start)
		y += gtx.Dp(40)
	}
	y += gtx.Dp(12)
	for _, l := range []string{
		"Close to you: the executives.",
		"Next ring: the department leads.",
		"Outside: the cards' Claudes.",
		"A halo is busy; a sticker wants you.",
		"Dots on the move: hand-offs.",
	} {
		u.cvText(gtx, image.Pt(x, y), l, unit.Sp(17), kit.RGB(0x3d3550), kit.Hand, w, text.Start)
		y += gtx.Dp(26)
	}
}

// coWedge fills the ring between r0 and r1 from angle a0 to a1.
func coWedge(ops *op.Ops, r0, r1, a0, a1 float32, c color.NRGBA) {
	var p clip.Path
	p.Begin(ops)
	steps := max(int((a1-a0)/3), 2)
	for i := 0; i <= steps; i++ {
		x, y := coAt(r1, a0+(a1-a0)*float32(i)/float32(steps))
		if i == 0 {
			p.MoveTo(f32.Pt(x, y))
		} else {
			p.LineTo(f32.Pt(x, y))
		}
	}
	for i := steps; i >= 0; i-- {
		x, y := coAt(r0, a0+(a1-a0)*float32(i)/float32(steps))
		p.LineTo(f32.Pt(x, y))
	}
	p.Close()
	paint.FillShape(ops, c, clip.Outline{Path: p.End()}.Op())
}

// coDotted is a ring of dots at radius r.
func coDotted(ops *op.Ops, r float32) {
	n := int(2 * math.Pi * float64(r) / 10)
	for i := range n {
		x, y := coAt(r, 360*float32(i)/float32(n))
		coDisc(ops, x, y, 1.2, coDots)
	}
}

func coDisc(ops *op.Ops, x, y, r float32, c color.NRGBA) {
	b := image.Rect(int(x-r), int(y-r), int(math.Ceil(float64(x+r))), int(math.Ceil(float64(y+r))))
	paint.FillShape(ops, c, clip.Ellipse(b).Op(ops))
}

func coRing(ops *op.Ops, x, y, r, w float32, c color.NRGBA) {
	b := image.Rect(int(x-r), int(y-r), int(x+r), int(y+r))
	paint.FillShape(ops, c, clip.Stroke{Path: clip.Ellipse(b).Path(ops), Width: w}.Op())
}

// coChordLine strokes the hand-off from a to b.
func coChordLine(ops *op.Ops, a, b coPlanet, c color.NRGBA) {
	var p clip.Path
	p.Begin(ops)
	p.MoveTo(f32.Pt(a.x, a.y))
	for i := 1; i <= 24; i++ {
		x, y := coChord(a, b, float32(i)/24)
		p.LineTo(f32.Pt(x, y))
	}
	c.A = 0x90
	paint.FillShape(ops, c, clip.Stroke{Path: p.End(), Width: 2}.Op())
}
