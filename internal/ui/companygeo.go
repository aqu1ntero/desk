package ui

import (
	"math"
	"slices"
)

// The Company view's sky (company.go), without drawing: the user is the
// sun in the middle, each organization a sector, and its people planets on
// three rings by rank: executives, leads, cards. Units are dp, the sun at
// (0, 0); angles in degrees from the top, clockwise.

// coPlanet is one of an organization's Claudes in the sky.
type coPlanet struct {
	id    string // "card:<id>", "lead:<org>:<dept>", "exec:<org>:<role>"
	org   string
	ring  int    // 0 executives, 1 leads, 2 cards
	ini   string // what its disc says
	label string // its name under it
	note  string // a line of what it does, handwritten
	fam   int    // its pastel (coFams)
	state string // "work", "you", "stuck", "done" or "" (idle)
	up    string // the autopilot's planet running this card, "" none
	x, y  float32
}

// coSector is an organization's slice of the sky.
type coSector struct {
	org    string
	a0, a1 float32
}

// coLink is a hand-off in flight between two planets.
type coLink struct{ from, to int }

// coRadii are the rings' radii in a sky of radius r.
func coRadii(r float32) [3]float32 { return [3]float32{r * 0.33, r * 0.6, r * 0.88} }

// coSectors shares the circle among orgs in order, by how many planets
// each has (at least 3, so a quiet organization still has room), with a
// gap between them.
func coSectors(orgs []string, planets map[string]int) []coSector {
	total := 0
	for _, o := range orgs {
		total += max(planets[o], 3)
	}
	const gap = 4
	var out []coSector
	a := float32(gap) / 2
	for _, o := range orgs {
		w := (360 - gap*float32(len(orgs))) * float32(max(planets[o], 3)) / float32(total)
		out = append(out, coSector{o, a, a + w})
		a += w + gap
	}
	return out
}

// coAt is the point at radius r and angle a.
func coAt(r, a float32) (x, y float32) {
	s, c := math.Sincos(float64(a) * math.Pi / 180)
	return r * float32(s), -r * float32(c)
}

// coPlace puts each planet on its ring, the ones of an organization and
// ring spread evenly across its sector.
func coPlace(ps []coPlanet, secs []coSector, radii [3]float32) {
	for _, s := range secs {
		for ring := range 3 {
			var mine []int
			for i := range ps {
				if ps[i].org == s.org && ps[i].ring == ring {
					mine = append(mine, i)
				}
			}
			for k, i := range mine {
				ps[i].x, ps[i].y = coAt(radii[ring], s.a0+(float32(k)+0.5)*(s.a1-s.a0)/float32(len(mine)))
			}
		}
	}
}

// coSize is a planet's disc radius on ring.
func coSize(ring int) float32 { return [3]float32{24, 28, 22}[ring] }

// coHit is the planet under (x, y), -1 for none.
func coHit(ps []coPlanet, x, y float32) int {
	for i, p := range slices.Backward(ps) {
		if dx, dy := x-p.x, y-p.y; dx*dx+dy*dy <= (coSize(p.ring)+6)*(coSize(p.ring)+6) {
			return i
		}
	}
	return -1
}

// coChord is the point at t (0..1) on the curve from a to b that bows
// towards the sun, so hand-offs cross the middle of the sky.
func coChord(a, b coPlanet, t float32) (x, y float32) {
	cx, cy := (a.x+b.x)/2*0.55, (a.y+b.y)/2*0.55
	u := 1 - t
	return u*u*a.x + 2*u*t*cx + t*t*b.x, u*u*a.y + 2*u*t*cy + t*t*b.y
}
