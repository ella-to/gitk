package ui

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"ella.to/microui"
	"ella.to/microui/renderer"
)

// textWidth is the number of terminal cells s occupies.
func textWidth(s string) int { return renderer.StringWidth(s) }

// truncate cuts s to at most w cells, ending in "…" when it was cut.
func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if textWidth(s) <= w {
		return s
	}
	var b strings.Builder
	used := 0
	for _, r := range s {
		rw := renderer.StringWidth(string(r))
		if used+rw > w-1 {
			break
		}
		b.WriteRune(r)
		used += rw
	}
	b.WriteRune('…')
	return b.String()
}

// skipCells drops the first n cells of s (for horizontal scrolling).
func skipCells(s string, n int) string {
	if n <= 0 {
		return s
	}
	used := 0
	for i, r := range s {
		if used >= n {
			return s[i:]
		}
		used += renderer.StringWidth(string(r))
	}
	return ""
}

// padRight pads s with spaces to w cells (truncating when longer).
func padRight(s string, w int) string {
	s = truncate(s, w)
	if n := w - textWidth(s); n > 0 {
		s += strings.Repeat(" ", n)
	}
	return s
}

// sanitize expands tabs and replaces control characters so text occupies
// predictable cells.
func sanitize(s string) string {
	if !strings.ContainsFunc(s, func(r rune) bool { return r == '\t' || unicode.IsControl(r) }) {
		return s
	}
	var b strings.Builder
	col := 0
	for _, r := range s {
		switch {
		case r == '\t':
			n := 4 - col%4
			b.WriteString(strings.Repeat(" ", n))
			col += n
		case r == '\r':
		case unicode.IsControl(r):
			b.WriteRune('·')
			col++
		default:
			b.WriteRune(r)
			col += renderer.StringWidth(string(r))
		}
	}
	return b.String()
}

// pen draws into the current microui container.
type pen struct {
	ctx *microui.Context
	th  *theme
}

func (p pen) fill(r microui.Rect, c microui.Color) {
	if !r.Empty() {
		p.ctx.DrawRect(r, c)
	}
}

// text draws s at (x, y) and returns the x after it.
func (p pen) text(x, y int, s string, c microui.Color) int {
	if s == "" {
		return x
	}
	p.ctx.DrawText(p.ctx.Style.Font, s, microui.Vec2{X: x, Y: y}, c)
	return x + textWidth(s)
}

// textIn draws s clipped to w cells.
func (p pen) textIn(x, y, w int, s string, c microui.Color) int {
	return p.text(x, y, truncate(s, w), c)
}

// badge draws " label " on a colored background and returns the x after it.
func (p pen) badge(x, y int, label string, bg, fg microui.Color) int {
	s := " " + label + " "
	p.fill(microui.Rect{X: x, Y: y, W: textWidth(s), H: 1}, bg)
	return p.text(x, y, s, fg)
}

func (p pen) clip(r microui.Rect, fn func()) {
	p.ctx.PushClipRect(r)
	fn()
	p.ctx.PopClipRect()
}

// box draws a panel with a 1-cell border-line title row.
func (p pen) frame(r microui.Rect, title string, active bool) {
	p.fill(r, p.th.Panel)
	c := p.th.Faint
	if active {
		c = p.th.Accent
	}
	line := strings.Repeat("─", max(r.W, 0))
	p.text(r.X, r.Y, line, p.th.Border)
	if title != "" {
		p.text(r.X+1, r.Y, " "+title+" ", c)
	}
}

// scroller is the scroll state of a virtual list.
type scroller struct {
	top int
}

// clamp keeps top within [0, total-view].
func (s *scroller) clamp(total, view int) {
	s.top = min(s.top, max(total-view, 0))
	s.top = max(s.top, 0)
}

// reveal scrolls so index i is visible with a small margin.
func (s *scroller) reveal(i, view, total int) {
	margin := min(2, max(view/4, 0))
	if i < s.top+margin {
		s.top = i - margin
	}
	if i >= s.top+view-margin {
		s.top = i - view + margin + 1
	}
	s.clamp(total, view)
}

// scrollbar draws a 1-cell bar on the right edge of r for a list of total
// rows showing view of them from top. It returns the thumb rect.
func (p pen) scrollbar(r microui.Rect, top, view, total int) microui.Rect {
	if total <= view || r.H <= 0 {
		return microui.Rect{}
	}
	x := r.X + r.W - 1
	p.fill(microui.Rect{X: x, Y: r.Y, W: 1, H: r.H}, p.th.Panel)
	h := max(r.H*view/total, 1)
	y := r.Y + (r.H-h)*top/max(total-view, 1)
	thumb := microui.Rect{X: x, Y: y, W: 1, H: h}
	p.fill(thumb, p.th.Faint)
	return thumb
}

func relTime(t time.Time, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < 0:
		return t.Format("2006-01-02 15:04")
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return plural(int(d.Minutes()), "min") + " ago"
	case d < 24*time.Hour:
		return plural(int(d.Hours()), "hour") + " ago"
	case d < 48*time.Hour:
		return "yesterday"
	case d < 30*24*time.Hour:
		return plural(int(d.Hours()/24), "day") + " ago"
	case t.Year() == now.Year():
		return t.Format("Jan 02 15:04")
	}
	return t.Format("2006-01-02")
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

func absTime(t time.Time) string { return t.Local().Format("2006-01-02 15:04") }
