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
	headOnly := flag.Bool("head", false, "show only HEAD's history instead of all refs")
	light := flag.Bool("light", false, "use the light theme")
	rel := flag.Bool("relative", false, "show relative commit dates")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: gitk [flags] [path]\n\nBrowse and edit the history of the repository containing path (default .).\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	path := "."
	if flag.NArg() > 0 {
		path = flag.Arg(0)
	}
	rp, err := repo.Open(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gitk:", err)
		os.Exit(1)
	}
	opts := ui.Options{Light: *light, RelTime: *rel}
	if *headOnly {
		opts.Scope = repo.ScopeHead
	}
	if err := ui.Run(rp, opts); err != nil {
		fmt.Fprintln(os.Stderr, "gitk:", err)
		os.Exit(1)
	}
}
