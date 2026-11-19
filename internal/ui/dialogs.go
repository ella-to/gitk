package ui

import (
	"strconv"
	"strings"

	"ella.to/microui"
)

type dialogKind int

const (
	dlgConfirm dialogKind = iota
	dlgInput
	dlgMessage
	dlgHelp
)

// dialog is a modal box: a confirmation, a one-line input, a commit message
// editor or the help screen.
type dialog struct {
	kind   dialogKind
	title  string
	body   []string
	ok     string
	danger bool

	value      string // dlgInput value / dlgMessage subject
	extra      string // label of an optional second input
	extraValue string
	check      string // optional checkbox label
	checked    bool
	area       *textArea // dlgMessage body

	onOK     func(d *dialog) error
	err      string
	opened   int
	focusSet bool
	subject  microui.ID
	second   microui.ID
}

func (a *App) openDialog(d *dialog) *dialog {
	d.opened = a.frameNo
	if a.ctx != nil {
		a.ctx.SetFocus(0)
	}
	a.dlg = d
	a.menu = nil
	return d
}

// confirm asks a yes/no question.
func (a *App) confirm(title string, body []string, ok string, danger bool, fn func()) *dialog {
	return a.openDialog(&dialog{kind: dlgConfirm, title: title, body: body, ok: ok, danger: danger,
		onOK: func(*dialog) error { fn(); return nil }})
}

// input asks for one line of text, with an optional checkbox.
func (a *App) input(title string, body []string, initial, check string, checked bool, ok string, fn func(value string, checked bool) error) *dialog {
	d := &dialog{kind: dlgInput, title: title, body: body, ok: ok, value: initial, check: check, checked: checked}
	if fn != nil {
		d.onOK = func(d *dialog) error { return fn(d.value, d.checked) }
	}
	return a.openDialog(d)
}

// messageDialog edits a commit message: a subject line plus a body.
func (a *App) messageDialog(title, note, initial, check, ok string, fn func(msg string, checked bool) error) *dialog {
	subject, body, _ := strings.Cut(strings.TrimLeft(initial, "\n"), "\n")
	body = strings.Trim(body, "\n")
	d := &dialog{kind: dlgMessage, title: title, ok: ok, value: subject, check: check, area: newTextArea(body)}
	if note != "" {
		d.body = []string{note}
	}
	d.onOK = func(d *dialog) error {
		msg := strings.TrimSpace(d.value)
		if b := strings.TrimSpace(d.area.String()); b != "" {
			msg += "\n\n" + d.area.String()
		}
		return fn(msg, d.checked)
	}
	return a.openDialog(d)
}

func (a *App) showHelp() {
	a.openDialog(&dialog{kind: dlgHelp, title: "gitk · keys and mouse", ok: "Close"})
}

var helpText = [][2]string{
	{"", "Navigate"},
	{"j k ↑ ↓", "move selection"},
	{"PgUp PgDn g G", "page / first / last"},
	{"Tab", "next pane (commits, diff, files)"},
	{"Enter", "open the diff / jump to file"},
	{"← →", "back / forward in jump history"},
	{"p", "jump to first parent"},
	{"/  n  N", "find, next, previous match"},
	{"\\", "toggle the refs sidebar"},
	{"a", "all refs ⟷ HEAD only"},
	{"", "History"},
	{"c", "cherry-pick selection onto HEAD"},
	{"drag", "drop commits on a branch to pick"},
	{"r", "reword (edit message)"},
	{"v", "revert"},
	{"s  f", "squash / fixup into parent"},
	{"K  J", "move commit up / down"},
	{"d", "drop commit"},
	{"space", "toggle in multi-selection"},
	{"u  U", "undo / redo"},
	{"", "Refs"},
	{"b  t", "new branch / tag here"},
	{"o", "checkout"},
	{"x", "reset current branch here"},
	{"", "Compare"},
	{"m", "mark commit"},
	{"=", "diff selection against mark"},
	{"[  ]", "less / more diff context"},
	{"", "Local changes"},
	{"space", "stage / unstage file"},
	{"S", "stage / unstage all"},
	{"D", "discard file changes"},
	{"C  A", "commit / amend"},
	{"", "Other"},
	{"y  Y", "copy hash / subject"},
	{"right-click", "context menu anywhere"},
	{"R", "reload"},
	{"T  L", "time format / light theme"},
	{"q", "quit"},
}

