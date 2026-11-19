package ui

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"ella.to/gitk/internal/repo"
	"github.com/sergi/go-diff/diffmatchpatch"
)

// dkind classifies a line of the details pane.
type dkind uint8

const (
	dMeta  dkind = iota // "Label:  value"
	dLink               // a Parent/Child line that jumps to Link
	dMsg                // commit message
	dBlank              //
	dFile               // file header band
	dHunk               // @@ ... @@
	dAdd
	dDel
	dCtx
	dInfo // "Binary file differs" and similar notes
	dTitle
)

// dline is one rendered line of the details pane.
type dline struct {
	kind     dkind
	label    string
	text     string
	link     repo.Hash
	old, new int
	hl       [][2]int // byte ranges of text to emphasize (intraline diff)
	file     int      // index into details.files, -1 for the header
}

// fileEntry is one row of the files pane.
type fileEntry struct {
	name       string
	path       string
	status     byte
	adds, dels int
	line       int // index of the file header in details.lines
	change     repo.Change
}

// details is everything the bottom panes show for the selected row.
type details struct {
	key   string
	lines []dline
	files []fileEntry
	err   error
}

// detailKind tells the builder what the selected row is.
type detailKind int

const (
	detCommit detailKind = iota
	detUnstaged
	detStaged
	detRange
)

type detailReq struct {
	kind    detailKind
	hash    repo.Hash
	other   repo.Hash // detRange: diff other → hash
	g       *repo.Graph
	changes []repo.Change
	ctx     int
}

func (r detailReq) key() string {
	switch r.kind {
	case detUnstaged:
		return fmt.Sprintf("unstaged/%d/%p", r.ctx, r.changes)
	case detStaged:
		return fmt.Sprintf("staged/%d/%p", r.ctx, r.changes)
	case detRange:
		return fmt.Sprintf("range/%d/%s/%s", r.ctx, r.other, r.hash)
	}
	return fmt.Sprintf("commit/%d/%s", r.ctx, r.hash)
}

// buildDetails runs on a worker goroutine. It only reads its arguments.
func buildDetails(rp *repo.Repo, r detailReq) *details {
	d := &details{key: r.key()}
	var diffs []repo.FileDiff
	var err error
	switch r.kind {
	case detCommit:
		c := r.g.Commit(r.hash)
		if c == nil {
			d.err = fmt.Errorf("commit %s not loaded", r.hash)
			return d
		}
		commitHeader(d, r.g, c)
		diffs, err = rp.CommitDiff(r.hash, r.ctx)
		if len(c.Parents) > 1 {
			d.lines = append(d.lines, dline{kind: dInfo, text: "merge commit: changes shown against the first parent", file: -1}, dline{kind: dBlank, file: -1})
		}
	case detRange:
		d.lines = append(d.lines,
			dline{kind: dTitle, text: fmt.Sprintf("Diff %s → %s", r.other.String()[:7], r.hash.String()[:7]), file: -1},
			dline{kind: dLink, label: "From:", text: shortLine(r.g, r.other), link: r.other, file: -1},
			dline{kind: dLink, label: "To:", text: shortLine(r.g, r.hash), link: r.hash, file: -1},
			dline{kind: dBlank, file: -1})
		diffs, err = rp.RangeDiff(r.other, r.hash, r.ctx)
	case detUnstaged:
		d.lines = append(d.lines,
			dline{kind: dTitle, text: "Local changes not staged for commit", file: -1},
			dline{kind: dInfo, text: "space: stage file · S: stage all · D: discard file · C: commit", file: -1},
			dline{kind: dBlank, file: -1})
		diffs, err = rp.UnstagedDiff(r.changes, r.ctx)
	case detStaged:
		d.lines = append(d.lines,
			dline{kind: dTitle, text: "Changes staged for commit", file: -1},
			dline{kind: dInfo, text: "space: unstage file · S: unstage all · C: commit · A: amend", file: -1},
			dline{kind: dBlank, file: -1})
		diffs, err = rp.StagedDiff(r.changes, r.ctx)
	}
	if err != nil {
		d.err = err
		return d
	}
	changeOf := map[string]repo.Change{}
	for _, c := range r.changes {
		changeOf[c.Path] = c
	}
	for i := range diffs {
		appendFile(d, &diffs[i], changeOf[diffs[i].Path])
	}
	if len(diffs) == 0 && r.kind != detUnstaged && r.kind != detStaged {
		d.lines = append(d.lines, dline{kind: dInfo, text: "no changes", file: -1})
	}
	return d
}

func shortLine(g *repo.Graph, h repo.Hash) string {
	if c := g.Commit(h); c != nil {
		return c.Short() + "  " + c.Subject
	}
	return h.String()[:7]
}

