package ui

import (
	"strings"

	"ella.to/microui"
)

// menuItem is one entry of a context menu. An item with sub opens a nested
// menu; a zero item (no label) is a separator.
type menuItem struct {
	label    string
	key      string
	disabled bool
	danger   bool
	action   func()
	sub      func() []menuItem
}

type menuLevel struct {
	title string
	items []menuItem
	sel   int
}

// menu is an open context menu (with a stack of submenus).
type menu struct {
	x, y   int
	levels []menuLevel
	opened int
}

func (a *App) openMenu(title string, items []menuItem, at microui.Vec2) {
	m := &menu{x: at.X, y: at.Y, opened: a.frameNo}
	m.levels = []menuLevel{{title: title, items: items, sel: firstEnabled(items, 0, 1)}}
	a.menu = m
}

func firstEnabled(items []menuItem, from, dir int) int {
	for i := 0; i < len(items); i++ {
		j := ((from+dir*i)%len(items) + len(items)) % len(items)
		if items[j].label != "" && !items[j].disabled {
			return j
		}
	}
	return -1
}

// drawMenu draws the open menu and handles its input.
func (a *App) drawMenu(screen microui.Rect) {
	m, ctx, p := a.menu, a.ctx, a.p
	lv := &m.levels[len(m.levels)-1]
	w := textWidth(lv.title) + 4
	for _, it := range lv.items {
		w = max(w, textWidth(it.label)+textWidth(it.key)+7)
	}
	w = min(w, screen.W)
	h := min(len(lv.items)+2, screen.H)
	x := min(m.x+2*(len(m.levels)-1), screen.W-w)
	y := min(m.y, screen.H-h)
	box := microui.Rect{X: max(x, 0), Y: max(y, 0), W: w, H: h}

	p.fill(microui.Rect{X: box.X + 1, Y: box.Y + 1, W: box.W, H: box.H}, microui.RGBA(0, 0, 0, 90))
	p.fill(box, a.th.Header)
	p.textIn(box.X+1, box.Y, box.W-2, lv.title, a.th.Faint)

	fresh := m.opened == a.frameNo
	mouse := ctx.MousePos()
	activate := -1
	for i, it := range lv.items {
		ry := box.Y + 1 + i
		if ry >= box.Y+box.H-1 {
			break
		}
		r := microui.Rect{X: box.X, Y: ry, W: box.W, H: 1}
		if it.label == "" {
			p.text(r.X+1, ry, strings.Repeat("─", r.W-2), a.th.Border)
			continue
		}
		hover := r.Contains(mouse)
		if hover && !it.disabled && ctx.MouseDelta() != (microui.Vec2{}) {
			lv.sel = i
		}
		fg := a.th.Text
		switch {
		case it.disabled:
			fg = a.th.Faint
		case it.danger:
			fg = a.th.Red
		}
		if i == lv.sel && !it.disabled {
			p.fill(r, a.th.Sel)
		}
		label := it.label
		if it.sub != nil {
			label += " ▸"
		}
		p.textIn(r.X+2, ry, r.W-textWidth(it.key)-4, label, fg)
		if it.key != "" {
			p.text(r.X+r.W-1-textWidth(it.key), ry, it.key, a.th.Yellow)
		}
		if !fresh && hover && !it.disabled && ctx.MousePressed()&microui.MouseLeft != 0 {
			activate = i
		}
	}

	if fresh {
		return
	}
	if ctx.MousePressed() != 0 && !box.Contains(mouse) {
		a.menu = nil
		return
	}
	k := ctx.KeyPressed()
	switch {
	case k&microui.KeyEscape != 0, k&microui.KeyLeft != 0:
		if len(m.levels) > 1 {
			m.levels = m.levels[:len(m.levels)-1]
		} else {
			a.menu = nil
		}
		return
	case k&microui.KeyDown != 0:
		lv.sel = firstEnabled(lv.items, lv.sel+1, 1)
	case k&microui.KeyUp != 0:
		lv.sel = firstEnabled(lv.items, lv.sel-1+len(lv.items), -1)
	case k&(microui.KeyReturn|microui.KeyRight) != 0:
		activate = lv.sel
	}
	if t := ctx.TextInput(); t != "" {
		switch t {
		case "j":
			lv.sel = firstEnabled(lv.items, lv.sel+1, 1)
		case "k":
			lv.sel = firstEnabled(lv.items, lv.sel-1+len(lv.items), -1)
		case "q":
			a.menu = nil
			return
		default:
			for i, it := range lv.items {
				if it.key == t && !it.disabled {
					activate = i
				}
			}
		}
	}
	if activate < 0 || activate >= len(lv.items) {
		return
	}
	it := lv.items[activate]
	if it.disabled || it.label == "" {
		return
	}
	if it.sub != nil {
		items := it.sub()
		m.levels = append(m.levels, menuLevel{title: it.label, items: items, sel: firstEnabled(items, 0, 1)})
		m.opened = a.frameNo
		return
	}
	a.menu = nil
	if it.action != nil {
		it.action()
	}
}
