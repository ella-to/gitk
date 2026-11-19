package ui

import (
	"strings"

	"ella.to/microui"
	"ella.to/microui/renderer"
)

// textArea is a small multi-line editor (microui's textbox is single-line):
// typing, Enter for new lines, Backspace/Delete, arrows, Home/End and click
// to place the caret.
type textArea struct {
	lines  [][]rune
	row    int
	col    int
	scroll int
}

func newTextArea(s string) *textArea {
	t := &textArea{}
	for _, l := range strings.Split(s, "\n") {
		t.lines = append(t.lines, []rune(l))
	}
	if len(t.lines) == 0 {
		t.lines = [][]rune{{}}
	}
	return t
}

func (t *textArea) String() string {
	parts := make([]string, len(t.lines))
	for i, l := range t.lines {
		parts[i] = string(l)
	}
	return strings.Join(parts, "\n")
}

func (t *textArea) insert(s string) {
	for _, r := range s {
		if r == '\n' {
			t.newline()
			continue
		}
		if r < 0x20 {
			continue
		}
		l := t.lines[t.row]
		l = append(l[:t.col], append([]rune{r}, l[t.col:]...)...)
		t.lines[t.row] = l
		t.col++
	}
}

func (t *textArea) newline() {
	l := t.lines[t.row]
	rest := append([]rune(nil), l[t.col:]...)
	t.lines[t.row] = l[:t.col]
	t.lines = append(t.lines[:t.row+1], append([][]rune{rest}, t.lines[t.row+1:]...)...)
	t.row++
	t.col = 0
}

func (t *textArea) backspace() {
	switch {
	case t.col > 0:
		l := t.lines[t.row]
		t.lines[t.row] = append(l[:t.col-1], l[t.col:]...)
		t.col--
	case t.row > 0:
		prev := t.lines[t.row-1]
		t.col = len(prev)
		t.lines[t.row-1] = append(prev, t.lines[t.row]...)
		t.lines = append(t.lines[:t.row], t.lines[t.row+1:]...)
		t.row--
	}
}

func (t *textArea) del() {
	l := t.lines[t.row]
	switch {
	case t.col < len(l):
		t.lines[t.row] = append(l[:t.col], l[t.col+1:]...)
	case t.row+1 < len(t.lines):
		t.lines[t.row] = append(l, t.lines[t.row+1]...)
		t.lines = append(t.lines[:t.row+1], t.lines[t.row+2:]...)
	}
}

// update handles input when focused and draws the editor into r.
func (t *textArea) update(ctx *microui.Context, p pen, id microui.ID, r microui.Rect) {
	ctx.UpdateControl(id, r, microui.OptHoldFocus)
	focused := ctx.Focus() == id
	if focused {
		if s := ctx.TextInput(); s != "" {
			t.insert(s)
		}
		k := ctx.KeyPressed()
		switch {
		case k&microui.KeyReturn != 0:
			t.newline()
		case k&microui.KeyBackspace != 0:
			t.backspace()
		case k&microui.KeyDelete != 0:
			t.del()
		case k&microui.KeyLeft != 0:
			if t.col > 0 {
				t.col--
			} else if t.row > 0 {
				t.row--
				t.col = len(t.lines[t.row])
			}
		case k&microui.KeyRight != 0:
			if t.col < len(t.lines[t.row]) {
				t.col++
			} else if t.row+1 < len(t.lines) {
				t.row++
				t.col = 0
			}
		case k&microui.KeyUp != 0:
			if t.row > 0 {
				t.row--
			}
		case k&microui.KeyDown != 0:
			if t.row+1 < len(t.lines) {
				t.row++
			}
		case k&microui.KeyHome != 0:
			t.col = 0
		case k&microui.KeyEnd != 0:
			t.col = len(t.lines[t.row])
		}
		if ctx.MousePressed()&microui.MouseLeft != 0 && ctx.MouseOver(r) {
			m := ctx.MousePos()
			t.row = min(max(t.scroll+m.Y-r.Y, 0), len(t.lines)-1)
			t.col = runeAtCell(t.lines[t.row], m.X-r.X-1)
		}
	}
	t.col = min(t.col, len(t.lines[t.row]))

	bg := p.th.BG
	if focused {
		bg = p.th.Header
	}
	p.fill(r, bg)
	if t.row < t.scroll {
		t.scroll = t.row
	}
	if t.row >= t.scroll+r.H {
		t.scroll = t.row - r.H + 1
	}
	p.clip(r, func() {
		for y := 0; y < r.H && t.scroll+y < len(t.lines); y++ {
			line := string(t.lines[t.scroll+y])
			c := p.th.Text
			if t.scroll+y == 0 && len(t.lines) > 1 {
				c = p.th.Text
			}
			p.text(r.X+1, r.Y+y, line, c)
		}
		if focused {
			x := r.X + 1 + renderer.StringWidth(string(t.lines[t.row][:t.col]))
			y := r.Y + t.row - t.scroll
			ch := " "
			if t.col < len(t.lines[t.row]) {
				ch = string(t.lines[t.row][t.col])
			}
			p.fill(microui.Rect{X: x, Y: y, W: max(renderer.StringWidth(ch), 1), H: 1}, p.th.Accent)
			p.text(x, y, ch, p.th.OnDark)
		}
	})
}

// runeAtCell maps a cell offset to a rune index in line.
func runeAtCell(line []rune, cell int) int {
	used := 0
	for i, r := range line {
		w := renderer.StringWidth(string(r))
		if used+w > cell {
			return i
		}
		used += w
	}
	return len(line)
}
