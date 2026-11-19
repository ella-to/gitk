// Package repo is gitk's view of a git repository, built on go-git: it loads
// the commit graph and refs, computes diffs and status, and performs the
// history-editing operations the UI offers (reword, cherry-pick, revert, ...).
package repo

import (
	"cmp"
	"container/heap"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// Hash is a git object id.
type Hash = plumbing.Hash

// Commit is the subset of a commit object the UI needs.
type Commit struct {
	Hash        Hash
	Parents     []Hash
	Author      string
	AuthorEmail string
	AuthorTime  time.Time
	Committer   string
	CommitEmail string
	CommitTime  time.Time
	Message     string
	Subject     string // first line of Message
	Signed      bool   // carries a PGP/SSH signature (lost when rewritten)
}

// Short is the abbreviated hash.
func (c *Commit) Short() string { return c.Hash.String()[:7] }

// RefKind orders and colors refs.
type RefKind int

const (
	RefHead   RefKind = iota // detached HEAD
	RefLocal                 // refs/heads/*
	RefRemote                // refs/remotes/*
	RefTag                   // refs/tags/*
)

// Ref is a branch, tag or detached HEAD pointing at a commit.
type Ref struct {
	Name   plumbing.ReferenceName
	Short  string
	Kind   RefKind
	Hash   Hash // the commit it resolves to (tags are peeled)
	IsHead bool // the branch HEAD points to
}

// Scope selects which refs seed the commit walk.
type Scope int

const (
	ScopeAll  Scope = iota // every branch, remote and tag (gitk --all)
	ScopeHead              // HEAD's history only
	ScopeRevs              // the history of the revisions passed to Load
)

// Graph is a loaded snapshot of the repository history.
type Graph struct {
	Commits  []*Commit // newest first, never a parent before its child
	Index    map[Hash]int
	Children map[Hash][]Hash
	Refs     []Ref
	RefsAt   map[Hash][]Ref
	Head     Hash   // commit HEAD resolves to (zero in an empty repo)
	HeadRef  string // branch name HEAD points to; "" when detached
	LoadedAt time.Time
}

// Repo wraps an opened go-git repository.
type Repo struct {
	R    *git.Repository
	Root string // worktree root ("" for bare repositories)
	Name string
}

// Open finds the repository containing path.
func Open(path string) (*Repo, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	r, err := git.PlainOpenWithOptions(abs, &git.PlainOpenOptions{DetectDotGit: true, EnableDotGitCommonDir: true})
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", abs, err)
	}
	rp := &Repo{R: r}
	if wt, err := r.Worktree(); err == nil {
		rp.Root = wt.Filesystem.Root()
		rp.Name = filepath.Base(rp.Root)
	} else {
		rp.Name = filepath.Base(abs)
	}
	return rp, nil
}

// Refs lists the branches, remotes and tags, each resolved to a commit, plus
// HEAD when it is detached. Symbolic remote HEADs and stashes are skipped.
func (rp *Repo) Refs() (refs []Ref, head Hash, headRef string, err error) {
	if h, err := rp.R.Head(); err == nil {
		head = h.Hash()
		if h.Name().IsBranch() {
			headRef = h.Name().Short()
		}
	} else if errors.Is(err, plumbing.ErrReferenceNotFound) {
		// unborn branch: HEAD -> refs/heads/main with no commits yet
		if sym, err := rp.R.Storer.Reference(plumbing.HEAD); err == nil && sym.Type() == plumbing.SymbolicReference {
			headRef = sym.Target().Short()
		}
	} else {
		return nil, head, "", err
	}

	it, err := rp.R.References()
	if err != nil {
		return nil, head, headRef, err
	}
	defer it.Close()
	err = it.ForEach(func(ref *plumbing.Reference) error {
		if ref.Type() != plumbing.HashReference {
			return nil
		}
		name := ref.Name()
		var kind RefKind
		switch {
		case name.IsBranch():
			kind = RefLocal
		case name.IsRemote():
			if strings.HasSuffix(name.String(), "/HEAD") {
				return nil
			}
			kind = RefRemote
		case name.IsTag():
			kind = RefTag
		default:
			return nil // HEAD, stash, notes...
		}
		h, ok := rp.peel(ref.Hash())
		if !ok {
			return nil
		}
		refs = append(refs, Ref{
			Name:   name,
			Short:  name.Short(),
			Kind:   kind,
			Hash:   h,
			IsHead: kind == RefLocal && name.Short() == headRef,
		})
		return nil
	})
	if err != nil {
		return nil, head, headRef, err
	}
	if headRef == "" && !head.IsZero() {
		refs = append(refs, Ref{Name: plumbing.HEAD, Short: "HEAD", Kind: RefHead, Hash: head, IsHead: true})
	}
	slices.SortFunc(refs, func(a, b Ref) int {
		if a.IsHead != b.IsHead {
			if a.IsHead {
				return -1
			}
			return 1
		}
		return cmp.Or(cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.Short, b.Short))
	})
	return refs, head, headRef, nil
}

