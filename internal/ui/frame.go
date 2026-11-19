package ui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"ella.to/gitk/internal/repo"
	"ella.to/microui"
)

const canvasOpt = microui.OptNoTitle | microui.OptNoResize | microui.OptNoScroll | microui.OptNoFrame

var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func (a *App) frame(ctx *microui.Context) {
	a.ctx = ctx
	a.p = pen{ctx: ctx, th: &a.th}
	a.frameNo++
	a.drain()

	w, h := a.term.Size()
	screen := microui.Rect{W: w, H: h}
	a.layout(w, h)
	a.updateDetails()
	a.updateMatches()
	overlay := a.menu != nil || a.dlg != nil

	if ctx.BeginWindowEx("gitk", screen, canvasOpt).Active() {
		ctx.GetCurrentContainer().Rect = screen
		a.p.fill(screen, a.th.BG)
		if !overlay {
			a.handleMouse()
			if ctx.Focus() == 0 {
				a.handleKeys()
			}
		}
		a.drawHeader(overlay)
		a.drawCommits()
		a.drawSplit()
		a.drawDiff()
		a.drawFiles()
		if a.showRefs {
			a.drawRefs()
		}
		a.drawStatus(overlay)
		a.drawDrag()
		ctx.EndWindow()
	}
	if a.menu != nil || a.dlg != nil {
		if ctx.BeginWindowEx("overlay", screen, canvasOpt).Active() {
			win := ctx.GetCurrentContainer()
			win.Rect = screen
			ctx.BringToFront(win)
			if a.dlg != nil {
				a.drawDialog(screen)
			} else {
				a.drawMenu(screen)
			}
			ctx.EndWindow()
		}
	}
	a.prevDown = ctx.MouseDown()
}

func clampi(v, lo, hi int) int { return max(lo, min(v, hi)) }

func (a *App) layout(w, h int) {
	r := &a.rects
	r.header = microui.Rect{W: w, H: 1}
	r.status = microui.Rect{Y: h - 1, W: w, H: 1}
	bodyY, bodyH := 1, max(h-2, 0)
	x0 := 0
	if a.showRefs {
		a.refsW = clampi(a.refsW, 14, max(w/2, 14))
		r.refs = microui.Rect{Y: bodyY, W: a.refsW, H: bodyH}
		x0 = a.refsW + 1
	}
	if a.split == 0 {
		a.split = bodyY + bodyH*48/100
	}
	a.split = clampi(a.split, bodyY+3, bodyY+bodyH-4)
	r.commits = microui.Rect{X: x0, Y: bodyY, W: w - x0, H: a.split - bodyY}
	r.split = microui.Rect{X: x0, Y: a.split, W: w - x0, H: 1}
	by := a.split + 1
	bh := max(bodyY+bodyH-by, 0)
	if a.vsplit == 0 {
		a.vsplit = x0 + (w-x0)*68/100
	}
	a.vsplit = clampi(a.vsplit, x0+20, w-14)
	r.diff = microui.Rect{X: x0, Y: by, W: a.vsplit - x0, H: bh}
	r.vdiv = microui.Rect{X: a.vsplit, Y: by, W: 1, H: bh}
	r.files = microui.Rect{X: a.vsplit + 1, Y: by, W: w - a.vsplit - 1, H: bh}
}

// ---- header and status -----------------------------------------------------

func (a *App) clicked(r microui.Rect, overlay bool) bool {
	return !overlay && a.ctx.MousePressed()&microui.MouseLeft != 0 && r.Contains(a.ctx.MousePos())
}

