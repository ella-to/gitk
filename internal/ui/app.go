// Package ui is gitk's terminal interface, built with ella.to/microui.
package ui

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"ella.to/gitk/internal/graph"
	"ella.to/gitk/internal/repo"
	"ella.to/microui"
	"ella.to/microui/renderer"
)

// Options configures the UI.
type Options struct {
	Scope   repo.Scope
	Revs    []string // the revisions ScopeRevs shows
	Light   bool
	RelTime bool
}

type pane int

const (
	paneCommits pane = iota
	paneDiff
	paneFiles
	paneRefs
)

type rowKind int

const (
	rowCommit rowKind = iota
	rowUnstaged
	rowStaged
)

type listRow struct {
	kind rowKind
	c    *repo.Commit
	g    *graph.Row
}

// App is the whole UI state. It is owned by the UI goroutine; background
// work hands results back as closures on results.
type App struct {
	rp   *repo.Repo
	term *renderer.Terminal
	ctx  *microui.Context
	p    pen
	th   theme
	dark bool
	quit context.CancelFunc

	results chan func()
	frameNo int

	// history
	g       *repo.Graph
	rows    []listRow
	scope   repo.Scope
	revs    []string
	loading bool
	loadSeq int
	fp      string
	fpBusy  bool

	changes    []repo.Change
	statusBusy bool
	statusAt   time.Time
	statusTook time.Duration

	// selection
	sel    int
	list   scroller
	picked map[repo.Hash]bool
	anchor int
	marked repo.Hash
	back   []repo.Hash
	fwd    []repo.Hash

	// details
	det        *details
	detKey     string
	detRow     string
	detPending bool
	detCache   map[string]*details
	detReqs    chan detailJob
	diffCtx    int
	diffScroll scroller
	diffX      int
	fileSel    int
	fileScroll scroller

	// refs sidebar
	showRefs  bool
	refSel    int
	refScroll scroller

	// layout
	focus     pane
	split     int
	vsplit    int
	refsW     int
	resizing  int // 0 none, 1 horizontal split, 2 vertical split, 3 refs width
	rects     layoutRects
	badgeHits []badgeHit

	// find
	findOpen  bool
	findFocus bool
	query     string
	lastQuery string
	matches   []int

	// overlays
	menu *menu
	dlg  *dialog

	diffMarked bool

	// mouse
	hoverRow  int
	drag      dragState
	prevDown  microui.MouseButton
	lastClick time.Time
	clickPos  microui.Vec2

	// operations
	busy      string
	undo      []*repo.Op
	redo      []*repo.Op
	msg       string
	msgErr    bool
	msgAt     time.Time
	relTime   bool
	selectOpt repo.Hash
}

type layoutRects struct {
	header, commits, split, diff, vdiv, files, refs, status microui.Rect
}

type badgeHit struct {
	r   microui.Rect
	ref repo.Ref
	row int
}

type dragState struct {
	pressed  bool
	active   bool
	startRow int
	start    microui.Vec2
	target   *repo.Ref
}

type detailJob struct {
	seq int
	req detailReq
}

// Run opens the terminal and runs the UI until the user quits.
func Run(rp *repo.Repo, opts Options) error {
	term, err := renderer.NewTerminal()
	if err != nil {
		return err
	}
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := newApp(rp, term, opts)
	a.start(runCtx, cancel)
	return renderer.Run(term, a.frame,
		renderer.WithContext(runCtx),
		renderer.WithFPS(30),
		renderer.WithSetup(func(ctx *microui.Context) { a.th.applyStyle(ctx.Style) }),
	)
}

func newApp(rp *repo.Repo, term *renderer.Terminal, opts Options) *App {
	a := &App{
		rp:       rp,
		term:     term,
		dark:     !opts.Light,
		scope:    opts.Scope,
		revs:     opts.Revs,
		relTime:  opts.RelTime,
		results:  make(chan func(), 256),
		picked:   map[repo.Hash]bool{},
		detCache: map[string]*details{},
		detReqs:  make(chan detailJob, 1),
		diffCtx:  3,
		refsW:    28,
		fileSel:  -1,
		hoverRow: -1,
	}
	a.setTheme()
	return a
}