// peel resolves h (a commit or annotated tag) to a commit hash.
func (rp *Repo) peel(h Hash) (Hash, bool) {
	for range 8 {
		obj, err := rp.R.Storer.EncodedObject(plumbing.AnyObject, h)
		if err != nil {
			return h, false
		}
		switch obj.Type() {
		case plumbing.CommitObject:
			return h, true
		case plumbing.TagObject:
			tag, err := object.DecodeTag(rp.R.Storer, obj)
			if err != nil {
				return h, false
			}
			h = tag.Target
		default:
			return h, false
		}
	}
	return h, false
}

// Resolve turns a branch, remote branch, tag, (abbreviated) hash or
// expression like main~2 into the commit it names.
func (rp *Repo) Resolve(rev string) (Hash, error) {
	h, err := rp.R.ResolveRevision(plumbing.Revision(rev))
	if err != nil {
		return Hash{}, fmt.Errorf("unknown revision %q", rev)
	}
	return *h, nil
}

// Load walks the history reachable from the scope's refs (for ScopeRevs, from
// revs) and returns it in git's --date-order: newest first by committer date,
// children before parents. Revisions that no longer resolve, such as a branch
// deleted since startup, are skipped.
func (rp *Repo) Load(scope Scope, revs ...string) (*Graph, error) {
	refs, head, headRef, err := rp.Refs()
	if err != nil {
		return nil, err
	}
	g := &Graph{
		Index:    map[Hash]int{},
		Children: map[Hash][]Hash{},
		RefsAt:   map[Hash][]Ref{},
		Refs:     refs,
		Head:     head,
		HeadRef:  headRef,
		LoadedAt: time.Now(),
	}
	for _, r := range refs {
		g.RefsAt[r.Hash] = append(g.RefsAt[r.Hash], r)
	}

	var tips []Hash
	switch scope {
	case ScopeHead:
		if !head.IsZero() {
			tips = append(tips, head)
		}
	case ScopeRevs:
		for _, rev := range revs {
			if h, err := rp.Resolve(rev); err == nil {
				tips = append(tips, h)
			}
		}
	default:
		for _, r := range refs {
			tips = append(tips, r.Hash)
		}
	}

	all := map[Hash]*Commit{}
	stack := slices.Clone(tips)
	for len(stack) > 0 {
		h := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if _, seen := all[h]; seen {
			continue
		}
		c, err := rp.R.CommitObject(h)
		if err != nil {
			if errors.Is(err, plumbing.ErrObjectNotFound) {
				continue // shallow clone boundary or missing object
			}
			return nil, err
		}
		cm := toCommit(c)
		all[h] = cm
		for _, p := range c.ParentHashes {
			if _, seen := all[p]; !seen {
				stack = append(stack, p)
			}
		}
	}

	// drop parents that are missing (shallow clones) so the graph stays closed
	pending := map[Hash]int{} // number of children not yet emitted
	for _, c := range all {
		c.Parents = slices.DeleteFunc(c.Parents, func(p Hash) bool { _, ok := all[p]; return !ok })
		for _, p := range c.Parents {
			pending[p]++
			g.Children[p] = append(g.Children[p], c.Hash)
		}
	}

	q := &commitHeap{}
	for _, c := range all {
		if pending[c.Hash] == 0 {
			heap.Push(q, c)
		}
	}
	g.Commits = make([]*Commit, 0, len(all))
	for q.Len() > 0 {
		c := heap.Pop(q).(*Commit)
		g.Index[c.Hash] = len(g.Commits)
		g.Commits = append(g.Commits, c)
		for _, p := range c.Parents {
			if pending[p]--; pending[p] == 0 {
				heap.Push(q, all[p])
			}
		}
	}
	for p, kids := range g.Children {
		slices.SortFunc(kids, func(a, b Hash) int { return cmp.Compare(g.Index[a], g.Index[b]) })
		g.Children[p] = kids
	}
	return g, nil
}