func (a *App) drawHeader(overlay bool) {
	p, r := a.p, a.rects.header
	p.fill(r, a.th.Header)
	x := p.badge(r.X, r.Y, "gitk", a.th.Accent, a.th.OnDark)
	x = p.text(x+1, r.Y, a.rp.Name, a.th.Text)
	if a.g != nil {
		if a.g.HeadRef != "" {
			x = p.text(x+2, r.Y, "on ", a.th.Faint)
			x = p.text(x, r.Y, a.g.HeadRef, a.th.Green)
		} else if !a.g.Head.IsZero() {
			x = p.text(x+2, r.Y, "detached at "+a.g.Head.String()[:7], a.th.Yellow)
		}
		if n := len(a.changes); n > 0 {
			x = p.text(x+2, r.Y, fmt.Sprintf("● %d changed", n), a.th.Orange)
		}
		branches := 0
		for _, ref := range a.g.Refs {
			if ref.Kind == repo.RefLocal {
				branches++
			}
		}
		x = p.text(x+2, r.Y, plural2(len(a.g.Commits), "1 commit", "commits")+" · "+plural2(branches, "1 branch", "branches"), a.th.Faint)
	}
	// right-aligned chips
	chips := []struct {
		label string
		on    bool
		fn    func()
	}{
		{"refs", a.showRefs, func() { a.showRefs = !a.showRefs }},
		{map[repo.Scope]string{repo.ScopeAll: "all refs", repo.ScopeHead: "HEAD only"}[a.scope], true, a.toggleScope},
		{"? help", false, a.showHelp},
	}
	cx := r.X + r.W
	for i := len(chips) - 1; i >= 0; i-- {
		c := chips[i]
		cw := textWidth(c.label) + 2
		cx -= cw + 1
		if cx <= x+1 {
			break
		}
		cr := microui.Rect{X: cx, Y: r.Y, W: cw, H: 1}
		bg, fg := a.th.Border, a.th.Dim
		if c.on {
			bg, fg = a.th.Sel, a.th.Text
		}
		p.badge(cx, r.Y, c.label, bg, fg)
		if a.clicked(cr, overlay) {
			c.fn()
		}
	}
	if a.loading || a.busy != "" {
		s := spinner[(a.frameNo/2)%len(spinner)]
		label := "loading"
		if a.busy != "" {
			label = a.busy
		}
		p.text(x+2, r.Y, s+" "+label+"…", a.th.Yellow)
	}
}

func (a *App) toggleScope() {
	if a.scope == repo.ScopeAll {
		a.scope = repo.ScopeHead
	} else {
		a.scope = repo.ScopeAll
	}
	a.reload(repo.Hash{})
}

func (a *App) drawStatus(overlay bool) {
	p, ctx, r := a.p, a.ctx, a.rects.status
	p.fill(r, a.th.Header)
	if a.findOpen {
		x := p.text(r.X+1, r.Y, "find", a.th.Accent)
		info := "no matches"
		if len(a.matches) > 0 {
			cur := slices.Index(a.matches, a.sel) + 1
			info = fmt.Sprintf("%d/%d", cur, len(a.matches))
			if cur == 0 {
				info = fmt.Sprintf("%d matches", len(a.matches))
			}
		}
		hint := info + " · Enter next · n/N · Esc close"
		boxW := max(r.W-x-textWidth(hint)-4, 10)
		ctx.LayoutSetNext(microui.Rect{X: x + 1, Y: r.Y, W: boxW, H: 1}, false)
		res := ctx.Textbox(&a.query)
		id := ctx.LastID()
		if a.findFocus {
			ctx.SetFocus(id)
			a.findFocus = false
		}
		if res.Changed() {
			a.updateMatches()
			a.nextMatch(true, true)
		}
		if res.Submitted() {
			a.nextMatch(true, false)
		}
		if ctx.Focus() == id && ctx.KeyPressed()&microui.KeyEscape != 0 {
			a.findOpen = false
			a.query = ""
			ctx.SetFocus(0)
		}
		p.text(x+boxW+3, r.Y, hint, a.th.Faint)
		return
	}
	if a.msg != "" && (time.Since(a.msgAt) < 6*time.Second || (a.msgErr && time.Since(a.msgAt) < 15*time.Second)) {
		c := a.th.Green
		prefix := "✓ "
		if a.msgErr {
			c, prefix = a.th.Red, "✗ "
		}
		p.textIn(r.X+1, r.Y, r.W-2, prefix+a.msg, c)
		return
	}
	hints := map[pane]string{
		paneCommits: "j/k move · space select · c pick · r reword · b branch · o checkout · d drop · u undo · / find · ? help",
		paneDiff:    "j/k scroll · ←/→ pan · ,/. prev/next file · [ ] context · Tab next pane · ? help",
		paneFiles:   "j/k move · Enter show · y copy path · Tab next pane · ? help",
		paneRefs:    "j/k move · Enter jump · o checkout · R rename · D delete · ? help",
	}
	h := hints[a.focus]
	switch a.selKind() {
	case rowUnstaged:
		h = "space stage file · S stage all · D discard file · C commit · Tab files · ? help"
	case rowStaged:
		h = "space unstage file · S unstage all · C commit · A amend · Tab files · ? help"
	}
	p.textIn(r.X+1, r.Y, r.W-2, h, a.th.Faint)
}

// ---- commits pane ----------------------------------------------------------

