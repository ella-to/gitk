// Package graph lays out a commit DAG as one text row per commit, the way
// gitk draws it: each commit sits in a lane, lanes run vertically between
// rows, and merges and forks are drawn as horizontal connectors on the
// commit's own row with rounded box-drawing corners.
package graph

// Cell is one character of a row's graph column.
type Cell struct {
	R     rune // 0 for blank
	Color int  // lane color index
}

// Row is the drawing of one commit.
type Row struct {
	Col   int    // lane of the commit node
	Color int    // color of the commit's lane
	Cells []Cell // 2 cells per lane: the lane glyph, then a connector gap
}

// Width is the number of lanes the row spans.
func (r Row) Width() int { return len(r.Cells) / 2 }

type lane[K comparable] struct {
	id    K
	color int
	used  bool
}

const (
	up = 1 << iota
	down
	left
	right
)

var glyphs = [16]rune{
	0:                        ' ',
	up:                       '│',
	down:                     '│',
	up | down:                '│',
	left:                     '─',
	right:                    '─',
	left | right:             '─',
	down | right:             '╭',
	down | left:              '╮',
	up | right:               '╰',
	up | left:                '╯',
	up | down | right:        '├',
	up | down | left:         '┤',
	down | left | right:      '┬',
	up | left | right:        '┴',
	up | down | left | right: '┼',
}

// Layout assigns lanes for commits listed children-first (topological order)
// and returns one Row per commit. parents(i) lists commit i's parent keys;
// parents not in the list simply end their lane below the row.
func Layout[K comparable](ids []K, parents func(i int) []K, colors int) []Row {
	if colors <= 0 {
		colors = 1
	}
	var lanes []lane[K]
	nextColor := 0
	newColor := func() int {
		c := nextColor % colors
		nextColor++
		return c
	}
	rows := make([]Row, len(ids))

	for i, id := range ids {
		ps := parents(i)
		n := len(lanes)

		col := -1
		var converging []int
		for j := range lanes {
			if lanes[j].used && lanes[j].id == id {
				if col < 0 {
					col = j
				} else {
					converging = append(converging, j)
				}
			}
		}
		hadUp := col >= 0
		if col < 0 {
			col = freeSlot(lanes, nil)
			if col == len(lanes) {
				lanes = append(lanes, lane[K]{})
			}
			lanes[col] = lane[K]{id: id, color: newColor(), used: true}
		}

		width := max(n, len(lanes)) + len(ps)
		bits := make([]uint8, width)
		vcol := make([]int, width) // color of the vertical/endpoint glyph
		hcol := make([]int, width) // color of the gap after lane j
		hgap := make([]bool, width)
		for j := range n {
			if lanes[j].used && lanes[j].id != id {
				bits[j] |= up | down
				vcol[j] = lanes[j].color
			}
		}
		node := lanes[col].color
		if hadUp {
			bits[col] |= up
		}
		hline := func(a, b, color int) {
			lo, hi := min(a, b), max(a, b)
			bits[lo] |= right
			bits[hi] |= left
			for m := lo + 1; m < hi; m++ {
				bits[m] |= left | right
				if bits[m]&(up|down) == 0 {
					vcol[m] = color
				}
			}
			for g := lo; g < hi; g++ {
				hgap[g] = true
				hcol[g] = color
			}
		}

		freed := map[int]bool{}
		for _, j := range converging {
			bits[j] |= up
			vcol[j] = lanes[j].color
			hline(j, col, lanes[j].color)
			lanes[j].used = false
			freed[j] = true
		}

		if len(ps) == 0 {
			lanes[col].used = false
		} else {
			lanes[col].id = ps[0]
			bits[col] |= down
			for _, p := range ps[1:] {
				k := -1
				for j := range lanes {
					if j != col && lanes[j].used && lanes[j].id == p {
						k = j
						break
					}
				}
				if k < 0 {
					freed[col] = true // never reuse the node's own lane
					k = freeSlot(lanes, freed)
					delete(freed, col)
					if k == len(lanes) {
						lanes = append(lanes, lane[K]{})
					}
					lanes[k] = lane[K]{id: p, color: newColor(), used: true}
					if k >= len(bits) {
						bits = append(bits, 0)
						vcol = append(vcol, 0)
						hcol = append(hcol, 0)
						hgap = append(hgap, false)
					}
				}
				bits[k] |= down
				if bits[k]&up == 0 {
					vcol[k] = lanes[k].color
				}
				hline(col, k, lanes[k].color)
			}
		}

		// trim to the lanes that actually draw something
		w := 0
		for j := range bits {
			if bits[j] != 0 || j == col {
				w = j + 1
			}
		}
		cells := make([]Cell, 2*w)
		for j := range w {
			if j == col {
				cells[2*j] = Cell{R: 0, Color: node}
			} else if bits[j] != 0 {
				cells[2*j] = Cell{R: glyphs[bits[j]], Color: vcol[j]}
			}
			if hgap[j] && j+1 < w {
				cells[2*j+1] = Cell{R: '─', Color: hcol[j]}
			}
		}
		rows[i] = Row{Col: col, Color: node, Cells: cells}

		for len(lanes) > 0 && !lanes[len(lanes)-1].used {
			lanes = lanes[:len(lanes)-1]
		}
	}
	return rows
}

// freeSlot returns the first unused lane not in skip, or len(lanes).
func freeSlot[K comparable](lanes []lane[K], skip map[int]bool) int {
	for j := range lanes {
		if !lanes[j].used && !skip[j] {
			return j
		}
	}
	return len(lanes)
}
