package repo

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/format/index"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// ErrBare is returned for worktree operations on a bare repository.
var ErrBare = errors.New("repository has no worktree")

// Change is one path with local modifications.
type Change struct {
	Path     string
	Staged   byte // index vs HEAD: 'A', 'M', 'D' or 0
	Unstaged byte // worktree vs index: 'M', 'D', '?' (untracked) or 0
}

// Status lists local changes, sorted by path.
func (rp *Repo) Status() ([]Change, error) {
	wt, err := rp.R.Worktree()
	if err != nil {
		return nil, ErrBare
	}
	st, err := wt.StatusWithOptions(git.StatusOptions{Strategy: git.Preload})
	if err != nil {
		return nil, err
	}
	var out []Change
	for path, fs := range st {
		c := Change{Path: path}
		switch fs.Staging {
		case git.Added, git.Modified, git.Deleted, git.Renamed, git.Copied:
			c.Staged = byte(fs.Staging)
		}
		switch fs.Worktree {
		case git.Modified, git.Deleted:
			c.Unstaged = byte(fs.Worktree)
		case git.Untracked:
			if fs.Staging == git.Untracked {
				c.Unstaged = '?'
			}
		}
		if c.Staged != 0 || c.Unstaged != 0 {
			out = append(out, c)
		}
	}
	slices.SortFunc(out, func(a, b Change) int { return strings.Compare(a.Path, b.Path) })
	return out, nil
}

// Dirty reports whether any tracked file has staged or unstaged changes.
// Untracked files do not count: history operations never touch them.
func Dirty(changes []Change) bool {
	for _, c := range changes {
		if c.Staged != 0 || (c.Unstaged != 0 && c.Unstaged != '?') {
			return true
		}
	}
	return false
}

func (rp *Repo) headTree() (*object.Tree, error) {
	h, err := rp.R.Head()
	if err != nil {
		return nil, nil // unborn branch: empty tree
	}
	return rp.commitTree(h.Hash())
}

// StagedDiff diffs HEAD against the index for the staged paths.
func (rp *Repo) StagedDiff(changes []Change, ctx int) ([]FileDiff, error) {
	idx, err := rp.R.Storer.Index()
	if err != nil {
		return nil, err
	}
	tree, err := rp.headTree()
	if err != nil {
		return nil, err
	}
	var out []FileDiff
	for _, c := range changes {
		if c.Staged == 0 {
			continue
		}
		a, err := treeFile(tree, c.Path)
		if err != nil {
			return nil, err
		}
		var b FileContent
		if e, err := idx.Entry(c.Path); err == nil {
			if b, err = rp.blob(e.Hash, e.Mode); err != nil {
				return nil, err
			}
		}
		out = append(out, DiffContents(c.Path, "", a, b, ctx))
	}
	return out, nil
}

// UnstagedDiff diffs the index against the working tree, including
// untracked files as additions.
func (rp *Repo) UnstagedDiff(changes []Change, ctx int) ([]FileDiff, error) {
	if rp.Root == "" {
		return nil, ErrBare
	}
	idx, err := rp.R.Storer.Index()
	if err != nil {
		return nil, err
	}
	var out []FileDiff
	for _, c := range changes {
		if c.Unstaged == 0 {
			continue
		}
		var a FileContent
		if e, err := idx.Entry(c.Path); err == nil {
			if a, err = rp.blob(e.Hash, e.Mode); err != nil {
				return nil, err
			}
		}
		b, err := rp.worktreeFile(c.Path)
		if err != nil {
			return nil, err
		}
		out = append(out, DiffContents(c.Path, "", a, b, ctx))
	}
	return out, nil
}

func treeFile(t *object.Tree, path string) (FileContent, error) {
	if t == nil {
		return FileContent{}, nil
	}
	f, err := t.File(path)
	if errors.Is(err, object.ErrFileNotFound) || errors.Is(err, object.ErrDirectoryNotFound) || errors.Is(err, object.ErrEntryNotFound) {
		return FileContent{}, nil
	}
	if err != nil {
		return FileContent{}, err
	}
	return blobContent(f)
}

func (rp *Repo) worktreeFile(path string) (FileContent, error) {
	full := filepath.Join(rp.Root, filepath.FromSlash(path))
	fi, err := os.Lstat(full)
	if errors.Is(err, os.ErrNotExist) {
		return FileContent{}, nil
	}
	if err != nil {
		return FileContent{}, err
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(full)
		return FileContent{Exists: true, Data: []byte(target), Mode: filemode.Symlink}, err
	}
	f, err := os.Open(full)
	if err != nil {
		return FileContent{}, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxDiffBytes+1))
	mode := filemode.Regular
	if fi.Mode()&0o111 != 0 {
		mode = filemode.Executable
	}
	return FileContent{Exists: true, Data: data, Mode: mode}, err
}

// Stage adds the working-tree state of paths (including deletions) to the
// index.
func (rp *Repo) Stage(paths ...string) error {
	wt, err := rp.R.Worktree()
	if err != nil {
		return ErrBare
	}
	for _, p := range paths {
		if _, err := wt.Add(p); err != nil {
			return err
		}
	}
	return nil
}

// Unstage resets the index entries of paths back to HEAD, leaving the
// working tree alone.
func (rp *Repo) Unstage(paths ...string) error {
	idx, err := rp.R.Storer.Index()
	if err != nil {
		return err
	}
	tree, err := rp.headTree()
	if err != nil {
		return err
	}
	for _, p := range paths {
		var te *object.TreeEntry
		if tree != nil {
			te, _ = tree.FindEntry(p)
		}
		if te == nil {
			_, _ = idx.Remove(p)
			continue
		}
		e, err := idx.Entry(p)
		if err != nil {
			e = idx.Add(p)
		}
		e.Hash = te.Hash
		e.Mode = te.Mode
		// forget the stat cache so git re-hashes the working file
		e.ModifiedAt, e.CreatedAt = time.Time{}, time.Time{}
		e.Size = 0
	}
	return rp.R.Storer.SetIndex(idx)
}

// Discard reverts the working-tree copies of paths to the index. Untracked
// paths are deleted.
func (rp *Repo) Discard(changes ...Change) error {
	if rp.Root == "" {
		return ErrBare
	}
	idx, err := rp.R.Storer.Index()
	if err != nil {
		return err
	}
	for _, c := range changes {
		full := filepath.Join(rp.Root, filepath.FromSlash(c.Path))
		e, err := idx.Entry(c.Path)
		if errors.Is(err, index.ErrEntryNotFound) {
			if err := os.Remove(full); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		fc, err := rp.blob(e.Hash, e.Mode)
		if err != nil {
			return err
		}
		if err := writeWorktreeFile(full, fc); err != nil {
			return err
		}
	}
	return nil
}

func writeWorktreeFile(full string, fc FileContent) error {
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	_ = os.Remove(full)
	if fc.Mode == filemode.Symlink {
		return os.Symlink(string(fc.Data), full)
	}
	perm := os.FileMode(0o644)
	if fc.Mode == filemode.Executable {
		perm = 0o755
	}
	return os.WriteFile(full, fc.Data, perm)
}