func (a *App) commitColumns(r microui.Rect, graphW int) (gw, subjEnd, authorX, dateX, hashX int) {
	gw = min(max(graphW, 2), max(r.W/3, 8))
	right := r.X + r.W - 1
	authorX, dateX, hashX = -1, -1, -1
	x := right
	if r.W >= 50 {
		x -= 8
		hashX = x
	}
	if r.W >= 70 {
		dw := 16
		if a.relTime {
			dw = 13
		}
		x -= dw + 1
		dateX = x
	}
	if r.W >= 100 {
		x -= 19
		authorX = x
	}
	return gw, x - 1, authorX, dateX, hashX
}

func refColors(th *theme, r repo.Ref) (bg, fg microui.Color, label string) {
	switch {
	case r.Kind == repo.RefHead:
		return th.Yellow, th.OnDark, "◆ HEAD"
	case r.IsHead:
		return th.BadgeHead, th.OnDark, "◆ " + r.Short
	case r.Kind == repo.RefLocal:
		return th.BadgeLocal, th.OnDark, r.Short
	case r.Kind == repo.RefRemote:
		return th.BadgeRemote.Lerp(th.BG, 0.35), th.OnDark, r.Short
	}
	return th.BadgeTag, th.OnDark, "⚑ " + r.Short
}

func (a *App) drawCommits() {
	p, r := a.p, a.rects.commits
	view := r.H
	a.list.clamp(len(a.rows), view)
	if a.g == nil {
		msg := "Loading history…"
		if a.msgErr {
			msg = a.msg
		}
		p.text(r.X+2, r.Y+1, msg, a.th.Dim)
		return
	}
	if len(a.rows) == 0 {
		p.text(r.X+2, r.Y+1, "No commits yet. Stage files and press C to make the first one.", a.th.Dim)
	}
	// size the graph column to the rows on screen, not the widest in history
	visW := 0
	for i := a.list.top; i < min(a.list.top+view, len(a.rows)); i++ {
		if g := a.rows[i].g; g != nil {
			visW = max(visW, len(g.Cells))
		}
	}
	gw, subjEnd, authorX, dateX, hashX := a.commitColumns(r, visW)
	a.badgeHits = a.badgeHits[:0]
	now := time.Now()
	focused := a.focus == paneCommits
	p.clip(r, func() {
		for vi := range view {
			i := a.list.top + vi
			if i >= len(a.rows) {
				break
			}
			row := a.rows[i]
			y := r.Y + vi
			line := microui.Rect{X: r.X, Y: y, W: r.W, H: 1}
			picked := row.c != nil && a.picked[row.c.Hash]
			switch {
			case a.drag.active && a.drag.target != nil && a.rowOf(a.drag.target.Hash) == i:
				p.fill(line, a.th.Accent.Lerp(a.th.BG, 0.6))
			case i == a.sel && focused:
				p.fill(line, a.th.Sel)
			case i == a.sel:
				p.fill(line, a.th.SelDim)
			case picked:
				p.fill(line, a.th.Multi)
			case a.isMatch(i):
				p.fill(line, a.th.Match)
			case i == a.hoverRow:
				p.fill(line, a.th.Hover)
			}
			if picked {
				p.text(r.X, y, "▌", a.th.Purple)
			}
			gx := r.X + 1
			tx := gx + gw + 1
			switch row.kind {
			case rowUnstaged, rowStaged:
				c, label := a.th.Red, "Local changes, not staged"
				var n int
				for _, ch := range a.changes {
					if (row.kind == rowUnstaged && ch.Unstaged != 0) || (row.kind == rowStaged && ch.Staged != 0) {
						n++
					}
				}
				if row.kind == rowStaged {
					c, label = a.th.Green, "Staged changes, ready to commit"
				}
				col := 0
				if hr := a.rowOf(a.g.Head); hr >= 0 {
					col = a.rows[hr].g.Col
				}
				p.text(gx+2*min(col, gw/2), y, "◌", c)
				x := p.text(tx, y, label, c)
				p.text(x+2, y, plural2(n, "1 file", "files"), a.th.Faint)
				continue
			}
			c := row.c
			for ci, cell := range row.g.Cells {
				if ci >= gw {
					break
				}
				if ci == 2*row.g.Col {
					glyph := "●"
					switch {
					case c.Hash == a.g.Head:
						glyph = "◉"
					case len(c.Parents) > 1:
						glyph = "○"
					}
					p.text(gx+ci, y, glyph, a.th.Lanes[row.g.Color%len(a.th.Lanes)])
					continue
				}
				if cell.R != 0 && cell.R != ' ' {
					p.text(gx+ci, y, string(cell.R), a.th.Lanes[cell.Color%len(a.th.Lanes)])
				}
			}
			x := tx
			refs := a.g.RefsAt[c.Hash]
			for ri, ref := range refs {
				bg, fg, label := refColors(&a.th, ref)
				bw := textWidth(label) + 2
				if x+bw > subjEnd-12 && ri > 0 {
					x = p.text(x, y, fmt.Sprintf("+%d ", len(refs)-ri), a.th.Faint)
					break
				}
				nx := p.badge(x, y, label, bg, fg)
				a.badgeHits = append(a.badgeHits, badgeHit{r: microui.Rect{X: x, Y: y, W: nx - x, H: 1}, ref: ref, row: i})
				x = nx + 1
			}
			if c.Hash == a.marked {
				x = p.text(x, y, "★ ", a.th.Purple)
			}
			subjColor := a.th.Text
			if len(c.Parents) > 1 {
				subjColor = a.th.Dim
			}
			p.textIn(x, y, subjEnd-x, c.Subject, subjColor)
			if authorX >= 0 {
				p.textIn(authorX, y, 18, c.Author, a.th.Dim)
			}
			if dateX >= 0 {
				d := absTime(c.AuthorTime)
				if a.relTime {
					d = relTime(c.AuthorTime, now)
				}
				p.text(dateX, y, d, a.th.Faint)
			}
			if hashX >= 0 {
				p.text(hashX, y, c.Short(), a.th.Yellow.Lerp(a.th.BG, 0.25))
			}
		}
	})
	p.scrollbar(r, a.list.top, view, len(a.rows))
}

