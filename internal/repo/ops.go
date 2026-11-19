package repo

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/format/index"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// SyncMode says what happens to the index and working tree when an operation
// moves the commit HEAD resolves to.
type SyncMode int

const (
	// SyncSafe updates the paths that differ between the old and new HEAD,
	// refusing when one of them has local changes (like git checkout).
	SyncSafe SyncMode = iota
	// SyncNone moves refs only (git reset --soft).
	SyncNone
	// SyncMixed resets the index but keeps the working tree (git reset).
	SyncMixed
	// SyncHard resets index and working tree, discarding changes.
	SyncHard
)

// RefUpdate moves one ref. A zero Old means the ref is created, a zero New
// means it is deleted.
type RefUpdate struct {
	Name     plumbing.ReferenceName
	Old, New Hash
}

// HeadChange repoints HEAD (to a branch, or detached at a commit).
type HeadChange struct {
	Old, New *plumbing.Reference
}

// Op is an applied operation, kept so it can be undone.
type Op struct {
	Title string
	Refs  []RefUpdate
	Head  *HeadChange
	Sync  SyncMode
	Note  string // extra information for the user (skipped commits, ...)
	// Select is the commit the UI should select afterwards (the rewritten,
	// picked or created commit); zero keeps the current selection.
	Select Hash
}

// Inverse returns the operation that undoes op.
func (op *Op) Inverse() *Op {
	inv := &Op{Title: op.Title, Sync: op.Sync}
	if op.Sync == SyncHard {
		inv.Sync = SyncSafe
	}
	for i := len(op.Refs) - 1; i >= 0; i-- {
		u := op.Refs[i]
		inv.Refs = append(inv.Refs, RefUpdate{Name: u.Name, Old: u.New, New: u.Old})
	}
	if op.Head != nil {
		inv.Head = &HeadChange{Old: op.Head.New, New: op.Head.Old}
	}
	return inv
}

// Signature is the configured user, stamped now.
func (rp *Repo) Signature() (object.Signature, error) {
	cfg, err := rp.R.ConfigScoped(config.SystemScope)
	if err != nil {
		return object.Signature{}, err
	}
	name, email := cfg.User.Name, cfg.User.Email
	if cfg.Committer.Name != "" {
		name, email = cfg.Committer.Name, cfg.Committer.Email
	}
	if v := os.Getenv("GIT_COMMITTER_NAME"); v != "" {
		name = v
	}
	if v := os.Getenv("GIT_COMMITTER_EMAIL"); v != "" {
		email = v
	}
	if name == "" || email == "" {
		return object.Signature{}, errors.New("set git config user.name and user.email first")
	}
	return object.Signature{Name: name, Email: email, When: time.Now()}, nil
}

func (rp *Repo) writeCommit(c *object.Commit) (Hash, error) {
	obj := rp.R.Storer.NewEncodedObject()
	if err := c.Encode(obj); err != nil {
		return Hash{}, err
	}
	return rp.R.Storer.SetEncodedObject(obj)
}

// headState reads HEAD and the commit it resolves to.
func (rp *Repo) headState() (*plumbing.Reference, Hash, error) {
	head, err := rp.R.Storer.Reference(plumbing.HEAD)
	if err != nil {
		return nil, Hash{}, err
	}
	if head.Type() == plumbing.HashReference {
		return head, head.Hash(), nil
	}
	ref, err := rp.R.Storer.Reference(head.Target())
	if errors.Is(err, plumbing.ErrReferenceNotFound) {
		return head, Hash{}, nil // unborn branch
	}
	if err != nil {
		return nil, Hash{}, err
	}
	return head, ref.Hash(), nil
}