// start launches the background workers and the first load.
func (a *App) start(ctx context.Context, quit context.CancelFunc) {
	a.quit = quit
	go a.detailWorker(ctx)
	go a.poller(ctx)
	a.reload(repo.Hash{})
}

func (a *App) setTheme() {
	if a.dark {
		a.th = darkTheme()
	} else {
		a.th = lightTheme()
	}
	a.term.BG = a.th.BG
	if a.ctx != nil {
		a.th.applyStyle(a.ctx.Style)
	}
}

// post queues fn to run on the UI goroutine.
func (a *App) post(fn func()) {
	a.results <- fn
}

func (a *App) drain() {
	for {
		select {
		case fn := <-a.results:
			fn()
		default:
			return
		}
	}
}

func (a *App) say(format string, args ...any) {
	a.msg, a.msgErr, a.msgAt = fmt.Sprintf(format, args...), false, time.Now()
}

func (a *App) fail(err error) {
	a.msg, a.msgErr, a.msgAt = err.Error(), true, time.Now()
}

// ---- loading ---------------------------------------------------------------

// reload reloads history and status in the background, then selects sel
// (or keeps the current selection).
func (a *App) reload(sel repo.Hash) {
	a.loadSeq++
	seq := a.loadSeq
	a.loading = true
	if sel.IsZero() {
		if c := a.selCommit(); c != nil {
			sel = c.Hash
		}
	}
	keepRow := a.sel
	rp, scope, revs, colors := a.rp, a.scope, a.revs, len(a.th.Lanes)
	go func() {
		g, err := rp.Load(scope, revs...)
		var rows []graph.Row
		if err == nil {
			rows = graph.Layout(hashes(g), func(i int) []repo.Hash { return g.Commits[i].Parents }, colors)
		}
		fp := rp.Fingerprint()
		a.post(func() {
			if seq != a.loadSeq {
				return
			}
			a.loading = false
			if err != nil {
				a.fail(err)
				return
			}
			a.fp = fp
			a.setGraph(g, rows)
			a.statusAt = time.Time{}
			a.refreshStatus()
			if i := a.rowOf(sel); i >= 0 {
				a.selectRow(i, false)
			} else {
				a.selectRow(min(keepRow, len(a.rows)-1), false)
			}
		})
	}()
}

func hashes(g *repo.Graph) []repo.Hash {
	out := make([]repo.Hash, len(g.Commits))
	for i, c := range g.Commits {
		out[i] = c.Hash
	}
	return out
}

func (a *App) setGraph(g *repo.Graph, layout []graph.Row) {
	a.g = g
	a.rebuildRows(layout)
	for h := range a.picked {
		if g.Commit(h) == nil {
			delete(a.picked, h)
		}
	}
	if !a.marked.IsZero() && g.Commit(a.marked) == nil {
		a.marked = repo.Hash{}
	}
	a.detCache = map[string]*details{}
	a.detKey = ""
	a.lastQuery = "\x00" // recompute matches
}

func (a *App) rebuildRows(layout []graph.Row) {
	if layout == nil && a.g != nil {
		// reuse the existing layout (status changed only)
		layout = make([]graph.Row, 0, len(a.g.Commits))
		for _, r := range a.rows {
			if r.kind == rowCommit {
				layout = append(layout, *r.g)
			}
		}
	}
	var staged, unstaged bool
	for _, c := range a.changes {
		staged = staged || c.Staged != 0
		unstaged = unstaged || c.Unstaged != 0
	}
	var sel repo.Hash
	selKind := rowCommit
	if a.sel >= 0 && a.sel < len(a.rows) {
		selKind = a.rows[a.sel].kind
		if c := a.rows[a.sel].c; c != nil {
			sel = c.Hash
		}
	}
	rows := make([]listRow, 0, len(layout)+2)
	if unstaged {
		rows = append(rows, listRow{kind: rowUnstaged})
	}
	if staged {
		rows = append(rows, listRow{kind: rowStaged})
	}
	if a.g != nil {
		for i, c := range a.g.Commits {
			if i < len(layout) {
				rows = append(rows, listRow{kind: rowCommit, c: c, g: &layout[i]})
			}
		}
	}
	a.rows = rows
	switch {
	case selKind != rowCommit:
		a.sel = 0
		for i, r := range rows {
			if r.kind == selKind {
				a.sel = i
			}
		}
	case !sel.IsZero():
		if i := a.rowOf(sel); i >= 0 {
			a.sel = i
		}
	}
	a.sel = min(max(a.sel, 0), max(len(a.rows)-1, 0))
}