func (a *App) drawSplit() {
	p, r := a.p, a.rects.split
	c := a.th.Border
	if a.resizing == 1 || (r.Contains(a.ctx.MousePos()) && a.menu == nil && a.dlg == nil) {
		c = a.th.Accent
	}
	p.fill(r, a.th.BG)
	p.text(r.X, r.Y, strings.Repeat("─", r.W), c)
	var parts []string
	if row := a.sel; row >= 0 && row < len(a.rows) {
		if cm := a.rows[row].c; cm != nil {
			parts = append(parts, cm.Short())
		}
		parts = append(parts, fmt.Sprintf("%d/%d", row+1, len(a.rows)))
	}
	if n := len(a.picked); n > 0 {
		parts = append(parts, fmt.Sprintf("%d selected", n))
	}
	if !a.marked.IsZero() {
		s := "★ " + a.marked.String()[:7]
		if a.diffMarked {
			s += " (diffing)"
		}
		parts = append(parts, s)
	}
	if len(a.undo) > 0 {
		parts = append(parts, "undo: "+a.undo[len(a.undo)-1].Title)
	}
	p.textIn(r.X+2, r.Y, r.W-4, " "+strings.Join(parts, " · ")+" ", a.th.Dim)
}

// ---- details pane ----------------------------------------------------------

