package ui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ella.to/gitk/internal/repo"
	"ella.to/microui"
	"ella.to/microui/renderer"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

type harness struct {
	t    *testing.T
	dir  string
	r    *git.Repository
	term *renderer.Terminal
	ctx  *microui.Context
	a    *App
	when time.Time
}

func newHarness(t *testing.T, w, h int) *harness {
	t.Helper()
	dir := t.TempDir()
	r, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := r.Config()
	cfg.User.Name, cfg.User.Email = "Tester", "tester@example.com"
	_ = r.SetConfig(cfg)
	_ = r.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName("main")))
	hs := &harness{t: t, dir: dir, r: r, when: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	return hs.withSize(w, h)
}

func (h *harness) withSize(w, ht int) *harness {
	h.term = renderer.NewHeadless(w, ht)
	return h
}

func (h *harness) commit(msg string, files ...string) repo.Hash {
	h.t.Helper()
	for i := 0; i+1 < len(files); i += 2 {
		full := filepath.Join(h.dir, files[i])
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(files[i+1]), 0o644); err != nil {
			h.t.Fatal(err)
		}
	}
	wt, _ := h.r.Worktree()
	if err := wt.AddWithOptions(&git.AddOptions{All: true}); err != nil {
		h.t.Fatal(err)
	}
	h.when = h.when.Add(time.Minute)
	sig := &object.Signature{Name: "Tester", Email: "tester@example.com", When: h.when}
	c, err := wt.Commit(msg, &git.CommitOptions{Author: sig, Committer: sig})
	if err != nil {
		h.t.Fatal(err)
	}
	return c
}

func (h *harness) checkout(branch string, create bool) {
	h.t.Helper()
	wt, _ := h.r.Worktree()
	if err := wt.Checkout(&git.CheckoutOptions{Branch: plumbing.NewBranchReferenceName(branch), Create: create}); err != nil {
		h.t.Fatal(err)
	}
}

// start opens the UI on the repository and waits for the first load.
func (h *harness) start() {
	h.t.Helper()
	rp, err := repo.Open(h.dir)
	if err != nil {
		h.t.Fatal(err)
	}
	h.ctx = microui.New()
	renderer.Connect(h.ctx, h.term)
	h.a = newApp(rp, h.term, Options{})
	st := renderer.TerminalStyle()
	h.ctx.Style = &st
	h.a.th.applyStyle(h.ctx.Style)
	ctx, cancel := context.WithCancel(context.Background())
	h.t.Cleanup(cancel)
	h.a.quit = cancel
	go h.a.detailWorker(ctx)
	h.a.reload(repo.Hash{})
	h.settle()
}

func (h *harness) frame() {
	h.ctx.Begin()
	h.a.frame(h.ctx)
	h.ctx.End()
	renderer.Draw(h.term, h.ctx, h.term.BG)
}