// wrap breaks s into lines of at most w cells.
func wrap(s string, w int) []string {
	if w <= 0 {
		return nil
	}
	var out []string
	for _, para := range strings.Split(s, "\n") {
		line := ""
		for _, word := range strings.Fields(para) {
			switch {
			case line == "":
				line = word
			case textWidth(line)+1+textWidth(word) <= w:
				line += " " + word
			default:
				out = append(out, line)
				line = word
			}
			for textWidth(line) > w {
				out = append(out, truncate(line, w))
				line = skipCells(line, w-1)
			}
		}
		out = append(out, line)
	}
	return out
}

// drawDialog draws a.dlg centered on screen and handles its input.
func (a *App) drawDialog(screen microui.Rect) {
	d, ctx, p := a.dlg, a.ctx, a.p
	fresh := d.opened == a.frameNo
	if d.kind == dlgHelp {
		a.drawHelp(screen, fresh)
		return
	}
	w := min(screen.W-4, 78)
	inner := w - 4
	var body []string
	for _, b := range d.body {
		body = append(body, wrap(b, inner)...)
	}
	h := 3 + len(body) + 2 // title, gap, body, gap, buttons
	switch d.kind {
	case dlgInput:
		h += 2
		if d.extra != "" {
			h += 3
		}
	case dlgMessage:
		h += 3 + 2 + min(10, max(screen.H-h-12, 3))
	}
	if d.check != "" {
		h += 2
	}
	if d.err != "" {
		h += 2
	}
	h = min(h, screen.H-2)
	box := microui.Rect{X: screen.X + (screen.W-w)/2, Y: screen.Y + max((screen.H-h)/2, 1), W: w, H: h}

	p.fill(screen, microui.RGBA(0, 0, 0, 140))
	p.fill(box, a.th.Panel)
	p.fill(microui.Rect{X: box.X, Y: box.Y, W: box.W, H: 1}, a.th.Header)
	tc := a.th.Accent
	if d.danger {
		tc = a.th.Red
	}
	p.textIn(box.X+2, box.Y, box.W-4, d.title, tc)

	x, y := box.X+2, box.Y+2
	for _, l := range body {
		p.text(x, y, l, a.th.Dim)
		y++
	}
	if len(body) > 0 {
		y++
	}

	submit := false
	switch d.kind {
	case dlgInput:
		ctx.LayoutSetNext(microui.Rect{X: x, Y: y, W: inner, H: 1}, false)
		if ctx.Textbox(&d.value).Submitted() {
			submit = true
		}
		d.subject = ctx.LastID()
		y += 2
		if d.extra != "" {
			p.text(x, y, d.extra, a.th.Dim)
			ctx.LayoutSetNext(microui.Rect{X: x, Y: y + 1, W: inner, H: 1}, false)
			if ctx.Textbox(&d.extraValue).Submitted() {
				submit = true
			}
			d.second = ctx.LastID()
			y += 3
		}
	case dlgMessage:
		p.text(x, y, "Subject", a.th.Dim)
		n := textWidth(d.value)
		lc := a.th.Faint
		if n > 72 {
			lc = a.th.Yellow
		}
		p.text(x+inner-6, y, padLeft(strconv.Itoa(n), 6), lc)
		ctx.LayoutSetNext(microui.Rect{X: x, Y: y + 1, W: inner, H: 1}, false)
		if ctx.Textbox(&d.value).Submitted() {
			submit = true
		}
		d.subject = ctx.LastID()
		y += 3
		p.text(x, y, "Body", a.th.Dim)
		p.text(x+5, y, " (Enter: new line · Tab: switch field)", a.th.Faint)
		areaH := box.Y + box.H - y - 4
		if d.check != "" {
			areaH -= 2
		}
		if d.err != "" {
			areaH -= 2
		}
		d.second = ctx.GetID("message-body")
		d.area.update(ctx, p, d.second, microui.Rect{X: x, Y: y + 1, W: inner, H: max(areaH, 1)})
		y += 1 + max(areaH, 1) + 1
	}
	if d.check != "" {
		ctx.LayoutSetNext(microui.Rect{X: x, Y: y, W: inner, H: 1}, false)
		ctx.Checkbox(d.check, &d.checked)
		y += 2
	}
	if d.err != "" {
		p.textIn(x, y, inner, "✗ "+d.err, a.th.Red)
		y += 2
	}

	// focus the first field when the dialog opens
	if !d.focusSet && d.subject != 0 {
		ctx.SetFocus(d.subject)
		d.focusSet = true
	}

	by := box.Y + box.H - 2
	okW, cancelW := textWidth(d.ok)+4, 10
	okR := microui.Rect{X: box.X + box.W - 2 - okW, Y: by, W: okW, H: 1}
	cancelR := microui.Rect{X: okR.X - 1 - cancelW, Y: by, W: cancelW, H: 1}
	cancel := a.button(cancelR, "Cancel", 0)
	style := 1
	if d.danger {
		style = 2
	}
	if a.button(okR, d.ok, style) {
		submit = true
	}
	if d.kind == dlgConfirm || d.kind == dlgMessage {
		hint := "Enter: " + strings.ToLower(d.ok) + " · Esc: cancel"
		if d.kind == dlgMessage {
			hint = "Enter in subject: " + strings.ToLower(d.ok) + " · Esc: cancel"
		}
		p.textIn(box.X+2, by, cancelR.X-box.X-3, hint, a.th.Faint)
	}

	if !fresh {
		k := ctx.KeyPressed()
		switch {
		case k&microui.KeyEscape != 0:
			cancel = true
		case k&microui.KeyTab != 0 && d.second != 0:
			if ctx.Focus() == d.subject {
				ctx.SetFocus(d.second)
			} else {
				ctx.SetFocus(d.subject)
			}
		case k&microui.KeyReturn != 0 && d.kind == dlgConfirm:
			submit = true
		}
		if d.kind == dlgConfirm && ctx.Focus() == 0 {
			switch strings.ToLower(ctx.TextInput()) {
			case "y":
				submit = true
			case "n", "q":
				cancel = true
			}
		}
	}
	if fresh {
		submit, cancel = false, false
	}
	switch {
	case cancel:
		a.closeDialog()
	case submit:
		if d.onOK == nil {
			a.closeDialog()
			return
		}
		if err := d.onOK(d); err != nil {
			d.err = err.Error()
			return
		}
		if a.dlg == d {
			a.closeDialog()
		}
	}
}