func (a *App) rowOf(h repo.Hash) int {
	if h.IsZero() || a.g == nil {
		return -1
	}
	i, ok := a.g.Index[h]
	if !ok {
		return -1
	}
	return i + a.pseudoRows()
}

func (a *App) pseudoRows() int {
	n := 0
	for _, r := range a.rows {
		if r.kind == rowCommit {
			break
		}
		n++
	}
	return n
}

func (a *App) refreshStatus() {
	if a.statusBusy {
		return
	}
	a.statusBusy = true
	rp := a.rp
	go func() {
		t0 := time.Now()
		changes, err := rp.Status()
		took := time.Since(t0)
		a.post(func() {
			a.statusBusy = false
			a.statusAt = time.Now()
			a.statusTook = took
			if err != nil {
				return
			}
			if !sameChanges(changes, a.changes) {
				a.changes = changes
				a.rebuildRows(nil)
				a.detKey = ""
			}
		})
	}()
}

func sameChanges(a, b []repo.Change) bool {
	return slices.Equal(a, b)
}

// poller notices commits, checkouts and edits made outside gitk.
func (a *App) poller(ctx context.Context) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		fp := a.rp.Fingerprint()
		a.post(func() {
			if a.loading || a.busy != "" {
				return
			}
			if a.fp != "" && fp != a.fp {
				a.say("repository changed on disk: reloaded")
				a.reload(repo.Hash{})
				return
			}
			// poll status less often on repositories where it is slow
			if time.Since(a.statusAt) > max(4*time.Second, 10*a.statusTook) {
				a.refreshStatus()
			}
		})
	}
}

// ---- details ---------------------------------------------------------------

func (a *App) detailWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-a.detReqs:
			d := buildDetails(a.rp, job.req)
			a.post(func() {
				a.detCache[d.key] = d
				if len(a.detCache) > 128 {
					a.detCache = map[string]*details{d.key: d}
				}
				if d.key == a.detKey {
					a.det = d
					a.detPending = false
					a.clampFileSel()
				}
			})
		}
	}
}

// wantDetails returns the details request for the current selection.
func (a *App) wantDetails() (detailReq, string, bool) {
	if a.g == nil || a.sel < 0 || a.sel >= len(a.rows) {
		return detailReq{}, "", false
	}
	r := a.rows[a.sel]
	req := detailReq{g: a.g, ctx: a.diffCtx, changes: a.changes}
	switch r.kind {
	case rowUnstaged:
		req.kind = detUnstaged
		return req, "unstaged", true
	case rowStaged:
		req.kind = detStaged
		return req, "staged", true
	}
	req.hash = r.c.Hash
	if !a.marked.IsZero() && a.marked != r.c.Hash && a.diffMarked {
		req.kind, req.other = detRange, a.marked
		return req, "range/" + a.marked.String() + r.c.Hash.String(), true
	}
	req.kind = detCommit
	return req, "commit/" + r.c.Hash.String(), true
}

func (a *App) updateDetails() {
	req, rowKey, ok := a.wantDetails()
	if !ok {
		a.det = nil
		return
	}
	key := req.key()
	if rowKey != a.detRow {
		a.detRow = rowKey
		a.diffScroll.top, a.diffX = 0, 0
		a.fileSel = -1
		a.fileScroll.top = 0
	}
	if key == a.detKey {
		return
	}
	a.detKey = key
	if d, ok := a.detCache[key]; ok {
		a.det = d
		a.detPending = false
		a.clampFileSel()
		return
	}
	a.detPending = true
	select {
	case <-a.detReqs: // replace a stale pending request
	default:
	}
	a.detReqs <- detailJob{req: req}
}

