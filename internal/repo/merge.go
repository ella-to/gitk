package repo

import (
	"cmp"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/sergi/go-diff/diffmatchpatch"
)

// ConflictError reports paths a 3-way merge could not resolve.
type ConflictError struct {
	Op    string
	Paths []string
}

func (e *ConflictError) Error() string {
	const show = 5
	paths := e.Paths
	more := ""
	if len(paths) > show {
		more = fmt.Sprintf(" (+%d more)", len(paths)-show)
		paths = paths[:show]
	}
	return fmt.Sprintf("%s: conflicts in %s%s", e.Op, strings.Join(paths, ", "), more)
}

// Merge3 merges the line-level changes base→a and base→b. When both sides
// changed the same region differently the result holds conflict markers and
// ok is false.
func Merge3(base, a, b, labelA, labelB string) (out string, ok bool) {
	bl, al, blB := SplitLines(base), SplitLines(a), SplitLines(b)
	ma := matchLines(bl, al)
	mb := matchLines(bl, blB)

	var sb strings.Builder
	ok = true
	i, ia, ib := 0, 0, 0
	for {
		// copy the stable run where both sides still agree with base
		for i < len(bl) && ma[i] == ia && mb[i] == ib {
			sb.WriteString(bl[i])
			i, ia, ib = i+1, ia+1, ib+1
		}
		if i == len(bl) && ia == len(al) && ib == len(blB) {
			break
		}
		// next base line both sides kept: the end of this unstable chunk
		j := i
		for j < len(bl) && (ma[j] < 0 || mb[j] < 0) {
			j++
		}
		ja, jb := len(al), len(blB)
		if j < len(bl) {
			ja, jb = ma[j], mb[j]
		}
		cBase, cA, cB := bl[i:j], al[ia:ja], blB[ib:jb]
		switch {
		case slices.Equal(cA, cBase):
			writeAll(&sb, cB)
		case slices.Equal(cB, cBase), slices.Equal(cA, cB):
			writeAll(&sb, cA)
		default:
			ok = false
			sb.WriteString("<<<<<<< " + labelA + "\n")
			writeAll(&sb, ensureNL(cA))
			sb.WriteString("=======\n")
			writeAll(&sb, ensureNL(cB))
			sb.WriteString(">>>>>>> " + labelB + "\n")
		}
		i, ia, ib = j, ja, jb
	}
	return sb.String(), ok
}

func writeAll(sb *strings.Builder, lines []string) {
	for _, l := range lines {
		sb.WriteString(l)
	}
}

func ensureNL(lines []string) []string {
	if n := len(lines); n > 0 && !strings.HasSuffix(lines[n-1], "\n") {
		lines = append(slices.Clone(lines[:n-1]), lines[n-1]+"\n")
	}
	return lines
}

// matchLines maps each base line index to the index of the identical line in
// other that a minimal diff keeps, or -1 when the line was changed.
func matchLines(base, other []string) []int {
	m := make([]int, len(base))
	for i := range m {
		m[i] = -1
	}
	dmp := diffmatchpatch.New()
	ra, rb, _ := dmp.DiffLinesToRunes(strings.Join(base, ""), strings.Join(other, ""))
	i, j := 0, 0
	for _, d := range dmp.DiffMainRunes(ra, rb, false) {
		n := len([]rune(d.Text))
		switch d.Type {
		case diffmatchpatch.DiffEqual:
			for k := range n {
				m[i+k] = j + k
			}
			i, j = i+n, j+n
		case diffmatchpatch.DiffDelete:
			i += n
		case diffmatchpatch.DiffInsert:
			j += n
		}
	}
	return m
}

// emptyTree is the id of the tree with no entries.
var emptyTree = plumbing.NewHash("4b825dc642cb6eb9a060e54bf8d69288fbee4904")

// treeMerger performs a recursive 3-way merge of trees, writing new blobs and
// trees into the object store.
type treeMerger struct {
	rp             *Repo
	labelA, labelB string
	conflicts      []string
}

// MergeTrees merges the change base→theirs into ours. It returns the merged
// tree, which on conflict contains marker-annotated files (and the conflicting
// paths are reported in a *ConflictError).
func (rp *Repo) MergeTrees(base, ours, theirs Hash, labelOurs, labelTheirs, op string) (Hash, error) {
	m := &treeMerger{rp: rp, labelA: labelOurs, labelB: labelTheirs}
	h, err := m.merge("", base, ours, theirs)
	if err != nil {
		return h, err
	}
	if len(m.conflicts) > 0 {
		return h, &ConflictError{Op: op, Paths: m.conflicts}
	}
	return h, nil
}

func (m *treeMerger) entries(h Hash) (map[string]object.TreeEntry, error) {
	out := map[string]object.TreeEntry{}
	if h.IsZero() || h == emptyTree {
		return out, nil
	}
	t, err := m.rp.R.TreeObject(h)
	if err != nil {
		return nil, err
	}
	for _, e := range t.Entries {
		out[e.Name] = e
	}
	return out, nil
}

func sameEntry(a, b *object.TreeEntry) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Hash == b.Hash && a.Mode == b.Mode
}

