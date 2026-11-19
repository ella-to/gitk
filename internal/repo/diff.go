package repo

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/sergi/go-diff/diffmatchpatch"
)

// LineKind classifies a line of a unified diff.
type LineKind uint8

const (
	LineContext LineKind = iota
	LineAdd
	LineDel
	LineHunk // "@@ -a,b +c,d @@" separator
)

// Line is one rendered diff line. Old and New are 1-based line numbers (0
// when the line does not exist on that side).
type Line struct {
	Kind     LineKind
	Old, New int
	Text     string
}

// Status letters follow git: Added, Modified, Deleted, Renamed, Type change.
const (
	StatusAdded    = 'A'
	StatusModified = 'M'
	StatusDeleted  = 'D'
	StatusRenamed  = 'R'
)

// FileDiff is the change to one path.
type FileDiff struct {
	Path    string // new path (old path for deletions)
	OldPath string // set for renames
	Status  byte
	Binary  bool
	TooBig  bool
	Adds    int
	Dels    int
	Lines   []Line
	OldMode filemode.FileMode
	NewMode filemode.FileMode
}

// Name is the display name, "old → new" for renames.
func (f *FileDiff) Name() string {
	if f.OldPath != "" && f.OldPath != f.Path {
		return f.OldPath + " → " + f.Path
	}
	return f.Path
}

// maxDiffBytes caps how much text one side of a file diff may have before the
// diff is summarized instead of rendered.
const maxDiffBytes = 4 << 20

// Unified computes a line diff of a → b with ctx lines of context, as hunks.
func Unified(a, b string, ctx int) (lines []Line, adds, dels int) {
	ops := lineOps(a, b)
	// indices of changed ops; hunks cover each change ±ctx ops
	type span struct{ lo, hi int }
	var spans []span
	for i, op := range ops {
		if op.kind == LineContext {
			continue
		}
		lo, hi := max(i-ctx, 0), min(i+ctx+1, len(ops))
		if n := len(spans); n > 0 && lo <= spans[n-1].hi {
			spans[n-1].hi = max(spans[n-1].hi, hi)
		} else {
			spans = append(spans, span{lo, hi})
		}
		if op.kind == LineAdd {
			adds++
		} else {
			dels++
		}
	}
	for _, s := range spans {
		first := ops[s.lo]
		var oldN, newN int
		for _, op := range ops[s.lo:s.hi] {
			if op.kind != LineAdd {
				oldN++
			}
			if op.kind != LineDel {
				newN++
			}
		}
		lines = append(lines, Line{Kind: LineHunk, Text: fmt.Sprintf("@@ -%d,%d +%d,%d @@", hunkStart(first.old, oldN), oldN, hunkStart(first.new, newN), newN)})
		for _, op := range ops[s.lo:s.hi] {
			l := Line{Kind: op.kind, Text: op.text}
			if op.kind != LineAdd {
				l.Old = op.old
			}
			if op.kind != LineDel {
				l.New = op.new
			}
			lines = append(lines, l)
		}
	}
	return lines, adds, dels
}

// hunkStart is git's hunk header start: the first line, or the line before
// the hunk when that side is empty.
func hunkStart(start, n int) int {
	if n == 0 {
		return start - 1
	}
	return start
}

type lineOp struct {
	kind     LineKind
	old, new int // 1-based position on each side (next line for the absent side)
	text     string
}

// lineOps diffs a and b line by line (Myers via diffmatchpatch).
func lineOps(a, b string) []lineOp {
	dmp := diffmatchpatch.New()
	ra, rb, table := dmp.DiffLinesToRunes(a, b)
	diffs := dmp.DiffMainRunes(ra, rb, false)
	var ops []lineOp
	oldN, newN := 1, 1
	for _, d := range diffs {
		for _, r := range d.Text {
			text := strings.TrimSuffix(table[r], "\n")
			switch d.Type {
			case diffmatchpatch.DiffEqual:
				ops = append(ops, lineOp{LineContext, oldN, newN, text})
				oldN++
				newN++
			case diffmatchpatch.DiffDelete:
				ops = append(ops, lineOp{LineDel, oldN, newN, text})
				oldN++
			case diffmatchpatch.DiffInsert:
				ops = append(ops, lineOp{LineAdd, oldN, newN, text})
				newN++
			}
		}
	}
	return ops
}