func (a *App) clampFileSel() {
	if a.det == nil || len(a.det.files) == 0 {
		a.fileSel = -1
		return
	}
	a.fileSel = min(a.fileSel, len(a.det.files)-1)
}

// ---- selection -------------------------------------------------------------

func (a *App) selCommit() *repo.Commit {
	if a.sel >= 0 && a.sel < len(a.rows) {
		return a.rows[a.sel].c
	}
	return nil
}

func (a *App) selectRow(i int, remember bool) {
	if len(a.rows) == 0 {
		a.sel = 0
		return
	}
	i = min(max(i, 0), len(a.rows)-1)
	if remember && i != a.sel {
		if c := a.selCommit(); c != nil {
			a.back = append(a.back, c.Hash)
			if len(a.back) > 200 {
				a.back = a.back[1:]
			}
			a.fwd = a.fwd[:0]
		}
	}
	a.sel = i
	a.list.reveal(i, max(a.rects.commits.H, 1), len(a.rows))
}

func (a *App) jumpTo(h repo.Hash) {
	if i := a.rowOf(h); i >= 0 {
		a.selectRow(i, true)
		a.focus = paneCommits
		return
	}
	a.fail(fmt.Errorf("commit %s is not in the loaded history (press a to show all refs)", h.String()[:7]))
}

func (a *App) historyStep(backward bool) {
	from, to := &a.back, &a.fwd
	if !backward {
		from, to = &a.fwd, &a.back
	}
	for len(*from) > 0 {
		h := (*from)[len(*from)-1]
		*from = (*from)[:len(*from)-1]
		if i := a.rowOf(h); i >= 0 {
			if c := a.selCommit(); c != nil {
				*to = append(*to, c.Hash)
			}
			a.sel = i
			a.list.reveal(i, max(a.rects.commits.H, 1), len(a.rows))
			return
		}
	}
}

// pickedCommits returns the multi-selection oldest first, or the selected
// commit alone.
func (a *App) pickedCommits() []repo.Hash {
	var out []repo.Hash
	for h := range a.picked {
		out = append(out, h)
	}
	if len(out) == 0 {
		if c := a.selCommit(); c != nil {
			out = append(out, c.Hash)
		}
		return out
	}
	slices.SortFunc(out, func(x, y repo.Hash) int { return cmp.Compare(a.g.Index[y], a.g.Index[x]) })
	return out
}

// ---- operations ------------------------------------------------------------

// run executes a repository operation in the background and records it for
// undo. The graph is reloaded afterwards.
func (a *App) run(title string, fn func() (*repo.Op, error)) {
	if a.busy != "" {
		a.fail(fmt.Errorf("busy: %s", a.busy))
		return
	}
	a.busy = title
	go func() {
		op, err := fn()
		a.post(func() {
			a.busy = ""
			if err != nil {
				a.fail(err)
				a.reload(repo.Hash{})
				return
			}
			a.undo = append(a.undo, op)
			a.redo = nil
			note := ""
			if op.Note != "" {
				note = " · " + op.Note
			}
			a.say("%s%s · u to undo", op.Title, note)
			a.picked = map[repo.Hash]bool{}
			a.reload(op.Select)
		})
	}()
}

func (a *App) undoOp(redo bool) {
	from, to := &a.undo, &a.redo
	verb := "undid"
	if redo {
		from, to = &a.redo, &a.undo
		verb = "redid"
	}
	if len(*from) == 0 {
		a.fail(fmt.Errorf("nothing to %s", strings.TrimSuffix(verb, "id")+"o"))
		return
	}
	if a.busy != "" {
		a.fail(fmt.Errorf("busy: %s", a.busy))
		return
	}
	op := (*from)[len(*from)-1]
	a.busy = verb
	rp := a.rp
	go func() {
		inv := op.Inverse()
		inv.Title = op.Title
		err := rp.Apply(inv)
		a.post(func() {
			a.busy = ""
			if err != nil {
				a.fail(fmt.Errorf("%s failed: %w", verb, err))
				return
			}
			*from = (*from)[:len(*from)-1]
			*to = append(*to, inv)
			a.say("%s: %s", verb, op.Title)
			a.reload(repo.Hash{})
		})
	}()
}
