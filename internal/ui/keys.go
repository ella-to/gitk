package ui

import (
	"encoding/hex"
	"slices"
	"strings"
	"unicode"

	"ella.to/gitk/internal/repo"
	"ella.to/microui"
)

func (a *App) handleKeys() {
	ctx := a.ctx
	k := ctx.KeyPressed()
	shift := (k|ctx.KeyDown())&microui.KeyShift != 0
	switch {
	case k&microui.KeyTab != 0:
		a.cycleFocus(shift)
	case k&microui.KeyEscape != 0:
		switch {
		case a.findOpen:
			a.findOpen, a.query = false, ""
		case len(a.picked) > 0:
			a.picked = map[repo.Hash]bool{}
		case a.diffMarked:
			a.diffMarked = false
		case a.focus != paneCommits:
			a.focus = paneCommits
		}
	case k&microui.KeyUp != 0:
		a.move(-1, shift)
	case k&microui.KeyDown != 0:
		a.move(1, shift)
	case k&microui.KeyPageUp != 0:
		a.move(-a.pageSize(), false)
	case k&microui.KeyPageDown != 0:
		a.move(a.pageSize(), false)
	case k&microui.KeyHome != 0:
		a.move(-1<<30, false)
	case k&microui.KeyEnd != 0:
		a.move(1<<30, false)
	case k&microui.KeyLeft != 0:
		if a.focus == paneDiff {
			a.diffX = max(a.diffX-8, 0)
		} else {
			a.historyStep(true)
		}
	case k&microui.KeyRight != 0:
		if a.focus == paneDiff {
			a.diffX += 8
		} else {
			a.historyStep(false)
		}
	case k&microui.KeyReturn != 0:
		a.enter()
	}
	for _, r := range ctx.TextInput() {
		if a.menu != nil || a.dlg != nil {
			break // a key opened an overlay: it gets the rest
		}
		a.key(r)
	}
}

func (a *App) pageSize() int {
	switch a.focus {
	case paneDiff:
		return max(a.rects.diff.H-2, 1)
	case paneFiles:
		return max(a.rects.files.H-2, 1)
	}
	return max(a.rects.commits.H-1, 1)
}

func (a *App) cycleFocus(back bool) {
	order := []pane{paneCommits, paneDiff, paneFiles}
	if a.showRefs {
		order = append([]pane{paneRefs}, order...)
	}
	i := slices.Index(order, a.focus)
	if back {
		i--
	} else {
		i++
	}
	a.focus = order[(i+len(order))%len(order)]
}

func (a *App) move(d int, extend bool) {
	switch a.focus {
	case paneCommits:
		next := clampi(a.sel+d, 0, max(len(a.rows)-1, 0))
		if extend {
			for _, i := range []int{a.sel, next} {
				if i >= 0 && i < len(a.rows) && a.rows[i].c != nil {
					a.picked[a.rows[i].c.Hash] = true
				}
			}
		}
		a.selectRow(next, false)
	case paneDiff:
		a.diffScroll.top += d
		if a.det != nil {
			a.diffScroll.clamp(len(a.det.lines), max(a.rects.diff.H-1, 1))
		}
	case paneFiles:
		if a.det == nil || len(a.det.files) == 0 {
			return
		}
		a.fileSel = clampi(a.fileSel+d, 0, len(a.det.files)-1)
		a.showFile(a.fileSel)
	case paneRefs:
		entries := a.refEntries()
		i := clampi(a.refSel+d, 0, max(len(entries)-1, 0))
		for i < len(entries)-1 && entries[i].header != "" && d > 0 {
			i++
		}
		for i > 0 && entries[i].header != "" && d < 0 {
			i--
		}
		if i == 0 && len(entries) > 1 && entries[0].header != "" {
			i = 1
		}
		a.refSel = i
		a.refScroll.reveal(i, max(a.rects.refs.H-1, 1), len(entries))
		if i < len(entries) && entries[i].header == "" {
			if r := a.rowOf(entries[i].ref.Hash); r >= 0 {
				a.selectRow(r, false)
			}
		}
	}
}

func (a *App) enter() {
	switch a.focus {
	case paneCommits:
		a.focus = paneDiff
	case paneFiles:
		a.showFile(a.fileSel)
		a.focus = paneDiff
	case paneRefs:
		if e := a.refEntries(); a.refSel < len(e) && e[a.refSel].header == "" {
			a.jumpTo(e[a.refSel].ref.Hash)
		}
	}
}

func (a *App) selectedRef() (repo.Ref, bool) {
	e := a.refEntries()
	if a.refSel < len(e) && e[a.refSel].header == "" {
		return e[a.refSel].ref, true
	}
	return repo.Ref{}, false
}