func (a *App) closeDialog() {
	a.dlg = nil
	a.ctx.SetFocus(0)
}

func (a *App) drawHelp(screen microui.Rect, fresh bool) {
	p := a.p
	half := (len(helpText) + 1) / 2
	colW := 44
	w := min(screen.W-2, 2*colW+4)
	h := min(screen.H-2, half+4)
	box := microui.Rect{X: screen.X + (screen.W-w)/2, Y: screen.Y + max((screen.H-h)/2, 0), W: w, H: h}
	p.fill(screen, microui.RGBA(0, 0, 0, 140))
	p.fill(box, a.th.Panel)
	p.fill(microui.Rect{X: box.X, Y: box.Y, W: box.W, H: 1}, a.th.Header)
	p.text(box.X+2, box.Y, a.dlg.title, a.th.Accent)
	p.textIn(box.X+box.W-22, box.Y, 20, "Esc or ? to close", a.th.Faint)
	p.clip(box, func() {
		for i, e := range helpText {
			col, row := i/half, i%half
			x := box.X + 2 + col*(colW+1)
			y := box.Y + 2 + row
			if e[0] == "" {
				p.text(x, y, e[1], a.th.Purple)
				continue
			}
			p.text(x, y, padRight(e[0], 14), a.th.Yellow)
			p.textIn(x+14, y, colW-15, e[1], a.th.Text)
		}
	})
	if fresh {
		return
	}
	k := a.ctx.KeyPressed()
	t := a.ctx.TextInput()
	if k&(microui.KeyEscape|microui.KeyReturn) != 0 || t == "?" || t == "q" ||
		(a.ctx.MousePressed() != 0 && !box.Contains(a.ctx.MousePos())) {
		a.closeDialog()
	}
}

// button draws a clickable button and reports a click. style: 0 normal,
// 1 primary, 2 danger.
func (a *App) button(r microui.Rect, label string, style int) bool {
	ctx := a.ctx
	id := ctx.GetID("button:" + label)
	ctx.UpdateControl(id, r, 0)
	bg, fg := a.th.Border, a.th.Text
	switch style {
	case 1:
		bg, fg = a.th.Accent, a.th.OnDark
	case 2:
		bg, fg = a.th.Red, a.th.OnDark
	}
	if ctx.Hover() == id {
		bg = bg.Lerp(a.th.Text, 0.2)
	}
	a.p.fill(r, bg)
	a.p.text(r.X+(r.W-textWidth(label))/2, r.Y, label, fg)
	return ctx.MousePressed()&microui.MouseLeft != 0 && ctx.Focus() == id
}

func padLeft(s string, w int) string {
	if n := w - textWidth(s); n > 0 {
		return strings.Repeat(" ", n) + s
	}
	return s
}
