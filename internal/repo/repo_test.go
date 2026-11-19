package repo

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// fixture is a scratch repository with helpers to build history.
type fixture struct {
	t    *testing.T
	dir  string
	r    *git.Repository
	rp   *Repo
	when time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	r, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _ := r.Config()
	cfg.User.Name, cfg.User.Email = "Tester", "tester@example.com"
	if err := r.SetConfig(cfg); err != nil {
		t.Fatal(err)
	}
	// make HEAD point at main regardless of the init default
	_ = r.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName("main")))
	rp, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{t: t, dir: dir, r: r, rp: rp, when: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func (f *fixture) write(path, content string) {
	f.t.Helper()
	full := filepath.Join(f.dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) read(path string) string {
	f.t.Helper()
	b, err := os.ReadFile(filepath.Join(f.dir, path))
	if err != nil {
		f.t.Fatal(err)
	}
	return string(b)
}

// commit writes files, stages everything and commits.
func (f *fixture) commit(msg string, files ...string) Hash {
	f.t.Helper()
	for i := 0; i+1 < len(files); i += 2 {
		f.write(files[i], files[i+1])
	}
	wt, _ := f.r.Worktree()
	if err := wt.AddWithOptions(&git.AddOptions{All: true}); err != nil {
		f.t.Fatal(err)
	}
	f.when = f.when.Add(time.Minute)
	sig := &object.Signature{Name: "Tester", Email: "tester@example.com", When: f.when}
	h, err := wt.Commit(msg, &git.CommitOptions{Author: sig, Committer: sig, AllowEmptyCommits: true})
	if err != nil {
		f.t.Fatal(err)
	}
	return h
}

func (f *fixture) graph() *Graph {
	f.t.Helper()
	g, err := f.rp.Load(ScopeAll)
	if err != nil {
		f.t.Fatal(err)
	}
	return g
}

func (f *fixture) branch(name string) Hash {
	f.t.Helper()
	r, err := f.r.Reference(plumbing.NewBranchReferenceName(name), true)
	if err != nil {
		f.t.Fatal(err)
	}
	return r.Hash()
}

func (f *fixture) message(h Hash) string {
	c, err := f.r.CommitObject(h)
	if err != nil {
		f.t.Fatal(err)
	}
	return c.Message
}

func (f *fixture) subjects(tip Hash) []string {
	var out []string
	for h := tip; !h.IsZero(); {
		c, err := f.r.CommitObject(h)
		if err != nil {
			f.t.Fatal(err)
		}
		out = append(out, firstLine(c.Message))
		if c.NumParents() == 0 {
			break
		}
		h = c.ParentHashes[0]
	}
	return out
}

// fsck validates the object store with real git when it is installed.
func (f *fixture) fsck() {
	f.t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		return
	}
	out, err := exec.Command("git", "-C", f.dir, "fsck", "--strict").CombinedOutput()
	if err != nil {
		f.t.Fatalf("git fsck: %v\n%s", err, out)
	}
	out, err = exec.Command("git", "-C", f.dir, "status", "--porcelain").CombinedOutput()
	if err != nil {
		f.t.Fatalf("git status: %v\n%s", err, out)
	}
}

func (f *fixture) clean() {
	f.t.Helper()
	st, err := f.rp.Status()
	if err != nil {
		f.t.Fatal(err)
	}
	if Dirty(st) {
		f.t.Fatalf("worktree dirty: %+v", st)
	}
}

func TestLoadOrderAndRefs(t *testing.T) {
	f := newFixture(t)
	c1 := f.commit("one", "a.txt", "1\n")
	c2 := f.commit("two", "a.txt", "2\n")
	if _, err := f.rp.CreateBranch("feature", c1, false); err != nil {
		t.Fatal(err)
	}
	g := f.graph()
	if len(g.Commits) != 2 || g.Commits[0].Hash != c2 || g.Commits[1].Hash != c1 {
		t.Fatalf("order: %v", g.Commits)
	}
	if g.HeadRef != "main" || g.Head != c2 {
		t.Fatalf("head %s %s", g.HeadRef, g.Head)
	}
	if refs := g.RefsAt[c1]; len(refs) != 1 || refs[0].Short != "feature" {
		t.Fatalf("refs at c1: %+v", refs)
	}
	if !g.Contains(c2, c1) || g.Contains(c1, c2) {
		t.Fatal("Contains")
	}
}

func TestRewordRewritesDescendantsKeepingTrees(t *testing.T) {
	f := newFixture(t)
	f.commit("one", "a.txt", "1\n")
	c2 := f.commit("two", "b.txt", "2\n")
	f.commit("three", "c.txt", "3\n")
	f.write("dirty.txt", "untracked\n") // untracked files don't block a reword
	f.write("a.txt", "local edit\n")    // neither do edits: trees are unchanged

	op, err := f.rp.Reword(f.graph(), c2, "two, reworded\n\nwith a body")
	if err != nil {
		t.Fatal(err)
	}
	tip := f.branch("main")
	if got := f.subjects(tip); strings.Join(got, "|") != "three|two, reworded|one" {
		t.Fatalf("subjects %v", got)
	}
	if !strings.Contains(f.message(f.graph().Commits[1].Hash), "with a body") {
		t.Fatal("body lost")
	}
	if f.read("a.txt") != "local edit\n" {
		t.Fatal("reword touched the worktree")
	}
	f.fsck()

	if err := f.rp.Apply(op.Inverse()); err != nil {
		t.Fatal(err)
	}
	if got := f.subjects(f.branch("main")); got[1] != "two" {
		t.Fatalf("undo: %v", got)
	}
}

func TestCherryPickOntoHeadAndOtherBranch(t *testing.T) {
	f := newFixture(t)
	base := f.commit("base", "a.txt", "1\n2\n3\n4\n5\n6\n7\n8\n")
	feat := f.commit("feature edit", "a.txt", "1\n2\n3\n4\n5\n6\n7\nEIGHT\n")
	// main diverges: reset main to base and edit the top of the file
	if _, err := f.rp.Reset(base, SyncHard); err != nil {
		t.Fatal(err)
	}
	if _, err := f.rp.CreateBranch("feature", feat, false); err != nil {
		t.Fatal(err)
	}
	f.commit("main edit", "a.txt", "ONE\n2\n3\n4\n5\n6\n7\n8\n")

	if _, err := f.rp.CherryPick([]Hash{feat}, ""); err != nil {
		t.Fatal(err)
	}
	if got := f.read("a.txt"); got != "ONE\n2\n3\n4\n5\n6\n7\nEIGHT\n" {
		t.Fatalf("worktree after pick: %q", got)
	}
	f.clean()
	if got := f.subjects(f.branch("main")); got[0] != "feature edit" {
		t.Fatalf("main: %v", got)
	}
	// picking the same change again is reported as already applied
	if _, err := f.rp.CherryPick([]Hash{feat}, ""); err == nil {
		t.Fatal("expected nothing-to-pick error")
	}

	// pick onto a branch that is not checked out: no worktree involvement
	side, _ := f.rp.CreateBranch("side", base, false)
	_ = side
	mainEdit := f.graph().Commits[1].Hash
	if _, err := f.rp.CherryPick([]Hash{mainEdit}, "side"); err != nil {
		t.Fatal(err)
	}
	fc, err := f.rp.FileAt(f.branch("side"), "a.txt")
	if err != nil || string(fc.Data) != "ONE\n2\n3\n4\n5\n6\n7\n8\n" {
		t.Fatalf("side content %q %v", fc.Data, err)
	}
	f.fsck()
}

func TestCherryPickConflict(t *testing.T) {
	f := newFixture(t)
	base := f.commit("base", "a.txt", "x\n")
	feat := f.commit("theirs", "a.txt", "theirs\n")
	if _, err := f.rp.Reset(base, SyncHard); err != nil {
		t.Fatal(err)
	}
	_, _ = f.rp.CreateBranch("feature", feat, false)
	f.commit("ours", "a.txt", "ours\n")
	before := f.branch("main")
	_, err := f.rp.CherryPick([]Hash{feat}, "")
	ce, ok := err.(*ConflictError)
	if !ok || len(ce.Paths) != 1 || ce.Paths[0] != "a.txt" {
		t.Fatalf("want conflict on a.txt, got %v", err)
	}
	if f.branch("main") != before || f.read("a.txt") != "ours\n" {
		t.Fatal("a failed pick must not change anything")
	}
}

func TestDirtyWorktreeBlocksOverlappingPick(t *testing.T) {
	f := newFixture(t)
	base := f.commit("base", "a.txt", "1\n", "b.txt", "1\n")
	feat := f.commit("edit a", "a.txt", "2\n")
	_, _ = f.rp.Reset(base, SyncHard)
	_, _ = f.rp.CreateBranch("feature", feat, false)

	f.write("a.txt", "local\n")
	if _, err := f.rp.CherryPick([]Hash{feat}, ""); err == nil || !strings.Contains(err.Error(), "local changes") {
		t.Fatalf("want local-changes refusal, got %v", err)
	}
	// a local change to an unrelated file is preserved
	f.write("a.txt", "1\n")
	f.write("b.txt", "local\n")
	if _, err := f.rp.CherryPick([]Hash{feat}, ""); err != nil {
		t.Fatal(err)
	}
	if f.read("a.txt") != "2\n" || f.read("b.txt") != "local\n" {
		t.Fatal("unexpected worktree")
	}
}

func TestRevertDropMoveSquash(t *testing.T) {
	f := newFixture(t)
	f.commit("one", "a.txt", "1\n")
	c2 := f.commit("two", "b.txt", "2\n")
	c3 := f.commit("three", "c.txt", "3\n")
	f.commit("four", "d.txt", "4\n")

	if _, err := f.rp.Revert(c3); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.dir, "c.txt")); !os.IsNotExist(err) {
		t.Fatal("revert should delete c.txt")
	}
	if got := f.subjects(f.branch("main"))[0]; got != `Revert "three"` {
		t.Fatalf("revert subject %q", got)
	}

	op, err := f.rp.Drop(f.graph(), c2)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(f.subjects(f.branch("main")), "|"); got != `Revert "three"|four|three|one` {
		t.Fatalf("after drop: %s", got)
	}
	if _, err := os.Stat(filepath.Join(f.dir, "b.txt")); !os.IsNotExist(err) {
		t.Fatal("drop should delete b.txt from the worktree")
	}
	f.clean()
	if err := f.rp.Apply(op.Inverse()); err != nil {
		t.Fatal(err)
	}
	if f.read("b.txt") != "2\n" {
		t.Fatal("undo drop should restore b.txt")
	}

	g := f.graph()
	four := g.Commits[1].Hash
	if _, err := f.rp.Move(g, four, false); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(f.subjects(f.branch("main")), "|"); got != `Revert "three"|three|four|two|one` {
		t.Fatalf("after move down: %s", got)
	}
	g = f.graph()
	if _, err := f.rp.Squash(g, g.Commits[1].Hash, false); err != nil { // three into four
		t.Fatal(err)
	}
	got := f.subjects(f.branch("main"))
	if strings.Join(got, "|") != `Revert "three"|four|two|one` {
		t.Fatalf("after squash: %v", got)
	}
	if msg := f.message(f.graph().Commits[1].Hash); !strings.Contains(msg, "three") {
		t.Fatalf("squash message %q", msg)
	}
	f.clean()
	f.fsck()
}

