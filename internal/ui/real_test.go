package ui

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"ella.to/gitk/internal/graph"
	"ella.to/gitk/internal/repo"
	"ella.to/microui"
	"ella.to/microui/renderer"
)

// TestRealRepo times loading a real repository and, with SHOW=1, prints a
// rendered frame: GITK_REPO=/path/to/repo go test ./internal/ui -run RealRepo -v
func TestRealRepo(t *testing.T) {
	dir := os.Getenv("GITK_REPO")
	if dir == "" {
		t.Skip()
	}
	rp, err := repo.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Now()
	g, err := rp.Load(repo.ScopeAll)
	if err != nil {
		t.Fatal(err)
	}
	t1 := time.Now()
	rows := graph.Layout(hashes(g), func(i int) []repo.Hash { return g.Commits[i].Parents }, 8)
	t2 := time.Now()
	_, _ = rp.Status()
	t3 := time.Now()
	maxW := 0
	for _, r := range rows {
		maxW = max(maxW, r.Width())
	}
	fmt.Printf("commits=%d refs=%d load=%v layout=%v status=%v maxlanes=%d\n", len(g.Commits), len(g.Refs), t1.Sub(t0), t2.Sub(t1), t3.Sub(t2), maxW)
	if os.Getenv("SHOW") == "" {
		return
	}
	term := renderer.NewHeadless(150, 45)
	ctx := microui.New()
	renderer.Connect(ctx, term)
	a := newApp(rp, term, Options{})
	st := renderer.TerminalStyle()
	ctx.Style = &st
	a.th.applyStyle(ctx.Style)
	c, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.quit = cancel
	go a.detailWorker(c)
	a.reload(repo.Hash{})
	frame := func() { ctx.Begin(); a.frame(ctx); ctx.End(); renderer.Draw(term, ctx, term.BG) }
	for i := 0; i < 300 && (a.loading || a.det == nil || a.detPending); i++ {
		frame()
		time.Sleep(10 * time.Millisecond)
	}
	ctx.InputText("j")
	frame()
	frame()
	for i := 0; i < 100 && a.detPending; i++ {
		frame()
		time.Sleep(10 * time.Millisecond)
	}
	frame()
	fmt.Println(term.Text())
}