func (a *App) drawDiff() {
	p, r := a.p, a.rects.diff
	if r.H <= 0 {
		return
	}
	focused := a.focus == paneDiff
	title := "Details"
	if a.det != nil && len(a.det.files) > 0 {
		title = fmt.Sprintf("Details · %s · context %d", plural2(len(a.det.files), "1 file", "files"), a.diffCtx)
	}
	if a.detPending {
		title += " " + spinner[(a.frameNo/2)%len(spinner)]
	}
	p.frame(microui.Rect{X: r.X, Y: r.Y, W: r.W, H: r.H}, title, focused)
	body := microui.Rect{X: r.X, Y: r.Y + 1, W: r.W, H: r.H - 1}
	p.fill(body, a.th.BG)
	d := a.det
	if d == nil {
		return
	}
	if d.err != nil {
		p.textIn(body.X+1, body.Y, body.W-2, "✗ "+d.err.Error(), a.th.Red)
		return
	}
	view := body.H
	a.diffScroll.clamp(len(d.lines), view)
	numW := 0
	if body.W > 70 {
		numW = 4
	}
	mouse := a.ctx.MousePos()
	p.clip(body, func() {
		for vi := range view {
			i := a.diffScroll.top + vi
			if i >= len(d.lines) {
				break
			}
			l := d.lines[i]
			y := body.Y + vi
			line := microui.Rect{X: body.X, Y: y, W: body.W - 1, H: 1}
			x := body.X + 1
			switch l.kind {
			case dTitle:
				p.text(x, y, l.text, a.th.Accent)
			case dMeta, dLink:
				x = p.text(x, y, padRight(l.label, 11), a.th.Faint)
				c := a.th.Text
				switch l.label {
				case "Commit:":
					c = a.th.Yellow
				case "Branches:":
					c = a.th.Green
				case "Follows:", "Precedes:":
					c = a.th.BadgeTag
				}
				if l.kind == dLink {
					if line.Contains(mouse) {
						p.fill(line, a.th.Hover)
					}
					hash, rest, _ := strings.Cut(l.text, "  ")
					x = p.text(x, y, hash, a.th.Accent)
					p.textIn(x+2, y, line.W-(x-line.X)-3, rest, a.th.Dim)
					continue
				}
				p.textIn(x, y, line.W-(x-line.X)-1, l.text, c)
			case dMsg:
				c := a.th.Text
				if i > 0 && d.lines[i-1].kind == dBlank && (i < 2 || d.lines[i-2].kind != dMsg) {
					c = a.th.Text.Lerp(a.th.Accent, 0.3) // subject line
				}
				p.text(x+3, y, skipCells(l.text, a.diffX), c)
			case dFile:
				p.fill(line, a.th.FileBG)
				x = p.text(x, y, l.label+" ", statusColor(&a.th, l.label[0]))
				x = p.text(x, y, l.text, a.th.Text)
				if l.file >= 0 && l.file < len(d.files) {
					f := d.files[l.file]
					x = p.text(x+2, y, fmt.Sprintf("+%d", f.adds), a.th.Green)
					p.text(x+1, y, fmt.Sprintf("−%d", f.dels), a.th.Red)
				}
			case dHunk:
				p.fill(line, a.th.HunkBG)
				p.text(x, y, l.text, a.th.Cyan.Lerp(a.th.BG, 0.3))
			case dAdd, dDel, dCtx:
				bg, hi, sign, sc := microui.Color{}, microui.Color{}, " ", a.th.Faint
				switch l.kind {
				case dAdd:
					bg, hi, sign, sc = a.th.AddBG, a.th.AddHi, "+", a.th.Green
				case dDel:
					bg, hi, sign, sc = a.th.DelBG, a.th.DelHi, "-", a.th.Red
				}
				if bg.A != 0 {
					p.fill(line, bg)
				}
				if numW > 0 {
					old, nw := "", ""
					if l.old > 0 {
						old = strconv.Itoa(l.old)
					}
					if l.new > 0 {
						nw = strconv.Itoa(l.new)
					}
					p.text(x, y, padLeft(old, numW), a.th.Faint)
					p.text(x+numW+1, y, padLeft(nw, numW), a.th.Faint)
					x += 2*numW + 2
				}
				x = p.text(x, y, sign, sc)
				tx := x
				for _, h := range l.hl {
					x0 := tx + textWidth(l.text[:h[0]]) - a.diffX
					x1 := tx + textWidth(l.text[:h[1]]) - a.diffX
					x0 = max(x0, tx)
					if x1 > x0 {
						p.fill(microui.Rect{X: x0, Y: y, W: x1 - x0, H: 1}, hi)
					}
				}
				c := a.th.Text
				if l.kind == dCtx {
					c = a.th.Dim
				}
				p.text(tx, y, skipCells(l.text, a.diffX), c)
			case dInfo:
				p.textIn(x, y, line.W-2, l.text, a.th.Faint)
			}
		}
	})
	p.scrollbar(body, a.diffScroll.top, view, len(d.lines))

	// the files pane follows the diff while it is scrolled
	if a.focus != paneFiles {
		if f := d.fileAt(min(a.diffScroll.top+1, len(d.lines)-1)); f >= 0 {
			a.fileSel = f
		}
	}
}

func statusColor(th *theme, s byte) microui.Color {
	switch s {
	case 'A', '?':
		return th.Green
	case 'D':
		return th.Red
	case 'R':
		return th.Purple
	}
	return th.Yellow
}

