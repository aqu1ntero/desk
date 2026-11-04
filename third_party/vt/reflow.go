package vt

import (
	"sort"

	uv "github.com/charmbracelet/ultraviolet"
)

// desk patch: reflow. A width change of the main screen joins the rows
// autowrap split (Screen.wrap, Scrollback.wrap) back into the lines the
// program wrote and wraps them again at the new width, in the scrollback and
// on screen, as kitty and VTE do. The alt screen is only cut: its program
// redraws it.

// logical is a line as the program wrote it: its rows joined, wide
// placeholders and padding left out (layout puts them back).
type logical struct {
	cells  []uv.Cell
	first  int   // its first row in the new layout
	starts []int // the column (of the joined line) each new row starts at
}

// reflow rewraps s and its scrollback at w columns and h rows. The cursor
// keeps its place in its line (phantom: it is past the row's last column) and,
// room allowing, its row on screen; what no longer fits above goes to the
// scrollback, which drops its oldest lines past its size as Push does. It
// returns where an absolute position of before is now, and whether the
// cursor is now past the last column of its row.
func (s *Screen) reflow(w, h int, phantom bool) (func(line, x int) (int, int), bool) {
	sb := s.scrollback
	nsb := sb.Len()
	cx, cy := s.cur.X, s.cur.Y
	if phantom {
		cx = s.buf.Width() // past the row's end, a wide last character's too
	}
	n := nsb + s.buf.Height() // blank rows below the text come out blank again
	cur := nsb + cy

	// Join the old rows into lines, noting where each row starts in its line.
	var lines []logical
	at := make([][2]int, n) // old row -> its line, the column it starts at
	var l logical
	col := 0
	for i := range n {
		row, wrapped := sb.Line(i), sb.Wrapped(i)
		if i >= nsb {
			row, wrapped = s.buf.Line(i-nsb), s.Wrapped(i-nsb)
		}
		at[i] = [2]int{len(lines), col}
		for _, c := range row {
			if c.Width > 0 {
				l.cells = append(l.cells, c)
				col += c.Width
			}
		}
		if wrapped && i < n-1 {
			continue
		}
		// Trailing blanks are no text, but the cursor's line keeps them up to
		// the cursor.
		keep := 0
		if cur <= i && at[cur][0] == len(lines) {
			keep = at[cur][1] + cx
		}
		for len(l.cells) > 0 && col > keep && l.cells[len(l.cells)-1].Equal(&uv.EmptyCell) {
			l.cells = l.cells[:len(l.cells)-1]
			col--
		}
		lines = append(lines, l)
		l, col = logical{}, 0
	}

	// Wrap them again at w; a wide character never splits across rows.
	var rows []uv.Line
	var wraps []bool
	for k := range lines {
		l := &lines[k]
		l.first, l.starts = len(rows), []int{0}
		row, x, c0 := uv.NewLine(w), 0, 0
		for _, c := range l.cells {
			if x > 0 && x+c.Width > w {
				for ; x < w; x++ {
					row[x] = uv.Cell{} // padding
				}
				rows, wraps = append(rows, row), append(wraps, true)
				row, x = uv.NewLine(w), 0
				l.starts = append(l.starts, c0)
			}
			row.Set(x, &c)
			x += c.Width
			c0 += c.Width
		}
		rows, wraps = append(rows, row), append(wraps, false)
	}
	pos := func(k, col int) (int, int) {
		l := &lines[k]
		j := sort.Search(len(l.starts), func(i int) bool { return l.starts[i] > col }) - 1
		return l.first + j, col - l.starts[j]
	}

	r, x := pos(at[cur][0], at[cur][1]+cx)
	phantom = x >= w
	x = min(x, w-1)
	// The screen starts where it did, or higher, pulling lines back from the
	// scrollback, so the cursor's row stays (a prompt at the bottom stays
	// there); lower only when the cursor would fall off it.
	top, _ := pos(at[nsb][0], at[nsb][1])
	top = max(0, min(top, r-cy))
	if r-top >= h {
		top = r - h + 1
	}

	sb.Clear()
	for i := range top {
		sb.push(rows[i], wraps[i])
	}
	dropped := top - sb.Len()

	screen := make([]uv.Line, h)
	s.wrap = make([]bool, h)
	for y := range screen {
		if top+y < len(rows) {
			screen[y], s.wrap[y] = rows[top+y], wraps[top+y]
		} else {
			screen[y] = uv.NewLine(w)
		}
	}
	s.buf.Lines = screen
	s.buf.Touched = nil

	moved := func(line, x int) (int, int) {
		if line < 0 {
			return line, x
		}
		if line >= n {
			return len(rows) + line - n - dropped, min(x, w-1)
		}
		r, x := pos(at[line][0], at[line][1]+x)
		return r - dropped, min(x, w-1)
	}
	s.cur.X, s.cur.Y = x, r-top
	sr, sx := moved(nsb+s.saved.Y, s.saved.X)
	s.saved.X, s.saved.Y = sx, min(max(sr+dropped-top, 0), h-1)
	return moved, phantom
}