func (m *treeMerger) merge(dir string, base, ours, theirs Hash) (Hash, error) {
	switch {
	case theirs == base:
		return ours, nil
	case ours == base, ours == theirs:
		return theirs, nil
	}
	be, err := m.entries(base)
	if err != nil {
		return Hash{}, err
	}
	oe, err := m.entries(ours)
	if err != nil {
		return Hash{}, err
	}
	te, err := m.entries(theirs)
	if err != nil {
		return Hash{}, err
	}
	names := map[string]bool{}
	for _, set := range []map[string]object.TreeEntry{be, oe, te} {
		for n := range set {
			names[n] = true
		}
	}
	var result []object.TreeEntry
	for name := range names {
		path := dir + name
		b, o, t := ptr(be, name), ptr(oe, name), ptr(te, name)
		e, err := m.mergeEntry(path, name, b, o, t)
		if err != nil {
			return Hash{}, err
		}
		if e != nil {
			result = append(result, *e)
		}
	}
	return m.rp.writeTree(result)
}

func ptr(set map[string]object.TreeEntry, name string) *object.TreeEntry {
	if e, ok := set[name]; ok {
		return &e
	}
	return nil
}

func isDir(e *object.TreeEntry) bool { return e != nil && e.Mode == filemode.Dir }
func isFile(e *object.TreeEntry) bool {
	return e != nil && e.Mode != filemode.Dir && e.Mode != filemode.Submodule
}

func (m *treeMerger) mergeEntry(path, name string, b, o, t *object.TreeEntry) (*object.TreeEntry, error) {
	switch {
	case sameEntry(t, b):
		return o, nil
	case sameEntry(o, b), sameEntry(o, t):
		return t, nil
	}
	// both sides changed this entry, differently
	if isDir(o) && isDir(t) && (b == nil || isDir(b)) {
		var bh Hash
		if b != nil {
			bh = b.Hash
		}
		h, err := m.merge(path+"/", bh, o.Hash, t.Hash)
		if err != nil {
			return nil, err
		}
		if h == emptyTree {
			return nil, nil // git does not store empty directories
		}
		return &object.TreeEntry{Name: name, Mode: filemode.Dir, Hash: h}, nil
	}
	if isFile(o) && isFile(t) && (b == nil || isFile(b)) && o.Mode != filemode.Symlink && t.Mode != filemode.Symlink {
		var base []byte
		if b != nil {
			data, err := m.rp.blobBytes(b.Hash)
			if err != nil {
				return nil, err
			}
			base = data
		}
		oursData, err := m.rp.blobBytes(o.Hash)
		if err != nil {
			return nil, err
		}
		theirsData, err := m.rp.blobBytes(t.Hash)
		if err != nil {
			return nil, err
		}
		mode := t.Mode
		if b != nil && t.Mode == b.Mode {
			mode = o.Mode
		}
		if isBinary(base) || isBinary(oursData) || isBinary(theirsData) {
			m.conflicts = append(m.conflicts, path)
			return o, nil
		}
		merged, ok := Merge3(string(base), string(oursData), string(theirsData), m.labelA, m.labelB)
		if !ok {
			m.conflicts = append(m.conflicts, path)
		}
		h, err := m.rp.writeBlob([]byte(merged))
		if err != nil {
			return nil, err
		}
		return &object.TreeEntry{Name: name, Mode: mode, Hash: h}, nil
	}
	// modify/delete, file/directory and similar structural conflicts: keep
	// whichever side still has content and report the path
	m.conflicts = append(m.conflicts, path)
	if o != nil {
		return o, nil
	}
	return t, nil
}

func (rp *Repo) blobBytes(h Hash) ([]byte, error) {
	b, err := rp.R.BlobObject(h)
	if err != nil {
		return nil, err
	}
	rd, err := b.Reader()
	if err != nil {
		return nil, err
	}
	defer rd.Close()
	return io.ReadAll(rd)
}

func (rp *Repo) writeBlob(data []byte) (Hash, error) {
	obj := rp.R.Storer.NewEncodedObject()
	obj.SetType(plumbing.BlobObject)
	obj.SetSize(int64(len(data)))
	w, err := obj.Writer()
	if err != nil {
		return Hash{}, err
	}
	if _, err := w.Write(data); err != nil {
		return Hash{}, err
	}
	if err := w.Close(); err != nil {
		return Hash{}, err
	}
	return rp.R.Storer.SetEncodedObject(obj)
}

// writeTree stores a tree, sorting entries the way git requires (directories
// compare as if their name ended in "/").
func (rp *Repo) writeTree(entries []object.TreeEntry) (Hash, error) {
	key := func(e object.TreeEntry) string {
		if e.Mode == filemode.Dir {
			return e.Name + "/"
		}
		return e.Name
	}
	slices.SortFunc(entries, func(a, b object.TreeEntry) int { return cmp.Compare(key(a), key(b)) })
	t := &object.Tree{Entries: entries}
	obj := rp.R.Storer.NewEncodedObject()
	if err := t.Encode(obj); err != nil {
		return Hash{}, err
	}
	return rp.R.Storer.SetEncodedObject(obj)
}