func (a *App) drawFiles() {
	p, r := a.p, a.rects.files
	if r.H <= 0 || r.W <= 0 {
		return
	}
	v := a.rects.vdiv
	vc := a.th.Border
	if a.resizing == 2 {
		vc = a.th.Accent
	}
	for y := v.Y; y < v.Y+v.H; y++ {
		p.text(v.X, y, "│", vc)
	}
	d := a.det
	n := 0
	if d != nil {
		n = len(d.files)
	}
	p.frame(r, fmt.Sprintf("Files (%d)", n), a.focus == paneFiles)
	body := microui.Rect{X: r.X, Y: r.Y + 1, W: r.W, H: r.H - 1}
	p.fill(body, a.th.BG)
	if d == nil {
		return
	}
	a.fileScroll.clamp(n, body.H)
	local := a.selKind() != rowCommit
	p.clip(body, func() {
		for vi := range body.H {
			i := a.fileScroll.top + vi
			if i >= n {
				break
			}
			f := d.files[i]
			y := body.Y + vi
			line := microui.Rect{X: body.X, Y: y, W: body.W - 1, H: 1}
			if i == a.fileSel {
				c := a.th.SelDim
				if a.focus == paneFiles {
					c = a.th.Sel
				}
				p.fill(line, c)
			}
			st := f.status
			if local && f.change.Unstaged == '?' {
				st = '?'
			}
			x := p.text(body.X+1, y, string(st), statusColor(&a.th, st))
			stats := fmt.Sprintf("+%d −%d", f.adds, f.dels)
			room := line.W - (x - line.X) - textWidth(stats) - 2
			name := f.name
			if textWidth(name) > room && room > 2 {
				name = "…" + skipCells(name, textWidth(name)-room+1)
			}
			p.textIn(x+1, y, room, name, a.th.Text)
			sx := line.X + line.W - textWidth(stats)
			sx = p.text(sx, y, fmt.Sprintf("+%d", f.adds), a.th.Green.Lerp(a.th.BG, 0.3))
			p.text(sx+1, y, fmt.Sprintf("−%d", f.dels), a.th.Red.Lerp(a.th.BG, 0.3))
		}
	})
	p.scrollbar(body, a.fileScroll.top, body.H, n)
}

// ---- refs sidebar ----------------------------------------------------------

type refEntry struct {
	header string
	ref    repo.Ref
}

func (a *App) refEntries() []refEntry {
	if a.g == nil {
		return nil
	}
	var out []refEntry
	sections := []struct {
		title string
		kinds []repo.RefKind
	}{
		{"BRANCHES", []repo.RefKind{repo.RefHead, repo.RefLocal}},
		{"REMOTES", []repo.RefKind{repo.RefRemote}},
		{"TAGS", []repo.RefKind{repo.RefTag}},
	}
	for _, s := range sections {
		added := false
		for _, r := range a.g.Refs {
			if slices.Contains(s.kinds, r.Kind) {
				if !added {
					out = append(out, refEntry{header: s.title})
					added = true
				}
				out = append(out, refEntry{ref: r})
			}
		}
	}
	return out
}

func (a *App) drawRefs() {
	p, r := a.p, a.rects.refs
	entries := a.refEntries()
	p.frame(r, "Refs", a.focus == paneRefs)
	body := microui.Rect{X: r.X, Y: r.Y + 1, W: r.W, H: r.H - 1}
	p.fill(body, a.th.Panel)
	dc := a.th.Border
	if a.resizing == 3 {
		dc = a.th.Accent
	}
	for y := r.Y; y < r.Y+r.H; y++ {
		p.text(r.X+r.W, y, "│", dc)
	}
	a.refSel = clampi(a.refSel, 0, max(len(entries)-1, 0))
	a.refScroll.clamp(len(entries), body.H)
	p.clip(body, func() {
		for vi := range body.H {
			i := a.refScroll.top + vi
			if i >= len(entries) {
				break
			}
			e := entries[i]
			y := body.Y + vi
			if e.header != "" {
				p.text(body.X+1, y, e.header, a.th.Faint)
				continue
			}
			line := microui.Rect{X: body.X, Y: y, W: body.W - 1, H: 1}
			if i == a.refSel {
				c := a.th.SelDim
				if a.focus == paneRefs {
					c = a.th.Sel
				}
				p.fill(line, c)
			}
			if a.drag.active && a.drag.target != nil && a.drag.target.Name == e.ref.Name {
				p.fill(line, a.th.Accent.Lerp(a.th.BG, 0.6))
			}
			_, _, label := refColors(&a.th, e.ref)
			c := a.th.Text
			switch e.ref.Kind {
			case repo.RefRemote:
				c = a.th.Purple
			case repo.RefTag:
				c = a.th.Yellow
			}
			if e.ref.IsHead {
				c = a.th.Accent
			}
			p.textIn(body.X+2, y, body.W-3, label, c)
		}
	})
}

// ---- drag and drop ---------------------------------------------------------

func (a *App) drawDrag() {
	if !a.drag.active {
		return
	}
	p, m := a.p, a.ctx.MousePos()
	picks := a.pickedCommits()
	label := "⇢ " + plural2(len(picks), shortLine(a.g, picks[0]), "commits")
	if a.drag.target != nil {
		label += "  →  " + a.drag.target.Short
	} else {
		label += "  (drop on a branch)"
	}
	label = truncate(label, 60)
	x := min(m.X+2, a.rects.header.W-textWidth(label)-2)
	y := min(m.Y+1, a.rects.status.Y-1)
	bg := a.th.Border
	if a.drag.target != nil {
		bg = a.th.Accent
	}
	p.badge(max(x, 0), y, label, bg, a.th.OnDark)
}