// settle runs frames until background work is done.
func (h *harness) settle() {
	h.t.Helper()
	for range 400 {
		h.frame()
		if !h.a.loading && !h.a.detPending && !h.a.statusBusy && h.a.busy == "" && h.a.det != nil && len(h.a.results) == 0 {
			h.frame()
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	h.t.Fatalf("UI did not settle\n%s", h.term.Text())
}

// typ and press feed one input frame plus one frame to show its effect.
func (h *harness) typ(s string) {
	h.ctx.InputText(s)
	h.frame()
	h.frame()
}

func (h *harness) press(k microui.Key) {
	h.ctx.InputKeyDown(k)
	h.ctx.InputKeyUp(k)
	h.frame()
	h.frame()
}

func (h *harness) click(x, y int, b microui.MouseButton) {
	h.ctx.InputMouseMove(x, y)
	h.frame()
	h.ctx.InputMouseMove(x, y)
	h.frame()
	h.ctx.InputMouseDown(x, y, b)
	h.frame()
	h.ctx.InputMouseUp(x, y, b)
	h.frame()
}

func (h *harness) screen() string { return h.term.Text() }

func (h *harness) must(substr string) {
	h.t.Helper()
	if !strings.Contains(h.screen(), substr) {
		h.t.Fatalf("screen lacks %q:\n%s", substr, h.screen())
	}
}

// rowY finds the screen row showing text in the commits pane.
func (h *harness) rowY(text string) int {
	h.t.Helper()
	for y, l := range strings.Split(h.screen(), "\n") {
		if y >= h.a.rects.commits.Y && y < h.a.rects.commits.Y+h.a.rects.commits.H && strings.Contains(l, text) {
			return y
		}
	}
	h.t.Fatalf("no row with %q:\n%s", text, h.screen())
	return -1
}

func history(h *harness) {
	h.commit("first commit", "a.txt", "1\n2\n3\n")
	h.commit("feat: add b", "b.txt", "b\n")
	h.checkout("feature", true)
	h.commit("feature: edit a", "a.txt", "1\n2\nTHREE\n")
	h.commit("feature: add c", "c.txt", "c\n")
	h.checkout("main", false)
	h.commit("main: add d", "d.txt", "d\n")
}

func TestRendersGraphRefsAndDetails(t *testing.T) {
	h := newHarness(t, 140, 40)
	history(h)
	h.start()
	h.must("main: add d")
	h.must("◆ main")
	h.must("feature")
	h.must("◉") // HEAD node
	h.must("Author:")
	h.must("d.txt")
	if !strings.ContainsAny(h.screen(), "╯╮╰╭") {
		t.Fatalf("expected lane connectors:\n%s", h.screen())
	}
}

func TestKeyboardRewordAndUndo(t *testing.T) {
	h := newHarness(t, 140, 40)
	history(h)
	h.start()
	h.typ("j") // feature: add c
	h.settle()
	h.typ("r")
	h.must("Reword")
	// replace the subject: select all and type
	h.press(microui.KeySelectAll)
	h.typ("feature: add c, reworded")
	h.press(microui.KeyReturn)
	h.settle()
	h.must("feature: add c, reworded")
	h.typ("u")
	h.settle()
	h.must("feature: add c")
	if strings.Contains(h.screen(), "reworded") {
		t.Fatalf("undo did not restore the message:\n%s", h.screen())
	}
}

func TestCherryPickByDragAndDrop(t *testing.T) {
	h := newHarness(t, 140, 40)
	history(h)
	h.start()
	src := h.rowY("feature: add c")
	dst := h.rowY("main: add d")
	x := 60
	h.ctx.InputMouseMove(x, src)
	h.frame()
	h.ctx.InputMouseMove(x, src)
	h.frame()
	h.ctx.InputMouseDown(x, src, microui.MouseLeft)
	h.frame()
	h.ctx.InputMouseMove(x, dst)
	h.frame()
	h.must("→  main")
	h.ctx.InputMouseUp(x, dst, microui.MouseLeft)
	h.frame()
	h.must("Cherry-pick onto main")
	h.press(microui.KeyReturn)
	h.settle()
	h.must("cherry-pick")
	if _, err := os.Stat(filepath.Join(h.dir, "c.txt")); err != nil {
		t.Fatalf("c.txt not picked into the worktree: %v\n%s", err, h.screen())
	}
}

func TestContextMenuAndFind(t *testing.T) {
	h := newHarness(t, 140, 40)
	history(h)
	h.start()
	y := h.rowY("feat: add b")
	h.click(60, y, microui.MouseRight)
	h.must("Cherry-pick onto main")
	h.must("Reword message…")
	h.press(microui.KeyEscape)
	if strings.Contains(h.screen(), "Reword message…") {
		t.Fatal("menu did not close")
	}
	h.typ("/")
	h.typ("edit a")
	h.settle()
	if c := h.a.selCommit(); c == nil || c.Subject != "feature: edit a" {
		t.Fatalf("find selected %v", c)
	}
}

func TestLocalChangesStageAndCommit(t *testing.T) {
	h := newHarness(t, 140, 40)
	history(h)
	if err := os.WriteFile(filepath.Join(h.dir, "a.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.start()
	h.must("Local changes, not staged")
	h.typ("S") // stage all (the pseudo row is selected first)
	h.settle()
	h.must("Staged changes, ready to commit")
	h.typ("C")
	h.must("Commit to main")
	h.typ("wip: local edit")
	h.press(microui.KeyReturn)
	h.settle()
	h.must("wip: local edit")
	if strings.Contains(h.screen(), "Staged changes") {
		t.Fatalf("still staged after commit:\n%s", h.screen())
	}
}

func TestSmallTerminalDoesNotPanic(t *testing.T) {
	h := newHarness(t, 30, 8)
	history(h)
	h.start()
	h.typ("?")
	h.press(microui.KeyEscape)
	h.typ("\\")
	h.frame()
}