func TestBranchOpsAndCheckout(t *testing.T) {
	f := newFixture(t)
	c1 := f.commit("one", "a.txt", "1\n")
	f.commit("two", "a.txt", "2\n", "new.txt", "n\n")

	if _, err := f.rp.CreateBranch("old", c1, false); err != nil {
		t.Fatal(err)
	}
	op, err := f.rp.CheckoutBranch(Ref{Name: plumbing.NewBranchReferenceName("old"), Short: "old", Kind: RefLocal, Hash: c1})
	if err != nil {
		t.Fatal(err)
	}
	if f.read("a.txt") != "1\n" {
		t.Fatal("checkout did not update a.txt")
	}
	if _, err := os.Stat(filepath.Join(f.dir, "new.txt")); !os.IsNotExist(err) {
		t.Fatal("checkout should remove new.txt")
	}
	f.clean()
	if err := f.rp.Apply(op.Inverse()); err != nil {
		t.Fatal(err)
	}
	if f.graph().HeadRef != "main" || f.read("new.txt") != "n\n" {
		t.Fatal("undo checkout")
	}
	if _, err := f.rp.RenameBranch("old", "renamed"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.rp.DeleteBranch("renamed"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.rp.DeleteBranch("main"); err == nil {
		t.Fatal("deleting the checked-out branch must fail")
	}
	if _, err := f.rp.CreateTag("v1", c1, "release one"); err != nil {
		t.Fatal(err)
	}
	g := f.graph()
	if refs := g.RefsAt[c1]; len(refs) != 1 || refs[0].Kind != RefTag {
		t.Fatalf("tag refs %+v", refs)
	}
	f.fsck()
}

func TestStageUnstageCommit(t *testing.T) {
	f := newFixture(t)
	f.commit("one", "a.txt", "1\n")
	f.write("a.txt", "2\n")
	f.write("b.txt", "new\n")
	st, _ := f.rp.Status()
	if len(st) != 2 {
		t.Fatalf("status %+v", st)
	}
	if err := f.rp.Stage("a.txt", "b.txt"); err != nil {
		t.Fatal(err)
	}
	st, _ = f.rp.Status()
	staged, _ := f.rp.StagedDiff(st, 3)
	if len(staged) != 2 {
		t.Fatalf("staged %+v", staged)
	}
	if err := f.rp.Unstage("b.txt"); err != nil {
		t.Fatal(err)
	}
	st, _ = f.rp.Status()
	for _, c := range st {
		if c.Path == "b.txt" && (c.Staged != 0 || c.Unstaged != '?') {
			t.Fatalf("b.txt should be untracked again: %+v", c)
		}
	}
	op, err := f.rp.Commit("two", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.subjects(f.branch("main")); got[0] != "two" {
		t.Fatalf("%v", got)
	}
	if err := f.rp.Apply(op.Inverse()); err != nil {
		t.Fatal(err)
	}
	if got := f.subjects(f.branch("main")); got[0] != "one" {
		t.Fatalf("undo commit: %v", got)
	}
	f.fsck()
}