func commitHeader(d *details, g *repo.Graph, c *repo.Commit) {
	add := func(l dline) {
		l.file = -1
		d.lines = append(d.lines, l)
	}
	add(dline{kind: dMeta, label: "Commit:", text: c.Hash.String()})
	add(dline{kind: dMeta, label: "Author:", text: fmt.Sprintf("%s <%s>  %s", c.Author, c.AuthorEmail, absTime(c.AuthorTime))})
	if c.Committer != c.Author || c.CommitEmail != c.AuthorEmail || !c.CommitTime.Equal(c.AuthorTime) {
		add(dline{kind: dMeta, label: "Committer:", text: fmt.Sprintf("%s <%s>  %s", c.Committer, c.CommitEmail, absTime(c.CommitTime))})
	}
	for _, p := range c.Parents {
		add(dline{kind: dLink, label: "Parent:", text: shortLine(g, p), link: p})
	}
	for _, ch := range g.Children[c.Hash] {
		add(dline{kind: dLink, label: "Child:", text: shortLine(g, ch), link: ch})
	}
	var branches []string
	for _, r := range g.BranchesContaining(c.Hash, repo.RefLocal, repo.RefRemote) {
		branches = append(branches, r.Short)
	}
	if len(branches) > 0 {
		const show = 8
		s := strings.Join(branches[:min(len(branches), show)], ", ")
		if len(branches) > show {
			s += fmt.Sprintf(" (+%d more)", len(branches)-show)
		}
		add(dline{kind: dMeta, label: "Branches:", text: s})
	}
	if t := nearestTag(g, c.Hash, false); t != "" {
		add(dline{kind: dMeta, label: "Follows:", text: t})
	}
	if t := nearestTag(g, c.Hash, true); t != "" {
		add(dline{kind: dMeta, label: "Precedes:", text: t})
	}
	if c.Signed {
		add(dline{kind: dMeta, label: "Signed:", text: "yes (rewriting this commit drops the signature)"})
	}
	add(dline{kind: dBlank})
	for _, l := range strings.Split(strings.TrimRight(c.Message, "\n"), "\n") {
		add(dline{kind: dMsg, text: sanitize(l)})
	}
	add(dline{kind: dBlank})
}

// nearestTag finds the closest tag among ancestors (or descendants).
func nearestTag(g *repo.Graph, h repo.Hash, descendants bool) string {
	seen := map[repo.Hash]bool{h: true}
	queue := []repo.Hash{h}
	for steps := 0; len(queue) > 0 && steps < 200000; steps++ {
		cur := queue[0]
		queue = queue[1:]
		if cur != h {
			var tags []string
			for _, r := range g.RefsAt[cur] {
				if r.Kind == repo.RefTag {
					tags = append(tags, r.Short)
				}
			}
			if len(tags) > 0 {
				return strings.Join(tags, ", ")
			}
		}
		var next []repo.Hash
		if descendants {
			next = g.Children[cur]
		} else if c := g.Commit(cur); c != nil {
			next = c.Parents
		}
		for _, n := range next {
			if !seen[n] {
				seen[n] = true
				queue = append(queue, n)
			}
		}
	}
	return ""
}

func appendFile(d *details, f *repo.FileDiff, ch repo.Change) {
	fi := len(d.files)
	d.files = append(d.files, fileEntry{
		name: f.Name(), path: f.Path, status: f.Status, adds: f.Adds, dels: f.Dels,
		line: len(d.lines), change: ch,
	})
	add := func(l dline) {
		l.file = fi
		d.lines = append(d.lines, l)
	}
	add(dline{kind: dFile, text: f.Name(), label: string(f.Status)})
	switch {
	case f.Binary:
		add(dline{kind: dInfo, text: "binary file differs"})
	case f.TooBig:
		add(dline{kind: dInfo, text: "file too large to diff"})
	}
	if f.OldMode != 0 && f.NewMode != 0 && f.OldMode != f.NewMode {
		add(dline{kind: dInfo, text: fmt.Sprintf("mode %o → %o", uint32(f.OldMode), uint32(f.NewMode))})
	}
	start := len(d.lines)
	for _, l := range f.Lines {
		k := dCtx
		switch l.Kind {
		case repo.LineAdd:
			k = dAdd
		case repo.LineDel:
			k = dDel
		case repo.LineHunk:
			k = dHunk
		}
		add(dline{kind: k, text: sanitize(l.Text), old: l.Old, new: l.New})
	}
	intraline(d.lines[start:])
	add(dline{kind: dBlank})
}

// intraline pairs runs of deleted lines with the added lines that follow and
// marks the changed parts of each pair.
func intraline(lines []dline) {
	dmp := diffmatchpatch.New()
	for i := 0; i < len(lines); {
		if lines[i].kind != dDel {
			i++
			continue
		}
		j := i
		for j < len(lines) && lines[j].kind == dDel {
			j++
		}
		k := j
		for k < len(lines) && lines[k].kind == dAdd {
			k++
		}
		if k-j == j-i && k-j <= 8 {
			for n := range j - i {
				del, add := &lines[i+n], &lines[j+n]
				if len(del.text) > 400 || len(add.text) > 400 {
					continue
				}
				diffs := dmp.DiffCleanupSemantic(dmp.DiffMain(del.text, add.text, false))
				var same, total int
				for _, df := range diffs {
					total += utf8.RuneCountInString(df.Text)
					if df.Type == diffmatchpatch.DiffEqual {
						same += utf8.RuneCountInString(df.Text)
					}
				}
				if total == 0 || same*3 < total { // mostly rewritten: highlighting is noise
					continue
				}
				oi, ai := 0, 0
				for _, df := range diffs {
					n := len(df.Text)
					switch df.Type {
					case diffmatchpatch.DiffEqual:
						oi += n
						ai += n
					case diffmatchpatch.DiffDelete:
						del.hl = append(del.hl, [2]int{oi, oi + n})
						oi += n
					case diffmatchpatch.DiffInsert:
						add.hl = append(add.hl, [2]int{ai, ai + n})
						ai += n
					}
				}
			}
		}
		i = max(k, i+1)
	}
}

// fileAt returns the file index the details line i belongs to.
func (d *details) fileAt(i int) int {
	if d == nil || i < 0 || i >= len(d.lines) {
		return -1
	}
	return d.lines[i].file
}

// changesFor lists the changes shown in the files pane, in order.
func (d *details) changesFor(idx []int) []repo.Change {
	var out []repo.Change
	for _, i := range idx {
		if i >= 0 && i < len(d.files) {
			out = append(out, d.files[i].change)
		}
	}
	return slices.Clip(out)
}