// Apply performs op: it syncs the working tree first (so a refusal leaves
// everything untouched), then moves refs and HEAD.
func (rp *Repo) Apply(op *Op) error {
	head, before, err := rp.headState()
	if err != nil {
		return err
	}
	after := before
	newHead := head
	if op.Head != nil {
		newHead = op.Head.New
	}
	if newHead.Type() == plumbing.HashReference {
		after = newHead.Hash()
	} else {
		target := newHead.Target()
		found := false
		for _, u := range op.Refs {
			if u.Name == target {
				after, found = u.New, true
			}
		}
		if !found {
			if r, err := rp.R.Storer.Reference(target); err == nil {
				after = r.Hash()
			}
		}
	}

	if after != before && op.Sync == SyncSafe && rp.Root != "" {
		if err := rp.syncWorktree(before, after); err != nil {
			return err
		}
	}
	for _, u := range op.Refs {
		if u.Name == plumbing.HEAD {
			continue // detached HEAD moves are expressed through Head
		}
		if u.New.IsZero() {
			if err := rp.R.Storer.RemoveReference(u.Name); err != nil {
				return err
			}
			continue
		}
		ref := plumbing.NewHashReference(u.Name, u.New)
		var old *plumbing.Reference
		if !u.Old.IsZero() {
			old = plumbing.NewHashReference(u.Name, u.Old)
		}
		if err := rp.R.Storer.CheckAndSetReference(ref, old); err != nil {
			return fmt.Errorf("update %s: %w", u.Name.Short(), err)
		}
	}
	if op.Head != nil {
		if err := rp.R.Storer.SetReference(op.Head.New); err != nil {
			return err
		}
	}
	if after != before && (op.Sync == SyncMixed || op.Sync == SyncHard) && rp.Root != "" {
		wt, err := rp.R.Worktree()
		if err != nil {
			return err
		}
		mode := git.MixedReset
		if op.Sync == SyncHard {
			mode = git.HardReset
		}
		return wt.Reset(&git.ResetOptions{Commit: after, Mode: mode})
	}
	return nil
}

// syncWorktree rewrites the index entries and working files that differ
// between commits from and to, refusing if any of them has local changes.
func (rp *Repo) syncWorktree(from, to Hash) error {
	var ft, tt *object.Tree
	var err error
	if !from.IsZero() {
		if ft, err = rp.commitTree(from); err != nil {
			return err
		}
	}
	if !to.IsZero() {
		if tt, err = rp.commitTree(to); err != nil {
			return err
		}
	}
	if ft == nil {
		ft = &object.Tree{}
	}
	if tt == nil {
		tt = &object.Tree{}
	}
	changes, err := object.DiffTree(ft, tt)
	if err != nil {
		return err
	}
	if len(changes) == 0 {
		return nil
	}
	status, err := rp.Status()
	if err != nil {
		return err
	}
	local := map[string]bool{}
	for _, c := range status {
		local[c.Path] = true
	}
	var blocked []string
	for _, ch := range changes {
		for _, p := range []string{ch.From.Name, ch.To.Name} {
			if p != "" && local[p] {
				blocked = append(blocked, p)
			}
		}
	}
	if len(blocked) > 0 {
		slices.Sort(blocked)
		blocked = slices.Compact(blocked)
		more := ""
		if len(blocked) > 3 {
			more = fmt.Sprintf(" (+%d more)", len(blocked)-3)
			blocked = blocked[:3]
		}
		return fmt.Errorf("local changes to %s%s would be overwritten: commit or discard them first", strings.Join(blocked, ", "), more)
	}

	idx, err := rp.R.Storer.Index()
	if err != nil {
		return err
	}
	for _, ch := range changes {
		if ch.From.Name != "" && ch.From.Name != ch.To.Name {
			full := filepath.Join(rp.Root, filepath.FromSlash(ch.From.Name))
			if err := os.Remove(full); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			removeEmptyDirs(rp.Root, filepath.Dir(full))
			_, _ = idx.Remove(ch.From.Name)
		}
		if ch.To.Name == "" {
			continue
		}
		e := ch.To.TreeEntry
		full := filepath.Join(rp.Root, filepath.FromSlash(ch.To.Name))
		if e.Mode != filemode.Submodule {
			fc, err := rp.blob(e.Hash, e.Mode)
			if err != nil {
				return err
			}
			if err := writeWorktreeFile(full, fc); err != nil {
				return err
			}
		}
		ie, err := idx.Entry(ch.To.Name)
		if err != nil {
			ie = idx.Add(ch.To.Name)
		}
		ie.Hash, ie.Mode = e.Hash, e.Mode
		ie.CreatedAt, ie.ModifiedAt, ie.Size = time.Time{}, time.Time{}, 0
		if fi, err := os.Lstat(full); err == nil {
			ie.ModifiedAt, ie.Size = fi.ModTime(), uint32(fi.Size())
		}
	}
	slices.SortFunc(idx.Entries, func(a, b *index.Entry) int { return strings.Compare(a.Name, b.Name) })
	return rp.R.Storer.SetIndex(idx)
}

