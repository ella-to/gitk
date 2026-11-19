package ui

import (
	"fmt"

	"ella.to/gitk/internal/repo"
)

func (a *App) menuTitle() string {
	if n := len(a.picked); n > 1 {
		return fmt.Sprintf("%d commits selected", n)
	}
	if c := a.selCommit(); c != nil {
		return c.Short() + " " + truncate(c.Subject, 40)
	}
	return "Local changes"
}

func (a *App) commitMenu() []menuItem {
	c := a.selCommit()
	if c == nil {
		return a.localMenu()
	}
	picks := a.pickedCommits()
	pick := "Cherry-pick onto " + a.headLabel()
	if len(picks) > 1 {
		pick = fmt.Sprintf("Cherry-pick %d commits onto %s", len(picks), a.headLabel())
	}
	single := len(c.Parents) == 1
	markLabel := "Mark this commit"
	if a.marked == c.Hash {
		markLabel = "Clear mark"
	}
	return []menuItem{
		{label: pick, key: "c", action: func() { a.actCherryPick("") }},
		{label: "Cherry-pick onto", sub: a.branchesMenu},
		{label: "Revert", key: "v", action: a.actRevert},
		{},
		{label: "Reword message…", key: "r", action: a.actReword},
		{label: "Squash into parent", key: "s", disabled: !single, action: func() { a.actSquash(false) }},
		{label: "Fixup into parent", key: "f", disabled: !single, action: func() { a.actSquash(true) }},
		{label: "Move up", key: "K", disabled: !single, action: func() { a.actMove(true) }},
		{label: "Move down", key: "J", disabled: !single, action: func() { a.actMove(false) }},
		{label: "Drop…", key: "d", disabled: !single, danger: true, action: a.actDrop},
		{},
		{label: "New branch here…", key: "b", action: a.actNewBranch},
		{label: "New tag here…", key: "t", action: a.actNewTag},
		{label: "Checkout", key: "o", action: a.actCheckout},
		{label: "Reset " + a.headLabel() + " here", key: "x", sub: a.resetMenu},
		{},
		{label: markLabel, key: "m", action: a.actMark},
		{label: "Diff against marked", key: "=", disabled: a.marked.IsZero() || a.marked == c.Hash, action: a.actDiffMarked},
		{label: "Copy hash", key: "y", action: func() { a.copy(c.Hash.String(), "hash "+c.Short()) }},
		{label: "Copy subject", key: "Y", action: func() { a.copy(c.Subject, "subject") }},
	}
}

func (a *App) branchesMenu() []menuItem {
	var items []menuItem
	for _, r := range a.g.Refs {
		if r.Kind != repo.RefLocal || r.IsHead {
			continue
		}
		name := r.Short
		items = append(items, menuItem{label: name, action: func() { a.actCherryPick(name) }})
	}
	if len(items) == 0 {
		items = append(items, menuItem{label: "no other local branches", disabled: true})
	}
	return items
}

func (a *App) resetMenu() []menuItem {
	return []menuItem{
		{label: "Keep: move branch, keep local changes", key: "k", action: func() { a.actReset(repo.SyncSafe) }},
		{label: "Soft: changes become staged", key: "s", action: func() { a.actReset(repo.SyncNone) }},
		{label: "Mixed: reset index, keep files", key: "m", action: func() { a.actReset(repo.SyncMixed) }},
		{label: "Hard: discard local changes", key: "h", danger: true, action: func() { a.actReset(repo.SyncHard) }},
	}
}

func (a *App) refMenu(r repo.Ref) []menuItem {
	items := []menuItem{
		{label: "Checkout " + r.Short, key: "o", disabled: r.IsHead, action: func() { a.actCheckoutRef(r) }},
	}
	if r.Kind == repo.RefLocal && !r.IsHead {
		picks := a.pickedCommits()
		items = append(items, menuItem{
			label:  fmt.Sprintf("Cherry-pick %s here", plural2(len(picks), "selection", "commits")),
			key:    "c",
			action: func() { a.actCherryPick(r.Short) },
		})
	}
	if r.Kind == repo.RefLocal {
		items = append(items, menuItem{label: "Rename…", key: "R", action: func() { a.actRenameBranch(r) }})
	}
	if (r.Kind == repo.RefLocal && !r.IsHead) || r.Kind == repo.RefTag {
		items = append(items, menuItem{label: "Delete…", key: "D", danger: true, action: func() { a.actDeleteRef(r) }})
	}
	items = append(items,
		menuItem{},
		menuItem{label: "Copy name", key: "y", action: func() { a.copy(r.Short, r.Short) }},
	)
	return items
}

func (a *App) localMenu() []menuItem {
	unstaged := a.selKind() == rowUnstaged
	all := "Unstage all"
	if unstaged {
		all = "Stage all"
	}
	return []menuItem{
		{label: all, key: "S", action: a.actStageAll},
		{label: "Commit…", key: "C", action: func() { a.actCommit(false) }},
		{label: "Amend last commit…", key: "A", action: func() { a.actCommit(true) }},
	}
}

func (a *App) fileMenu() []menuItem {
	if a.det == nil || a.fileSel < 0 || a.fileSel >= len(a.det.files) {
		return nil
	}
	f := a.det.files[a.fileSel]
	items := []menuItem{}
	switch a.selKind() {
	case rowUnstaged:
		items = append(items,
			menuItem{label: "Stage", key: " ", action: a.actToggleStage},
			menuItem{label: "Discard changes…", key: "D", danger: true, action: a.actDiscard})
	case rowStaged:
		items = append(items, menuItem{label: "Unstage", key: " ", action: a.actToggleStage})
	}
	items = append(items, menuItem{label: "Copy path", key: "y", action: func() { a.copy(f.path, "path") }})
	return items
}