// SplitLines splits s into lines, keeping each line's "\n".
func SplitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.SplitAfter(s, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func isBinary(b []byte) bool {
	n := min(len(b), 8000)
	return bytes.IndexByte(b[:n], 0) >= 0
}

// FileContent describes one side of a file diff.
type FileContent struct {
	Exists bool
	Data   []byte
	Mode   filemode.FileMode
}

// DiffContents builds a FileDiff from two sides of a path.
func DiffContents(path, oldPath string, a, b FileContent, ctx int) FileDiff {
	fd := FileDiff{Path: path, OldPath: oldPath, OldMode: a.Mode, NewMode: b.Mode}
	switch {
	case !a.Exists:
		fd.Status = StatusAdded
	case !b.Exists:
		fd.Status = StatusDeleted
	case oldPath != "" && oldPath != path:
		fd.Status = StatusRenamed
	default:
		fd.Status = StatusModified
	}
	if isBinary(a.Data) || isBinary(b.Data) {
		fd.Binary = true
		return fd
	}
	if len(a.Data) > maxDiffBytes || len(b.Data) > maxDiffBytes {
		fd.TooBig = true
		return fd
	}
	fd.Lines, fd.Adds, fd.Dels = Unified(string(a.Data), string(b.Data), ctx)
	return fd
}

func blobContent(f *object.File) (FileContent, error) {
	if f == nil {
		return FileContent{}, nil
	}
	rd, err := f.Reader()
	if err != nil {
		return FileContent{}, err
	}
	defer rd.Close()
	data, err := io.ReadAll(io.LimitReader(rd, maxDiffBytes+1))
	if err != nil {
		return FileContent{}, err
	}
	return FileContent{Exists: true, Data: data, Mode: f.Mode}, nil
}

// TreeDiff diffs two trees (either may be nil for the empty tree), detecting
// renames.
func (rp *Repo) TreeDiff(from, to *object.Tree, ctx int) ([]FileDiff, error) {
	if from == nil {
		from = &object.Tree{}
	}
	if to == nil {
		to = &object.Tree{}
	}
	changes, err := object.DiffTreeWithOptions(context.Background(), from, to, object.DefaultDiffTreeOptions)
	if err != nil {
		return nil, err
	}
	out := make([]FileDiff, 0, len(changes))
	for _, ch := range changes {
		a, b, err := ch.Files()
		if err != nil {
			return nil, err
		}
		ca, err := blobContent(a)
		if err != nil {
			return nil, err
		}
		cb, err := blobContent(b)
		if err != nil {
			return nil, err
		}
		path, old := ch.To.Name, ch.From.Name
		if path == "" {
			path, old = old, ""
		}
		out = append(out, DiffContents(path, old, ca, cb, ctx))
	}
	slices.SortFunc(out, func(x, y FileDiff) int { return strings.Compare(x.Path, y.Path) })
	return out, nil
}

// CommitDiff diffs a commit against its first parent (the empty tree for a
// root commit). Merges are shown against their first parent, like
// `git show --first-parent`.
func (rp *Repo) CommitDiff(h Hash, ctx int) ([]FileDiff, error) {
	c, err := rp.R.CommitObject(h)
	if err != nil {
		return nil, err
	}
	to, err := c.Tree()
	if err != nil {
		return nil, err
	}
	var from *object.Tree
	if c.NumParents() > 0 {
		p, err := c.Parent(0)
		if err != nil {
			return nil, err
		}
		if from, err = p.Tree(); err != nil {
			return nil, err
		}
	}
	return rp.TreeDiff(from, to, ctx)
}

// RangeDiff diffs the trees of two commits (a → b).
func (rp *Repo) RangeDiff(a, b Hash, ctx int) ([]FileDiff, error) {
	ta, err := rp.commitTree(a)
	if err != nil {
		return nil, err
	}
	tb, err := rp.commitTree(b)
	if err != nil {
		return nil, err
	}
	return rp.TreeDiff(ta, tb, ctx)
}

func (rp *Repo) commitTree(h Hash) (*object.Tree, error) {
	c, err := rp.R.CommitObject(h)
	if err != nil {
		return nil, err
	}
	return c.Tree()
}

// FileAt returns the contents of path in commit h.
func (rp *Repo) FileAt(h Hash, path string) (FileContent, error) {
	t, err := rp.commitTree(h)
	if err != nil {
		return FileContent{}, err
	}
	f, err := t.File(path)
	if err == object.ErrFileNotFound {
		return FileContent{}, nil
	}
	if err != nil {
		return FileContent{}, err
	}
	return blobContent(f)
}

func (rp *Repo) blob(h plumbing.Hash, mode filemode.FileMode) (FileContent, error) {
	b, err := rp.R.BlobObject(h)
	if err != nil {
		return FileContent{}, err
	}
	rd, err := b.Reader()
	if err != nil {
		return FileContent{}, err
	}
	defer rd.Close()
	data, err := io.ReadAll(io.LimitReader(rd, maxDiffBytes+1))
	return FileContent{Exists: true, Data: data, Mode: mode}, err
}