// dropTarget finds the branch under the mouse while dragging.
func (a *App) dropTarget(m microui.Vec2) *repo.Ref {
	if a.showRefs && a.rects.refs.Contains(m) {
		entries := a.refEntries()
		i := a.refScroll.top + m.Y - a.rects.refs.Y - 1
		if i >= 0 && i < len(entries) && entries[i].header == "" && entries[i].ref.Kind == repo.RefLocal {
			r := entries[i].ref
			return &r
		}
		return nil
	}
	if !a.rects.commits.Contains(m) {
		return nil
	}
	i := a.list.top + m.Y - a.rects.commits.Y
	if i < 0 || i >= len(a.rows) || i == a.drag.startRow || a.rows[i].c == nil {
		return nil
	}
	for _, b := range a.badgeHits {
		if b.row == i && b.r.Contains(m) && (b.ref.Kind == repo.RefLocal || b.ref.Kind == repo.RefHead) {
			r := b.ref
			return &r
		}
	}
	for _, r := range a.g.RefsAt[a.rows[i].c.Hash] {
		if r.Kind == repo.RefLocal || r.Kind == repo.RefHead {
			return &r
		}
	}
	return nil
}

func (a *App) drop() {
	t := a.drag.target
	if t == nil {
		return
	}
	if t.Kind == repo.RefHead || t.IsHead {
		a.actCherryPick("")
		return
	}
	a.actCherryPick(t.Short)
}

// ---- mouse -----------------------------------------------------------------

func (a *App) handleMouse() {
	ctx := a.ctx
	m := ctx.MousePos()
	pressed, down := ctx.MousePressed(), ctx.MouseDown()
	scroll := ctx.ScrollDelta()
	rc := a.rects

	if a.resizing != 0 {
		if down&microui.MouseLeft == 0 {
			a.resizing = 0
			return
		}
		switch a.resizing {
		case 1:
			a.split = m.Y
		case 2:
			a.vsplit = m.X
		case 3:
			a.refsW = m.X
		case 4: // commit scrollbar drag
			view := rc.commits.H
			a.list.top = (m.Y - rc.commits.Y) * max(len(a.rows)-view, 0) / max(view-1, 1)
		case 5:
			if a.det != nil {
				view := rc.diff.H - 1
				a.diffScroll.top = (m.Y - rc.diff.Y - 1) * max(len(a.det.lines)-view, 0) / max(view-1, 1)
			}
		}
		return
	}

	if pressed&microui.MouseLeft != 0 {
		switch {
		case rc.split.Contains(m):
			a.resizing = 1
			return
		case rc.vdiv.Contains(m):
			a.resizing = 2
			return
		case a.showRefs && m.X == rc.refs.X+rc.refs.W && m.Y >= rc.refs.Y:
			a.resizing = 3
			return
		case rc.commits.Contains(m) && m.X == rc.commits.X+rc.commits.W-1 && len(a.rows) > rc.commits.H:
			a.resizing = 4
			return
		case rc.diff.Contains(m) && m.X == rc.diff.X+rc.diff.W-1 && m.Y > rc.diff.Y:
			a.resizing = 5
			return
		}
	}

	if scroll.Y != 0 || scroll.X != 0 {
		switch {
		case rc.commits.Contains(m):
			a.list.top += scroll.Y
		case rc.diff.Contains(m):
			a.diffScroll.top += scroll.Y
			a.diffX = max(a.diffX+scroll.X, 0)
		case rc.files.Contains(m):
			a.fileScroll.top += scroll.Y
		case a.showRefs && rc.refs.Contains(m):
			a.refScroll.top += scroll.Y
		}
	}

	double := false
	if pressed != 0 {
		double = time.Since(a.lastClick) < 400*time.Millisecond && a.clickPos == m
		a.lastClick, a.clickPos = time.Now(), m
	}

	a.hoverRow = -1
	if rc.commits.Contains(m) {
		i := a.list.top + m.Y - rc.commits.Y
		if i < len(a.rows) {
			a.hoverRow = i
			a.commitsClick(i, m, pressed, double)
		}
	}

	if a.drag.pressed {
		switch {
		case down&microui.MouseLeft == 0:
			if a.drag.active {
				a.drop()
			}
			a.drag = dragState{}
		case !a.drag.active && m.Y != a.drag.start.Y && a.drag.startRow < len(a.rows) && a.rows[a.drag.startRow].c != nil:
			a.drag.active = true
		}
		if a.drag.active {
			a.drag.target = a.dropTarget(m)
			// scroll the list when dragging past its edges
			if m.Y <= rc.commits.Y && a.frameNo%3 == 0 {
				a.list.top--
			}
			if m.Y >= rc.commits.Y+rc.commits.H-1 && m.Y <= rc.split.Y && a.frameNo%3 == 0 {
				a.list.top++
			}
		}
	}

	if rc.diff.Contains(m) && m.Y > rc.diff.Y && pressed != 0 && a.det != nil {
		a.focus = paneDiff
		i := a.diffScroll.top + m.Y - rc.diff.Y - 1
		if i < len(a.det.lines) && pressed&microui.MouseLeft != 0 {
			l := a.det.lines[i]
			if l.kind == dLink {
				a.jumpTo(l.link)
			}
		}
		if i < len(a.det.lines) && pressed&microui.MouseRight != 0 {
			if f := a.det.fileAt(i); f >= 0 {
				a.fileSel = f
				a.openMenu(a.det.files[f].path, a.fileMenu(), m)
			}
		}
	}

	if rc.files.Contains(m) && m.Y > rc.files.Y && pressed != 0 && a.det != nil {
		a.focus = paneFiles
		i := a.fileScroll.top + m.Y - rc.files.Y - 1
		if i < len(a.det.files) {
			a.fileSel = i
			a.showFile(i)
			if double && a.selKind() != rowCommit {
				a.actToggleStage()
			}
			if pressed&microui.MouseRight != 0 {
				a.openMenu(a.det.files[i].path, a.fileMenu(), m)
			}
		}
	}

	if a.showRefs && rc.refs.Contains(m) && m.Y > rc.refs.Y && pressed != 0 {
		a.focus = paneRefs
		entries := a.refEntries()
		i := a.refScroll.top + m.Y - rc.refs.Y - 1
		if i < len(entries) && entries[i].header == "" {
			a.refSel = i
			ref := entries[i].ref
			a.jumpTo(ref.Hash)
			a.focus = paneRefs
			switch {
			case pressed&microui.MouseRight != 0:
				a.openMenu(ref.Short, a.refMenu(ref), m)
			case double:
				a.actCheckoutRef(ref)
			}
		}
	}
}

