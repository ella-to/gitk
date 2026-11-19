package ui

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"ella.to/gitk/internal/repo"
)

// requireCommit returns the selected commit or reports why there is none.
func (a *App) requireCommit() *repo.Commit {
	c := a.selCommit()
	if c == nil {
		a.fail(errors.New("select a commit first"))
	}
	return c
}

func (a *App) headLabel() string {
	if a.g != nil && a.g.HeadRef != "" {
		return a.g.HeadRef
	}
	return "HEAD"
}

func plural2(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func (a *App) actCherryPick(onto string) {
	picks := a.pickedCommits()
	if len(picks) == 0 {
		a.fail(errors.New("select a commit first"))
		return
	}
	target := onto
	if target == "" {
		target = a.headLabel()
	}
	what := plural2(len(picks), picks[0].String()[:7], "commits")
	body := []string{fmt.Sprintf("Apply %s on top of %s.", what, target)}
	for i, h := range picks {
		if i == 6 {
			body = append(body, fmt.Sprintf("  … and %d more", len(picks)-6))
			break
		}
		body = append(body, "  "+shortLine(a.g, h))
	}
	if onto == "" || onto == a.g.HeadRef {
		body = append(body, "", "Your working tree is updated; files with local changes are never overwritten.")
	} else {
		body = append(body, "", onto+" is not checked out: the working tree is left alone.")
	}
	a.confirm("Cherry-pick onto "+target, body, "Cherry-pick", false, func() {
		rp := a.rp
		a.run("cherry-pick", func() (*repo.Op, error) { return rp.CherryPick(picks, onto) })
	})
}

func (a *App) actRevert() {
	c := a.requireCommit()
	if c == nil {
		return
	}
	a.confirm("Revert commit", []string{
		"Create a new commit on " + a.headLabel() + " that undoes:",
		"  " + c.Short() + "  " + c.Subject,
	}, "Revert", false, func() {
		rp, h := a.rp, c.Hash
		a.run("revert", func() (*repo.Op, error) { return rp.Revert(h) })
	})
}

func (a *App) actReword() {
	c := a.requireCommit()
	if c == nil {
		return
	}
	tips := a.g.BranchesContaining(c.Hash, repo.RefLocal, repo.RefHead)
	if len(tips) == 0 {
		a.fail(errors.New("commit is not on a local branch: nothing to rewrite"))
		return
	}
	var names []string
	for _, t := range tips {
		names = append(names, t.Short)
	}
	note := "Rewrites " + strings.Join(names, ", ") + " (trees are unchanged, so this never conflicts)."
	if c.Signed {
		note += " The signature is dropped."
	}
	g, h, rp := a.g, c.Hash, a.rp
	a.messageDialog("Reword "+c.Short(), note, c.Message, "", "Reword", func(msg string, _ bool) error {
		if strings.TrimSpace(msg) == "" {
			return errors.New("the message is empty")
		}
		a.run("reword", func() (*repo.Op, error) { return rp.Reword(g, h, msg) })
		return nil
	})
}

func (a *App) actSquash(fixup bool) {
	c := a.requireCommit()
	if c == nil {
		return
	}
	if len(c.Parents) != 1 {
		a.fail(errors.New("can only squash a commit with exactly one parent"))
		return
	}
	verb, how := "Squash", "both messages are kept"
	if fixup {
		verb, how = "Fixup", "the parent's message is kept"
	}
	a.confirm(verb+" into parent", []string{
		"Fold " + c.Short() + " into " + c.Parents[0].String()[:7] + "; " + how + ".",
		"  " + c.Subject,
		"  " + shortLine(a.g, c.Parents[0]),
	}, verb, false, func() {
		g, h, rp := a.g, c.Hash, a.rp
		a.run(strings.ToLower(verb), func() (*repo.Op, error) { return rp.Squash(g, h, fixup) })
	})
}

func (a *App) actDrop() {
	c := a.requireCommit()
	if c == nil {
		return
	}
	a.confirm("Drop commit", []string{
		"Remove this commit from its branch and replay the commits above it:",
		"  " + c.Short() + "  " + c.Subject,
		"",
		"Undo with u if the result is not what you wanted.",
	}, "Drop", true, func() {
		g, h, rp := a.g, c.Hash, a.rp
		a.run("drop", func() (*repo.Op, error) { return rp.Drop(g, h) })
	})
}

func (a *App) actMove(up bool) {
	c := a.requireCommit()
	if c == nil {
		return
	}
	g, h, rp := a.g, c.Hash, a.rp
	a.run("move", func() (*repo.Op, error) { return rp.Move(g, h, up) })
}

func (a *App) actNewBranch() {
	c := a.requireCommit()
	if c == nil {
		return
	}
	h, rp := c.Hash, a.rp
	a.input("New branch at "+c.Short(), []string{c.Subject}, "", "Check it out", true, "Create", func(name string, checkout bool) error {
		name = strings.TrimSpace(name)
		if err := repo.ValidRefName(name); err != nil {
			return err
		}
		a.run("branch", func() (*repo.Op, error) {
			op, err := rp.CreateBranch(name, h, checkout)
			if op != nil {
				op.Select = h
			}
			return op, err
		})
		return nil
	})
}

func (a *App) actNewTag() {
	c := a.requireCommit()
	if c == nil {
		return
	}
	h, rp := c.Hash, a.rp
	d := a.input("New tag at "+c.Short(), []string{c.Subject}, "", "", false, "Tag", nil)
	d.extra = "Message (optional, makes an annotated tag)"
	d.onOK = func(d *dialog) error {
		name := strings.TrimSpace(d.value)
		if err := repo.ValidRefName(name); err != nil {
			return err
		}
		msg := d.extraValue
		a.run("tag", func() (*repo.Op, error) {
			op, err := rp.CreateTag(name, h, msg)
			if op != nil {
				op.Select = h
			}
			return op, err
		})
		return nil
	}
}

func (a *App) actCheckout() {
	c := a.requireCommit()
	if c == nil {
		return
	}
	for _, r := range a.g.RefsAt[c.Hash] {
		if r.Kind == repo.RefLocal {
			a.actCheckoutRef(r)
			return
		}
	}
	for _, r := range a.g.RefsAt[c.Hash] {
		if r.Kind == repo.RefRemote {
			a.actCheckoutRef(r)
			return
		}
	}
	a.confirm("Checkout detached", []string{
		"Check out " + c.Short() + " without a branch (detached HEAD).",
		"Create a branch with b to keep commits made there.",
	}, "Checkout", false, func() {
		h, rp := c.Hash, a.rp
		a.run("checkout", func() (*repo.Op, error) { return rp.CheckoutDetached(h) })
	})
}

func (a *App) actCheckoutRef(r repo.Ref) {
	if r.IsHead {
		a.say("%s is already checked out", r.Short)
		return
	}
	rp := a.rp
	a.run("checkout", func() (*repo.Op, error) {
		op, err := rp.CheckoutBranch(r)
		if op != nil {
			op.Select = r.Hash
		}
		return op, err
	})
}

func (a *App) actReset(mode repo.SyncMode) {
	c := a.requireCommit()
	if c == nil {
		return
	}
	desc := map[repo.SyncMode]string{
		repo.SyncSafe:  "keep: move the branch and update unchanged files; refuses if local changes are in the way",
		repo.SyncNone:  "soft: move the branch only; the index and working tree keep their content (changes appear staged)",
		repo.SyncMixed: "mixed: move the branch and reset the index; the working tree keeps its content",
		repo.SyncHard:  "hard: move the branch and DISCARD all local changes to tracked files",
	}[mode]
	a.confirm("Reset "+a.headLabel()+" to "+c.Short(), []string{
		"  " + c.Subject,
		"",
		desc,
	}, "Reset", mode == repo.SyncHard, func() {
		h, rp := c.Hash, a.rp
		a.run("reset", func() (*repo.Op, error) {
			op, err := rp.Reset(h, mode)
			if op != nil {
				op.Select = h
			}
			return op, err
		})
	})
}

func (a *App) actRenameBranch(r repo.Ref) {
	rp := a.rp
	a.input("Rename branch "+r.Short, nil, r.Short, "", false, "Rename", func(name string, _ bool) error {
		name = strings.TrimSpace(name)
		if name == r.Short {
			return nil
		}
		if err := repo.ValidRefName(name); err != nil {
			return err
		}
		a.run("rename", func() (*repo.Op, error) { return rp.RenameBranch(r.Short, name) })
		return nil
	})
}

func (a *App) actDeleteRef(r repo.Ref) {
	rp := a.rp
	kind := "branch"
	if r.Kind == repo.RefTag {
		kind = "tag"
	}
	body := []string{"Delete " + kind + " " + r.Short + " (at " + r.Hash.String()[:7] + ")."}
	if r.Kind == repo.RefLocal {
		merged := false
		for _, o := range a.g.Refs {
			if o.Name != r.Name && (o.Kind == repo.RefLocal || o.Kind == repo.RefRemote) && a.g.Contains(o.Hash, r.Hash) {
				merged = true
				break
			}
		}
		if !merged {
			body = append(body, "Its commits are not on any other branch.")
		}
	}
	body = append(body, "Undo with u.")
	a.confirm("Delete "+kind, body, "Delete", true, func() {
		a.run("delete", func() (*repo.Op, error) {
			if r.Kind == repo.RefTag {
				return rp.DeleteTag(r.Short)
			}
			return rp.DeleteBranch(r.Short)
		})
	})
}

func (a *App) actMark() {
	c := a.requireCommit()
	if c == nil {
		return
	}
	if a.marked == c.Hash {
		a.marked, a.diffMarked = repo.Hash{}, false
		a.say("mark cleared")
		return
	}
	a.marked = c.Hash
	a.say("marked %s · select another commit and press = to diff against it", c.Short())
}

func (a *App) actDiffMarked() {
	if a.marked.IsZero() {
		a.fail(errors.New("mark a commit first with m"))
		return
	}
	a.diffMarked = !a.diffMarked
	if a.diffMarked {
		a.say("showing diff from marked %s · = to go back", a.marked.String()[:7])
	}
}

// ---- local changes ---------------------------------------------------------

func (a *App) selKind() rowKind {
	if a.sel >= 0 && a.sel < len(a.rows) {
		return a.rows[a.sel].kind
	}
	return rowCommit
}

// actToggleStage stages or unstages the file selected in the files pane.
func (a *App) actToggleStage() {
	kind := a.selKind()
	if kind == rowCommit || a.det == nil || a.fileSel < 0 || a.fileSel >= len(a.det.files) {
		return
	}
	f := a.det.files[a.fileSel]
	a.stage(kind == rowUnstaged, []string{f.path})
}

// actStageAll unstages everything when the staged row is selected, and
// stages every unstaged change otherwise.
func (a *App) actStageAll() {
	unstage := a.selKind() == rowStaged
	var paths []string
	for _, c := range a.changes {
		if (!unstage && c.Unstaged != 0) || (unstage && c.Staged != 0) {
			paths = append(paths, c.Path)
		}
	}
	if len(paths) == 0 {
		a.fail(errors.New("nothing to " + map[bool]string{true: "unstage", false: "stage"}[unstage]))
		return
	}
	a.stage(!unstage, paths)
}

func (a *App) stage(add bool, paths []string) {
	if len(paths) == 0 {
		return
	}
	if a.busy != "" {
		return
	}
	a.busy = "staging"
	rp := a.rp
	go func() {
		var err error
		if add {
			err = rp.Stage(paths...)
		} else {
			err = rp.Unstage(paths...)
		}
		a.post(func() {
			a.busy = ""
			if err != nil {
				a.fail(err)
			} else if add {
				a.say("staged %s", plural2(len(paths), paths[0], "files"))
			} else {
				a.say("unstaged %s", plural2(len(paths), paths[0], "files"))
			}
			a.statusBusy = false
			a.refreshStatus()
		})
	}()
}

func (a *App) actDiscard() {
	if a.selKind() != rowUnstaged || a.det == nil || a.fileSel < 0 || a.fileSel >= len(a.det.files) {
		a.fail(errors.New("select a file under local unstaged changes to discard"))
		return
	}
	f := a.det.files[a.fileSel]
	what := "Revert " + f.path + " to its staged/committed content."
	if f.change.Unstaged == '?' {
		what = "Delete the untracked file " + f.path + "."
	}
	a.confirm("Discard changes", []string{what, "This cannot be undone."}, "Discard", true, func() {
		rp, ch := a.rp, f.change
		go func() {
			err := rp.Discard(ch)
			a.post(func() {
				if err != nil {
					a.fail(err)
				} else {
					a.say("discarded changes to %s", ch.Path)
				}
				a.statusBusy = false
				a.refreshStatus()
			})
		}()
	})
}

func (a *App) actCommit(amend bool) {
	staged := false
	for _, c := range a.changes {
		staged = staged || c.Staged != 0
	}
	if !staged && !amend {
		a.fail(errors.New("nothing staged: stage files with space or S first"))
		return
	}
	initial := ""
	title := "Commit to " + a.headLabel()
	if amend {
		title = "Amend " + a.headLabel()
		if c := a.g.Commit(a.g.Head); c != nil {
			initial = c.Message
		}
	}
	rp := a.rp
	a.messageDialog(title, fmt.Sprintf("%d staged file(s).", countStaged(a.changes)), initial, "Amend previous commit", "Commit", func(msg string, am bool) error {
		if strings.TrimSpace(msg) == "" {
			return errors.New("the message is empty")
		}
		a.run("commit", func() (*repo.Op, error) { return rp.Commit(msg, am) })
		return nil
	}).checked = amend
}

func countStaged(changes []repo.Change) int {
	n := 0
	for _, c := range changes {
		if c.Staged != 0 {
			n++
		}
	}
	return n
}

// ---- clipboard -------------------------------------------------------------

func (a *App) copy(s, what string) {
	var cmd *exec.Cmd
	switch {
	case runtime.GOOS == "darwin":
		cmd = exec.Command("pbcopy")
	case os.Getenv("WAYLAND_DISPLAY") != "":
		cmd = exec.Command("wl-copy")
	case os.Getenv("DISPLAY") != "":
		cmd = exec.Command("xclip", "-selection", "clipboard")
	}
	if cmd != nil {
		cmd.Stdin = strings.NewReader(s)
		if cmd.Run() == nil {
			a.say("copied %s", what)
			return
		}
	}
	// OSC 52 works over ssh and in most modern terminals
	fmt.Fprintf(os.Stdout, "\x1b]52;c;%s\x07", base64.StdEncoding.EncodeToString([]byte(s)))
	a.say("copied %s", what)
}