func toCommit(c *object.Commit) *Commit {
	subject, _, _ := strings.Cut(strings.TrimLeft(c.Message, "\n"), "\n")
	return &Commit{
		Hash:        c.Hash,
		Parents:     slices.Clone(c.ParentHashes),
		Author:      c.Author.Name,
		AuthorEmail: c.Author.Email,
		AuthorTime:  c.Author.When,
		Committer:   c.Committer.Name,
		CommitEmail: c.Committer.Email,
		CommitTime:  c.Committer.When,
		Message:     c.Message,
		Subject:     strings.TrimSpace(subject),
		Signed:      c.PGPSignature != "",
	}
}

// commitHeap pops the newest commit first (ties broken by hash for stability).
type commitHeap []*Commit

func (h commitHeap) Len() int { return len(h) }
func (h commitHeap) Less(i, j int) bool {
	if !h[i].CommitTime.Equal(h[j].CommitTime) {
		return h[i].CommitTime.After(h[j].CommitTime)
	}
	return h[i].Hash.String() < h[j].Hash.String()
}
func (h commitHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *commitHeap) Push(x any)   { *h = append(*h, x.(*Commit)) }
func (h *commitHeap) Pop() any {
	old := *h
	x := old[len(old)-1]
	*h = old[:len(old)-1]
	return x
}

// Contains reports whether ancestor is reachable from tip within g.
func (g *Graph) Contains(tip, ancestor Hash) bool {
	ti, ok1 := g.Index[tip]
	ai, ok2 := g.Index[ancestor]
	if !ok1 || !ok2 || ai < ti {
		return false // in date-order a parent never precedes its child
	}
	seen := map[Hash]bool{}
	stack := []Hash{tip}
	for len(stack) > 0 {
		h := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if h == ancestor {
			return true
		}
		if seen[h] || g.Index[h] > ai {
			continue
		}
		seen[h] = true
		if i, ok := g.Index[h]; ok {
			stack = append(stack, g.Commits[i].Parents...)
		}
	}
	return false
}

// BranchesContaining lists the refs of the given kinds (all kinds when none
// are given) whose history includes h, in g.Refs order. It walks h's
// descendants once instead of searching from every ref.
func (g *Graph) BranchesContaining(h Hash, kinds ...RefKind) []Ref {
	if _, ok := g.Index[h]; !ok {
		return nil
	}
	reached := map[Hash]bool{h: true}
	queue := []Hash{h}
	for len(queue) > 0 {
		cur := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		for _, ch := range g.Children[cur] {
			if !reached[ch] {
				reached[ch] = true
				queue = append(queue, ch)
			}
		}
	}
	var out []Ref
	for _, r := range g.Refs {
		if (len(kinds) == 0 || slices.Contains(kinds, r.Kind)) && reached[r.Hash] {
			out = append(out, r)
		}
	}
	return out
}

// Commit returns the loaded commit for h, or nil.
func (g *Graph) Commit(h Hash) *Commit {
	if i, ok := g.Index[h]; ok {
		return g.Commits[i]
	}
	return nil
}

// Fingerprint summarizes HEAD and every ref so a poller can detect changes
// made outside gitk cheaply.
func (rp *Repo) Fingerprint() string {
	var lines []string
	if h, err := rp.R.Storer.Reference(plumbing.HEAD); err == nil {
		lines = append(lines, h.String())
	}
	if it, err := rp.R.Storer.IterReferences(); err == nil {
		_ = it.ForEach(func(r *plumbing.Reference) error {
			lines = append(lines, r.String())
			return nil
		})
	}
	slices.Sort(lines)
	return strings.Join(lines, "\n")
}
