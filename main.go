// Command gitk is a terminal git history browser in the spirit of gitk:
// a commit graph with branch lanes, commit details and diffs, plus mouse and
// keyboard history editing (reword, cherry-pick, revert, squash, reorder,
// drop) with undo.
package main

import (
	"flag"
	"fmt"
	"os"

	"ella.to/gitk/internal/repo"
	"ella.to/gitk/internal/ui"
)

func main() {
	dir := flag.String("C", ".", "open the repository containing `path`")
	headOnly := flag.Bool("head", false, "show only HEAD's history instead of all refs")
	light := flag.Bool("light", false, "use the light theme")
	rel := flag.Bool("relative", false, "show relative commit dates")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: gitk [flags] [revision...]\n\nBrowse and edit the history of the given branches, tags and commits\n(default: every branch, remote and tag).\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	rp, err := repo.Open(*dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gitk:", err)
		os.Exit(1)
	}
	opts := ui.Options{Light: *light, RelTime: *rel}
	switch {
	case flag.NArg() > 0:
		for _, rev := range flag.Args() {
			if _, err := rp.Resolve(rev); err != nil {
				fmt.Fprintln(os.Stderr, "gitk:", err)
				os.Exit(1)
			}
		}
		opts.Scope, opts.Revs = repo.ScopeRevs, flag.Args()
	case *headOnly:
		opts.Scope = repo.ScopeHead
	}
	if err := ui.Run(rp, opts); err != nil {
		fmt.Fprintln(os.Stderr, "gitk:", err)
		os.Exit(1)
	}
}