func removeEmptyDirs(root, dir string) {
	for dir != root && strings.HasPrefix(dir, root) {
		if os.Remove(dir) != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}

// ---- tree-preserving rewrites (reword, squash) -----------------------------

// localTips lists the refs a history rewrite of h would move: local branches
// containing h, plus HEAD when it is detached on top of h.
func localTips(g *Graph, h Hash) []Ref {
	return g.BranchesContaining(h, RefLocal, RefHead)
}

// rewrite recreates target (edited by edit) and every commit between it and
// the local branches that contain it, keeping their trees. Since no tree
// changes, this never conflicts and never touches the working tree.
func (rp *Repo) rewrite(g *Graph, title string, target Hash, edit func(c *object.Commit) error) (*Op, error) {
	tips := localTips(g, target)
	if len(tips) == 0 {
		return nil, errors.New("commit is not on a local branch")
	}
	ti, ok := g.Index[target]
	if !ok {
		return nil, errors.New("commit not loaded")
	}
	me, err := rp.Signature()
	if err != nil {
		return nil, err
	}

	// commits newer than target that descend from it, oldest first
	reach := map[Hash]bool{target: true}
	order := []Hash{target}
	for i := ti - 1; i >= 0; i-- {
		c := g.Commits[i]
		if slices.ContainsFunc(c.Parents, func(p Hash) bool { return reach[p] }) {
			reach[c.Hash] = true
			order = append(order, c.Hash)
		}
	}
	mapped := map[Hash]Hash{}
	for _, h := range order {
		c, err := rp.R.CommitObject(h)
		if err != nil {
			return nil, err
		}
		nc := *c
		nc.ParentHashes = slices.Clone(c.ParentHashes)
		for i, p := range nc.ParentHashes {
			if m, ok := mapped[p]; ok {
				nc.ParentHashes[i] = m
			}
		}
		if h == target {
			if err := edit(&nc); err != nil {
				return nil, err
			}
		}
		nc.Committer = me
		nc.PGPSignature = ""
		nh, err := rp.writeCommit(&nc)
		if err != nil {
			return nil, err
		}
		mapped[h] = nh
	}

	op := &Op{Title: title, Sync: SyncSafe, Select: mapped[target]}
	for _, r := range tips {
		n, ok := mapped[r.Hash]
		if !ok {
			continue
		}
		if r.Kind == RefHead {
			op.Head = &HeadChange{
				Old: plumbing.NewHashReference(plumbing.HEAD, r.Hash),
				New: plumbing.NewHashReference(plumbing.HEAD, n),
			}
			continue
		}
		op.Refs = append(op.Refs, RefUpdate{Name: r.Name, Old: r.Hash, New: n})
	}
	return op, rp.Apply(op)
}

// Reword replaces the message of h, rewriting the local branches that
// contain it.
func (rp *Repo) Reword(g *Graph, h Hash, msg string) (*Op, error) {
	msg = cleanMessage(msg)
	if msg == "" {
		return nil, errors.New("empty commit message")
	}
	return rp.rewrite(g, "reword "+h.String()[:7], h, func(c *object.Commit) error {
		c.Message = msg
		return nil
	})
}

// Squash folds h into its parent. With keepMessage (fixup) the parent's
// message is kept, otherwise both messages are joined.
func (rp *Repo) Squash(g *Graph, h Hash, fixup bool) (*Op, error) {
	c := g.Commit(h)
	if c == nil || len(c.Parents) != 1 {
		return nil, errors.New("can only squash a commit with exactly one parent")
	}
	parent, err := rp.R.CommitObject(c.Parents[0])
	if err != nil {
		return nil, err
	}
	verb := "squash"
	if fixup {
		verb = "fixup"
	}
	return rp.rewrite(g, verb+" "+h.String()[:7], h, func(nc *object.Commit) error {
		msg := parent.Message
		if !fixup {
			msg = strings.TrimRight(parent.Message, "\n") + "\n\n" + nc.Message
		}
		nc.Message = cleanMessage(msg)
		nc.Author = parent.Author
		nc.ParentHashes = slices.Clone(parent.ParentHashes)
		return nil
	})
}

// cleanMessage trims surrounding blank space and ends the message with one
// newline, like git commit.
func cleanMessage(msg string) string {
	lines := strings.Split(strings.ReplaceAll(msg, "\r\n", "\n"), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	msg = strings.Trim(strings.Join(lines, "\n"), "\n")
	if msg == "" {
		return ""
	}
	return msg + "\n"
}

// ---- replays (cherry-pick, revert, drop, reorder) --------------------------

// pick applies the change parent→c on top of onto and commits it, keeping
// c's author and message. It returns onto unchanged (and applied=false) when
// the change is already present.
func (rp *Repo) pick(onto Hash, c *object.Commit, me object.Signature, op string) (Hash, bool, error) {
	var baseTree Hash
	if c.NumParents() > 0 {
		p, err := c.Parent(0)
		if err != nil {
			return Hash{}, false, err
		}
		baseTree = p.TreeHash
	} else {
		baseTree = emptyTree
	}
	return rp.applyChange(onto, baseTree, c.TreeHash, c.Hash.String()[:7]+" "+firstLine(c.Message), op, func(tree Hash) *object.Commit {
		return &object.Commit{
			Author:       c.Author,
			Committer:    me,
			Message:      c.Message,
			TreeHash:     tree,
			ParentHashes: []Hash{onto},
			Encoding:     c.Encoding,
		}
	})
}

func (rp *Repo) applyChange(onto, base, theirs Hash, label, op string, mk func(tree Hash) *object.Commit) (Hash, bool, error) {
	var ours Hash
	if onto.IsZero() {
		ours = emptyTree
	} else {
		oc, err := rp.R.CommitObject(onto)
		if err != nil {
			return Hash{}, false, err
		}
		ours = oc.TreeHash
	}
	if base.IsZero() {
		base = emptyTree
	}
	tree, err := rp.MergeTrees(base, ours, theirs, "HEAD", label, op)
	if err != nil {
		return Hash{}, false, err
	}
	if tree == ours {
		return onto, false, nil
	}
	nh, err := rp.writeCommit(mk(tree))
	return nh, true, err
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return l
}

// targetRef resolves a branch name ("" or "HEAD" for the current HEAD) to
// the ref to move and its tip.
func (rp *Repo) targetRef(branch string) (plumbing.ReferenceName, Hash, error) {
	head, h, err := rp.headState()
	if err != nil {
		return "", Hash{}, err
	}
	if branch == "" || branch == "HEAD" {
		if head.Type() == plumbing.SymbolicReference {
			return head.Target(), h, nil
		}
		return plumbing.HEAD, h, nil
	}
	name := plumbing.NewBranchReferenceName(branch)
	ref, err := rp.R.Storer.Reference(name)
	if err != nil {
		return "", Hash{}, fmt.Errorf("branch %s: %w", branch, err)
	}
	return name, ref.Hash(), nil
}

func (rp *Repo) moveTo(title string, name plumbing.ReferenceName, old, tip Hash) (*Op, error) {
	op := &Op{Title: title, Sync: SyncSafe}
	if name == plumbing.HEAD {
		op.Head = &HeadChange{Old: plumbing.NewHashReference(plumbing.HEAD, old), New: plumbing.NewHashReference(plumbing.HEAD, tip)}
	} else {
		op.Refs = []RefUpdate{{Name: name, Old: old, New: tip}}
	}
	return op, rp.Apply(op)
}

// CherryPick applies commits (in the given order) on top of branch ("" for
// HEAD). Merge commits are picked relative to their first parent. Commits
// whose change is already present are skipped and listed in Op.Note.
func (rp *Repo) CherryPick(hashes []Hash, branch string) (*Op, error) {
	name, tip, err := rp.targetRef(branch)
	if err != nil {
		return nil, err
	}
	me, err := rp.Signature()
	if err != nil {
		return nil, err
	}
	cur := tip
	var skipped []string
	for _, h := range hashes {
		c, err := rp.R.CommitObject(h)
		if err != nil {
			return nil, err
		}
		next, applied, err := rp.pick(cur, c, me, "cherry-pick "+h.String()[:7])
		if err != nil {
			return nil, err
		}
		if !applied {
			skipped = append(skipped, h.String()[:7])
		}
		cur = next
	}
	title := fmt.Sprintf("cherry-pick %d commit(s) onto %s", len(hashes), name.Short())
	if len(hashes) == 1 {
		title = fmt.Sprintf("cherry-pick %s onto %s", hashes[0].String()[:7], name.Short())
	}
	if cur == tip {
		return nil, fmt.Errorf("nothing to cherry-pick: changes already on %s", name.Short())
	}
	op, err := rp.moveTo(title, name, tip, cur)
	if op != nil {
		op.Select = cur
	}
	if op != nil && len(skipped) > 0 {
		op.Note = "skipped (already applied): " + strings.Join(skipped, ", ")
	}
	return op, err
}

// Revert commits the inverse of h on top of HEAD.
func (rp *Repo) Revert(h Hash) (*Op, error) {
	name, tip, err := rp.targetRef("")
	if err != nil {
		return nil, err
	}
	me, err := rp.Signature()
	if err != nil {
		return nil, err
	}
	c, err := rp.R.CommitObject(h)
	if err != nil {
		return nil, err
	}
	parentTree := emptyTree
	if c.NumParents() > 0 {
		p, err := c.Parent(0)
		if err != nil {
			return nil, err
		}
		parentTree = p.TreeHash
	}
	msg := fmt.Sprintf("Revert \"%s\"\n\nThis reverts commit %s.\n", firstLine(c.Message), h)
	nh, applied, err := rp.applyChange(tip, c.TreeHash, parentTree, "revert "+h.String()[:7], "revert "+h.String()[:7], func(tree Hash) *object.Commit {
		return &object.Commit{Author: me, Committer: me, Message: msg, TreeHash: tree, ParentHashes: []Hash{tip}}
	})
	if err != nil {
		return nil, err
	}
	if !applied {
		return nil, errors.New("nothing to revert: the change is not present on HEAD")
	}
	op, err := rp.moveTo("revert "+h.String()[:7], name, tip, nh)
	if op != nil {
		op.Select = nh
	}
	return op, err
}

// firstParentChain returns the commits from tip back to (excluding) stop
// along first parents, oldest first. ok is false if stop is not on the chain.
func firstParentChain(g *Graph, tip, stop Hash) (chain []Hash, ok bool) {
	for h := tip; ; {
		if h == stop {
			slices.Reverse(chain)
			return chain, true
		}
		c := g.Commit(h)
		if c == nil || len(c.Parents) == 0 {
			return nil, false
		}
		chain = append(chain, h)
		h = c.Parents[0]
	}
}

// branchFor picks the local branch to edit for h: HEAD's branch when it
// contains h, else the only local branch that does.
func (rp *Repo) branchFor(g *Graph, h Hash) (Ref, error) {
	var cands []Ref
	for _, r := range localTips(g, h) {
		if r.IsHead {
			return r, nil
		}
		cands = append(cands, r)
	}
	switch len(cands) {
	case 0:
		return Ref{}, errors.New("commit is not on a local branch")
	case 1:
		return cands[0], nil
	}
	names := make([]string, len(cands))
	for i, r := range cands {
		names[i] = r.Short
	}
	return Ref{}, fmt.Errorf("commit is on several branches (%s): check one out first", strings.Join(names, ", "))
}

// replayChain rebuilds the linear chain of commits on top of onto. It
// returns the new tip and the new hash of every replayed commit.
func (rp *Repo) replayChain(onto Hash, chain []Hash, op string) (Hash, map[Hash]Hash, error) {
	me, err := rp.Signature()
	if err != nil {
		return Hash{}, nil, err
	}
	mapped := map[Hash]Hash{}
	cur := onto
	for _, h := range chain {
		c, err := rp.R.CommitObject(h)
		if err != nil {
			return Hash{}, nil, err
		}
		if c.NumParents() > 1 {
			return Hash{}, nil, fmt.Errorf("%s: cannot replay merge commit %s", op, h.String()[:7])
		}
		next, applied, err := rp.pick(cur, c, me, op)
		if err != nil {
			return Hash{}, nil, err
		}
		if applied { // a commit whose change is already present is dropped, like git rebase
			mapped[h] = next
		}
		cur = next
	}
	return cur, mapped, nil
}

// edit rebuilds the first-parent history of the branch containing h from
// h's parent upward, with the commits produced by reorder. The result
// selects follow's new hash (or h's parent when follow was dropped).
func (rp *Repo) edit(g *Graph, h, follow Hash, title string, reorder func(parent Hash, chain []Hash) (onto Hash, replay []Hash, err error)) (*Op, error) {
	br, err := rp.branchFor(g, h)
	if err != nil {
		return nil, err
	}
	c := g.Commit(h)
	if c == nil || len(c.Parents) != 1 {
		return nil, errors.New("only commits with exactly one parent can be moved or dropped")
	}
	chain, ok := firstParentChain(g, br.Hash, c.Parents[0])
	if !ok || !slices.Contains(chain, h) {
		return nil, fmt.Errorf("commit is not on the first-parent line of %s", br.Short)
	}
	onto, replay, err := reorder(c.Parents[0], chain)
	if err != nil {
		return nil, err
	}
	tip, mapped, err := rp.replayChain(onto, replay, title)
	if err != nil {
		return nil, err
	}
	op, err := rp.moveTo(title+" on "+br.Short, br.Name, br.Hash, tip)
	if op != nil {
		op.Select = c.Parents[0]
		if n, ok := mapped[follow]; ok {
			op.Select = n
		}
	}
	return op, err
}

// Drop removes h from its branch, replaying the commits above it.
func (rp *Repo) Drop(g *Graph, h Hash) (*Op, error) {
	return rp.edit(g, h, h, "drop "+h.String()[:7], func(parent Hash, chain []Hash) (Hash, []Hash, error) {
		return parent, slices.DeleteFunc(chain, func(x Hash) bool { return x == h }), nil
	})
}

// Move swaps h with its child (up, newer) or its parent (down, older) on
// its branch.
func (rp *Repo) Move(g *Graph, h Hash, up bool) (*Op, error) {
	if up {
		return rp.edit(g, h, h, "move "+h.String()[:7]+" up", func(parent Hash, chain []Hash) (Hash, []Hash, error) {
			if len(chain) < 2 {
				return Hash{}, nil, errors.New("already the newest commit on the branch")
			}
			chain[0], chain[1] = chain[1], chain[0]
			return parent, chain, nil
		})
	}
	c := g.Commit(h)
	if c == nil || len(c.Parents) != 1 {
		return nil, errors.New("only commits with exactly one parent can be moved")
	}
	p := g.Commit(c.Parents[0])
	if p == nil || len(p.Parents) != 1 {
		return nil, errors.New("cannot move below a root or merge commit")
	}
	return rp.edit(g, p.Hash, h, "move "+h.String()[:7]+" down", func(parent Hash, chain []Hash) (Hash, []Hash, error) {
		if len(chain) < 2 || chain[1] != h {
			return Hash{}, nil, errors.New("commit is not on the first-parent line of its branch")
		}
		chain[0], chain[1] = chain[1], chain[0]
		return parent, chain, nil
	})
}

// ---- refs ------------------------------------------------------------------

// ValidRefName reports whether name is usable as a branch or tag name.
func ValidRefName(name string) error {
	switch {
	case name == "", name == "HEAD", strings.HasPrefix(name, "-"), strings.HasPrefix(name, "/"),
		strings.HasSuffix(name, "/"), strings.HasSuffix(name, "."), strings.HasSuffix(name, ".lock"),
		strings.Contains(name, ".."), strings.Contains(name, "//"), strings.Contains(name, "@{"),
		strings.ContainsAny(name, " ~^:?*[\\\t\n"):
		return fmt.Errorf("invalid name %q", name)
	}
	return nil
}

func (rp *Repo) refExists(name plumbing.ReferenceName) bool {
	_, err := rp.R.Storer.Reference(name)
	return err == nil
}

// CreateBranch creates a branch at h, optionally checking it out.
func (rp *Repo) CreateBranch(name string, h Hash, checkout bool) (*Op, error) {
	if err := ValidRefName(name); err != nil {
		return nil, err
	}
	ref := plumbing.NewBranchReferenceName(name)
	if rp.refExists(ref) {
		return nil, fmt.Errorf("branch %s already exists", name)
	}
	op := &Op{Title: "create branch " + name, Refs: []RefUpdate{{Name: ref, New: h}}}
	if checkout {
		head, _, err := rp.headState()
		if err != nil {
			return nil, err
		}
		op.Title += " and check it out"
		op.Head = &HeadChange{Old: head, New: plumbing.NewSymbolicReference(plumbing.HEAD, ref)}
	}
	return op, rp.Apply(op)
}

// DeleteBranch deletes a local branch (not the checked-out one).
func (rp *Repo) DeleteBranch(name string) (*Op, error) {
	ref := plumbing.NewBranchReferenceName(name)
	r, err := rp.R.Storer.Reference(ref)
	if err != nil {
		return nil, err
	}
	head, _, _ := rp.headState()
	if head != nil && head.Type() == plumbing.SymbolicReference && head.Target() == ref {
		return nil, errors.New("cannot delete the checked-out branch")
	}
	op := &Op{Title: "delete branch " + name, Refs: []RefUpdate{{Name: ref, Old: r.Hash()}}}
	return op, rp.Apply(op)
}

// RenameBranch renames a local branch, carrying HEAD and its upstream config.
func (rp *Repo) RenameBranch(oldName, newName string) (*Op, error) {
	if err := ValidRefName(newName); err != nil {
		return nil, err
	}
	from, to := plumbing.NewBranchReferenceName(oldName), plumbing.NewBranchReferenceName(newName)
	r, err := rp.R.Storer.Reference(from)
	if err != nil {
		return nil, err
	}
	if rp.refExists(to) {
		return nil, fmt.Errorf("branch %s already exists", newName)
	}
	op := &Op{Title: "rename branch " + oldName + " → " + newName, Refs: []RefUpdate{
		{Name: to, New: r.Hash()},
		{Name: from, Old: r.Hash()},
	}}
	head, _, _ := rp.headState()
	if head != nil && head.Type() == plumbing.SymbolicReference && head.Target() == from {
		op.Head = &HeadChange{Old: head, New: plumbing.NewSymbolicReference(plumbing.HEAD, to)}
	}
	if err := rp.Apply(op); err != nil {
		return nil, err
	}
	if cfg, err := rp.R.Config(); err == nil {
		if b, ok := cfg.Branches[oldName]; ok {
			delete(cfg.Branches, oldName)
			b.Name = newName
			cfg.Branches[newName] = b
			_ = rp.R.SetConfig(cfg)
		}
	}
	return op, nil
}

// CreateTag tags h; a non-empty message makes an annotated tag.
func (rp *Repo) CreateTag(name string, h Hash, message string) (*Op, error) {
	if err := ValidRefName(name); err != nil {
		return nil, err
	}
	ref := plumbing.NewTagReferenceName(name)
	if rp.refExists(ref) {
		return nil, fmt.Errorf("tag %s already exists", name)
	}
	if message == "" {
		op := &Op{Title: "tag " + name, Refs: []RefUpdate{{Name: ref, New: h}}}
		return op, rp.Apply(op)
	}
	me, err := rp.Signature()
	if err != nil {
		return nil, err
	}
	r, err := rp.R.CreateTag(name, h, &git.CreateTagOptions{Tagger: &me, Message: cleanMessage(message)})
	if err != nil {
		return nil, err
	}
	return &Op{Title: "tag " + name, Refs: []RefUpdate{{Name: ref, New: r.Hash()}}}, nil
}

// DeleteTag deletes a tag.
func (rp *Repo) DeleteTag(name string) (*Op, error) {
	ref := plumbing.NewTagReferenceName(name)
	r, err := rp.R.Storer.Reference(ref)
	if err != nil {
		return nil, err
	}
	op := &Op{Title: "delete tag " + name, Refs: []RefUpdate{{Name: ref, Old: r.Hash()}}}
	return op, rp.Apply(op)
}

// CheckoutBranch switches HEAD to a local branch. A remote branch is checked
// out as a new local tracking branch (or the existing local one of the same
// name).
func (rp *Repo) CheckoutBranch(r Ref) (*Op, error) {
	head, _, err := rp.headState()
	if err != nil {
		return nil, err
	}
	switch r.Kind {
	case RefLocal:
		op := &Op{Title: "checkout " + r.Short, Head: &HeadChange{Old: head, New: plumbing.NewSymbolicReference(plumbing.HEAD, r.Name)}}
		return op, rp.Apply(op)
	case RefRemote:
		remote, branch, _ := strings.Cut(r.Short, "/")
		local := plumbing.NewBranchReferenceName(branch)
		if lr, err := rp.R.Storer.Reference(local); err == nil {
			return rp.CheckoutBranch(Ref{Name: local, Short: branch, Kind: RefLocal, Hash: lr.Hash()})
		}
		op := &Op{
			Title: "checkout " + branch + " tracking " + r.Short,
			Refs:  []RefUpdate{{Name: local, New: r.Hash}},
			Head:  &HeadChange{Old: head, New: plumbing.NewSymbolicReference(plumbing.HEAD, local)},
		}
		if err := rp.Apply(op); err != nil {
			return nil, err
		}
		if cfg, err := rp.R.Config(); err == nil {
			cfg.Branches[branch] = &config.Branch{Name: branch, Remote: remote, Merge: plumbing.NewBranchReferenceName(branch)}
			_ = rp.R.SetConfig(cfg)
		}
		return op, nil
	}
	return rp.CheckoutDetached(r.Hash)
}

// CheckoutDetached points HEAD directly at h.
func (rp *Repo) CheckoutDetached(h Hash) (*Op, error) {
	head, _, err := rp.headState()
	if err != nil {
		return nil, err
	}
	op := &Op{Title: "checkout " + h.String()[:7] + " (detached)", Head: &HeadChange{Old: head, New: plumbing.NewHashReference(plumbing.HEAD, h)}}
	return op, rp.Apply(op)
}

// Reset moves the current branch (or detached HEAD) to h.
func (rp *Repo) Reset(h Hash, mode SyncMode) (*Op, error) {
	name, tip, err := rp.targetRef("")
	if err != nil {
		return nil, err
	}
	kind := map[SyncMode]string{SyncNone: "soft", SyncMixed: "mixed", SyncHard: "hard", SyncSafe: "keep"}[mode]
	op := &Op{Title: fmt.Sprintf("reset %s to %s (%s)", name.Short(), h.String()[:7], kind), Sync: mode}
	if name == plumbing.HEAD {
		op.Head = &HeadChange{Old: plumbing.NewHashReference(plumbing.HEAD, tip), New: plumbing.NewHashReference(plumbing.HEAD, h)}
	} else {
		op.Refs = []RefUpdate{{Name: name, Old: tip, New: h}}
	}
	return op, rp.Apply(op)
}

// Commit commits the index (or amends HEAD), returning an undoable op that
// soft-resets back to the previous HEAD.
func (rp *Repo) Commit(msg string, amend bool) (*Op, error) {
	msg = cleanMessage(msg)
	if msg == "" {
		return nil, errors.New("empty commit message")
	}
	name, tip, err := rp.targetRef("")
	if err != nil {
		return nil, err
	}
	nh, err := rp.CommitIndex(msg, amend)
	if err != nil {
		return nil, err
	}

	title := "commit " + nh.String()[:7]
	if amend {
		title = "amend " + tip.String()[:7]
	}
	op := &Op{Title: title, Sync: SyncNone, Select: nh}
	if name == plumbing.HEAD {
		op.Head = &HeadChange{Old: plumbing.NewHashReference(plumbing.HEAD, tip), New: plumbing.NewHashReference(plumbing.HEAD, nh)}
	} else {
		op.Refs = []RefUpdate{{Name: name, Old: tip, New: nh}}
	}
	return op, nil
}

// CommitIndex commits the index with msg. With amend it replaces HEAD.
func (rp *Repo) CommitIndex(msg string, amend bool) (Hash, error) {
	wt, err := rp.R.Worktree()
	if err != nil {
		return Hash{}, ErrBare
	}
	opts := &git.CommitOptions{Amend: amend}
	if !amend {
		sig, err := rp.Signature()
		if err != nil {
			return Hash{}, err
		}
		opts.Author, opts.Committer = &sig, &sig
	}
	return wt.Commit(msg, opts)
}