// key handles one typed character.
func (a *App) key(r rune) {
	local := a.selKind() != rowCommit
	switch r {
	case 'q':
		a.quit()
	case '?':
		a.showHelp()
	case '/':
		a.findOpen, a.findFocus = true, true
	case 'n':
		a.nextMatch(true, false)
	case 'N':
		a.nextMatch(false, false)
	case 'R':
		if a.focus == paneRefs {
			if ref, ok := a.selectedRef(); ok && ref.Kind == repo.RefLocal {
				a.actRenameBranch(ref)
				return
			}
		}
		a.reload(repo.Hash{})
		a.say("reloaded")
	case 'a':
		a.toggleScope()
	case '\\':
		a.showRefs = !a.showRefs
		if !a.showRefs && a.focus == paneRefs {
			a.focus = paneCommits
		}
	case 'T':
		a.relTime = !a.relTime
	case 'L':
		a.dark = !a.dark
		a.setTheme()
	case '[':
		a.diffCtx = max(a.diffCtx-1, 0)
	case ']':
		a.diffCtx = min(a.diffCtx+1, 50)
	case 'u':
		a.undoOp(false)
	case 'U':
		a.undoOp(true)
	case 'j':
		a.move(1, false)
	case 'k':
		a.move(-1, false)
	case 'g':
		a.move(-1<<30, false)
	case 'G':
		a.move(1<<30, false)
	case ',', '.':
		if a.det != nil && len(a.det.files) > 0 {
			d := 1
			if r == ',' {
				d = -1
			}
			a.fileSel = clampi(a.fileSel+d, 0, len(a.det.files)-1)
			a.showFile(a.fileSel)
		}
	case 'p':
		if c := a.selCommit(); c != nil && len(c.Parents) > 0 {
			a.jumpTo(c.Parents[0])
		}
	case 'y':
		if a.focus == paneFiles && a.det != nil && a.fileSel >= 0 && a.fileSel < len(a.det.files) {
			a.copy(a.det.files[a.fileSel].path, "path")
		} else if c := a.selCommit(); c != nil {
			a.copy(c.Hash.String(), "hash "+c.Short())
		}
	case 'Y':
		if c := a.selCommit(); c != nil {
			a.copy(c.Subject, "subject")
		}
	case ' ':
		switch {
		case local || a.focus == paneFiles:
			a.actToggleStage()
		default:
			if c := a.selCommit(); c != nil {
				a.togglePick(c.Hash)
				a.selectRow(a.sel+1, false)
			}
		}
	case 'S':
		a.actStageAll()
	case 'C':
		a.actCommit(false)
	case 'A':
		a.actCommit(true)
	case 'D':
		if a.focus == paneRefs {
			if ref, ok := a.selectedRef(); ok && ref.Kind != repo.RefRemote && ref.Kind != repo.RefHead {
				a.actDeleteRef(ref)
			}
			return
		}
		a.actDiscard()
	case 'o':
		if a.focus == paneRefs {
			if ref, ok := a.selectedRef(); ok {
				a.actCheckoutRef(ref)
			}
			return
		}
		a.actCheckout()
	}
	if local {
		return
	}
	switch r {
	case 'c':
		a.actCherryPick("")
	case 'r':
		a.actReword()
	case 'v':
		a.actRevert()
	case 's':
		a.actSquash(false)
	case 'f':
		a.actSquash(true)
	case 'K':
		a.actMove(true)
	case 'J':
		a.actMove(false)
	case 'd':
		a.actDrop()
	case 'b':
		a.actNewBranch()
	case 't':
		a.actNewTag()
	case 'x':
		y := a.rects.commits.Y + a.sel - a.list.top
		a.openMenu("Reset "+a.headLabel()+" here", a.resetMenu(), microui.Vec2{X: a.rects.commits.X + 12, Y: y + 1})
	case 'm':
		a.actMark()
	case '=':
		a.actDiffMarked()
	}
}

// ---- find ------------------------------------------------------------------

func (a *App) updateMatches() {
	if a.query == a.lastQuery {
		return
	}
	a.lastQuery = a.query
	a.matches = a.matches[:0]
	q := strings.TrimSpace(a.query)
	if q == "" || a.g == nil {
		return
	}
	fold := !strings.ContainsFunc(q, unicode.IsUpper)
	if fold {
		q = strings.ToLower(q)
	}
	contains := func(s string) bool {
		if fold {
			s = strings.ToLower(s)
		}
		return strings.Contains(s, q)
	}
	isHex := len(q) >= 4
	if _, err := hex.DecodeString(q[:len(q)&^1]); err != nil {
		isHex = false
	}
	for i, row := range a.rows {
		c := row.c
		if c == nil {
			continue
		}
		hit := (isHex && strings.HasPrefix(c.Hash.String(), strings.ToLower(q))) ||
			contains(c.Message) || contains(c.Author) || contains(c.AuthorEmail)
		if !hit {
			for _, r := range a.g.RefsAt[c.Hash] {
				if contains(r.Short) {
					hit = true
					break
				}
			}
		}
		if hit {
			a.matches = append(a.matches, i)
		}
	}
}

func (a *App) isMatch(i int) bool {
	_, ok := slices.BinarySearch(a.matches, i)
	return ok && a.query != ""
}

// nextMatch selects the next (or previous) match; inclusive also accepts
// the current row (incremental search while typing).
func (a *App) nextMatch(forward, inclusive bool) {
	if len(a.matches) == 0 {
		if a.query != "" {
			a.fail(errNoMatch(a.query))
		}
		return
	}
	i, found := slices.BinarySearch(a.matches, a.sel)
	switch {
	case forward && found && !inclusive:
		i++
	case !forward:
		i--
	}
	i = (i%len(a.matches) + len(a.matches)) % len(a.matches)
	a.selectRow(a.matches[i], true)
}

type errNoMatch string

func (e errNoMatch) Error() string { return "no commit matches " + strings.TrimSpace(string(e)) }