func (a *App) commitsClick(i int, m microui.Vec2, pressed microui.MouseButton, double bool) {
	if pressed == 0 {
		return
	}
	a.focus = paneCommits
	var badge *repo.Ref
	for _, b := range a.badgeHits {
		if b.row == i && b.r.Contains(m) {
			r := b.ref
			badge = &r
		}
	}
	row := a.rows[i]
	mods := a.ctx.KeyDown()
	if pressed&microui.MouseRight != 0 {
		if badge != nil {
			a.selectRow(i, true)
			a.openMenu(badge.Short, a.refMenu(*badge), m)
			return
		}
		if row.c == nil || !a.picked[row.c.Hash] {
			a.picked = map[repo.Hash]bool{}
		}
		a.selectRow(i, true)
		a.openMenu(a.menuTitle(), a.commitMenu(), m)
		return
	}
	if pressed&microui.MouseLeft == 0 {
		return
	}
	switch {
	case mods&microui.KeyCtrl != 0 && row.c != nil:
		a.togglePick(row.c.Hash)
	case mods&microui.KeyShift != 0 && row.c != nil:
		lo, hi := min(a.anchor, i), max(a.anchor, i)
		for j := lo; j <= hi; j++ {
			if c := a.rows[j].c; c != nil {
				a.picked[c.Hash] = true
			}
		}
	default:
		if row.c == nil || !a.picked[row.c.Hash] {
			a.picked = map[repo.Hash]bool{}
		}
		a.anchor = i
	}
	a.selectRow(i, true)
	switch {
	case double && badge != nil:
		a.actCheckoutRef(*badge)
	case double:
		a.focus = paneDiff
	default:
		a.drag = dragState{pressed: true, startRow: i, start: m}
	}
}

func (a *App) togglePick(h repo.Hash) {
	if a.picked[h] {
		delete(a.picked, h)
	} else {
		a.picked[h] = true
	}
}

// showFile scrolls the diff to file i.
func (a *App) showFile(i int) {
	if a.det == nil || i < 0 || i >= len(a.det.files) {
		return
	}
	a.diffScroll.top = a.det.files[i].line
	a.fileScroll.reveal(i, max(a.rects.files.H-1, 1), len(a.det.files))
}
